// Package egress limits the addresses the Gateway and the Operator connect to
// for MCPServer endpoints. Endpoints come from anyone who can create an
// MCPServer, so they are untrusted: without this check an endpoint (or a DNS
// name that resolves later) could reach cloud instance metadata or the
// component's own loopback listeners.
//
// The check runs on the resolved address at dial time, so DNS rebinding and
// redirects cannot bypass it.
package egress

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// metadataAddrs are cloud instance metadata services. The link-local range
// is blocked as a whole; these are named so a refusal can say why.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), // AWS, Azure, GCP, and others
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IMDS over IPv6
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud metadata
}

// Allowed reports whether an MCP endpoint may resolve to addr. Unspecified,
// link-local (which holds 169.254.169.254), multicast, and known metadata
// addresses are always refused; loopback only when allowLoopback is false.
func Allowed(addr netip.Addr, allowLoopback bool) bool {
	return refusedKind(addr, allowLoopback) == ""
}

// refusedKind names the kind of address addr is when it is refused, and is
// empty when it is allowed.
func refusedKind(addr netip.Addr, allowLoopback bool) string {
	addr = addr.Unmap()
	for _, blocked := range metadataAddrs {
		if addr == blocked {
			return "cloud metadata"
		}
	}
	switch {
	case !addr.IsValid(), addr.IsUnspecified():
		return "unspecified"
	case addr.IsLinkLocalUnicast():
		return "link-local"
	case addr.IsMulticast():
		return "multicast"
	case addr.IsLoopback() && !allowLoopback:
		return "loopback"
	}
	return ""
}

// RefusedError is a connection the egress policy refused. Kind names the
// kind of address (cloud metadata, link-local, loopback, unspecified, or
// multicast), so callers can explain the refusal without the address
// (SPEC-0027-R07).
type RefusedError struct {
	Kind string
	Addr netip.Addr
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("egress: connections to %s (%s) are not allowed for MCP endpoints", e.Addr, e.Kind)
}

// Control is a net.Dialer Control function that refuses connections to
// addresses Allowed rejects with a *RefusedError.
func Control(allowLoopback bool) func(network, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("egress: unexpected dial address %q", address)
		}
		if kind := refusedKind(addr, allowLoopback); kind != "" {
			return &RefusedError{Kind: kind, Addr: addr.Unmap()}
		}
		return nil
	}
}

// Transport returns a clone of http.DefaultTransport whose connections are
// checked with Control.
func Transport(allowLoopback bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   Control(allowLoopback),
	}
	transport.DialContext = dialer.DialContext
	return transport
}
