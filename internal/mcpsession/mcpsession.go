// Package mcpsession pins MCP sessions to pods (SPEC-0026). It picks the
// ready pods of a Service from its EndpointSlices and names each by a tag
// that the Gateway puts in front of the server's Mcp-Session-Id, so any
// Gateway replica can send a session's requests to the pod that started it
// without shared state.
package mcpsession

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"sort"
	"strings"

	"github.com/kavrynt/kavrynt/internal/egress"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ServiceNameLabel selects the EndpointSlices of one Service.
const ServiceNameLabel = discoveryv1.LabelServiceName

// tagLength is the length of a tag in hex characters.
const tagLength = 16

// Endpoint is one ready pod of a Service.
type Endpoint struct {
	// Tag names the pod in session IDs without revealing its address or
	// name (SPEC-0026-R06).
	Tag  string
	Addr netip.AddrPort
}

// TagOf returns the tag of the pod with uid.
func TagOf(uid types.UID) string {
	sum := sha256.Sum256([]byte(uid))
	return hex.EncodeToString(sum[:])[:tagLength]
}

// Ready returns the ready pods of a single-port Service from its
// EndpointSlices, sorted by tag, at the slice's port (the target port).
// Slices with more than one port are skipped: their ports are target ports,
// which cannot be matched to the Service port the endpoint names without
// reading the Service. Endpoints without a pod, not ready, not serving,
// terminating, or at an address the egress policy refuses are left out
// (SPEC-0026-R03, R08).
func Ready(slices []discoveryv1.EndpointSlice, allowLoopback bool) []Endpoint {
	seen := map[string]bool{}
	var ready []Endpoint
	for i := range slices {
		s := &slices[i]
		if s.AddressType != discoveryv1.AddressTypeIPv4 && s.AddressType != discoveryv1.AddressTypeIPv6 {
			continue
		}
		if len(s.Ports) != 1 || s.Ports[0].Port == nil || *s.Ports[0].Port < 1 || *s.Ports[0].Port > 65535 {
			continue
		}
		// HTTP needs TCP; an absent protocol means TCP.
		if protocol := s.Ports[0].Protocol; protocol != nil && *protocol != corev1.ProtocolTCP {
			continue
		}
		target := uint16(*s.Ports[0].Port) // #nosec G115 -- checked to be a port number.
		for _, e := range s.Endpoints {
			// Absent conditions mean ready and serving, not terminating.
			c := e.Conditions
			if (c.Ready != nil && !*c.Ready) || (c.Serving != nil && !*c.Serving) || (c.Terminating != nil && *c.Terminating) {
				continue
			}
			if e.TargetRef == nil || e.TargetRef.Kind != "Pod" || e.TargetRef.UID == "" || len(e.Addresses) == 0 {
				continue
			}
			addr, err := netip.ParseAddr(e.Addresses[0])
			if err != nil || !egress.Allowed(addr, allowLoopback) {
				continue
			}
			tag := TagOf(e.TargetRef.UID)
			if seen[tag] {
				continue
			}
			seen[tag] = true
			ready = append(ready, Endpoint{Tag: tag, Addr: netip.AddrPortFrom(addr.Unmap(), target)})
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].Tag < ready[j].Tag })
	return ready
}

// Find returns the endpoint with tag.
func Find(endpoints []Endpoint, tag string) (Endpoint, bool) {
	for _, e := range endpoints {
		if e.Tag == tag {
			return e, true
		}
	}
	return Endpoint{}, false
}

// Wrap returns the session ID the client gets for the server's id on the
// pod with tag.
func Wrap(tag, id string) string {
	return tag + "." + id
}

// Unwrap splits a session ID from a client into the pod's tag and the
// server's session ID. Anything else reports false (SPEC-0026-R07).
func Unwrap(id string) (tag, serverID string, ok bool) {
	tag, serverID, found := strings.Cut(id, ".")
	if !found || len(tag) != tagLength || serverID == "" || strings.Trim(tag, "0123456789abcdef") != "" {
		return "", "", false
	}
	return tag, serverID, true
}
