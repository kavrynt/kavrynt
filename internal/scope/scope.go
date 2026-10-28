// Package scope decides which MCPServer endpoints and token exchange
// audiences a namespace may use (SPEC-0029). The MCPServer spec is written
// by namespace owners and untrusted: an endpoint that reaches another
// namespace's Services, or an audience the platform team did not allow for
// the namespace, keeps the server from being routed.
package scope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ErrNamespaceUnknown reports that a namespace an endpoint may name could
// not be read, so the endpoint is not allowed for now.
var ErrNamespaceUnknown = errors.New("namespace could not be read")

// NamespacePair allows MCPServers in From to use endpoints in To.
type NamespacePair struct {
	From string
	To   string
}

// Policy holds the platform team's endpoint rules.
type Policy struct {
	// CrossNamespace lists the allowed exceptions (SPEC-0029-R05).
	CrossNamespace []NamespacePair
	// ClusterCIDRs are the cluster's Service and Pod ranges; IP endpoints
	// inside them are refused (SPEC-0029-R06).
	ClusterCIDRs []netip.Prefix
	// GatewayNamespace is where the Gateway resolves single-label names.
	GatewayNamespace string
	// NamespaceExists tells a <service>.<namespace> name from an external
	// two-label name.
	NamespaceExists func(ctx context.Context, name string) (bool, error)
	// Audiences are the token exchange audiences namespaces may request
	// (SPEC-0029-R07).
	Audiences Audiences
}

// Endpoint returns an error when endpoint, used by an MCPServer in
// namespace, reaches another namespace that is not allowed, or a cluster
// address (SPEC-0029-R04 to R06). The error names namespaces, never
// addresses or Service names.
func (p Policy) Endpoint(ctx context.Context, namespace, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("spec.endpoint cannot be parsed")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return p.address(addr)
	}
	target, err := p.namespaceOf(ctx, host)
	if err != nil {
		return err
	}
	if target == "" || target == namespace {
		return nil
	}
	for _, pair := range p.CrossNamespace {
		if pair.From == namespace && pair.To == target {
			return nil
		}
	}
	return fmt.Errorf("spec.endpoint reaches namespace %q, and MCPServers in namespace %q may use endpoints only in their own namespace unless global.endpoints.crossNamespace allows it", target, namespace)
}

// address refuses cluster addresses, which belong to some namespace the
// Operator cannot name.
func (p Policy) address(addr netip.Addr) error {
	// Prefixes hold no zone, and Contains is false for a zoned address.
	addr = addr.WithZone("").Unmap()
	for _, prefix := range p.ClusterCIDRs {
		if prefix.Contains(addr) {
			return fmt.Errorf("spec.endpoint is an address in the cluster's Service or Pod ranges (global.endpoints.clusterCIDRs); use the Service's name")
		}
	}
	return nil
}

// namespaceOf returns the namespace an in-cluster name resolves into, or
// "" for an external name, the way cluster DNS resolves it from the
// Gateway's pod.
func (p Policy) namespaceOf(ctx context.Context, host string) (string, error) {
	labels := strings.Split(host, ".")
	for i := 2; i < len(labels); i++ {
		switch labels[i] {
		case "svc":
			// <service>.<namespace>.svc[...] and headless
			// <pod>.<service>.<namespace>.svc[...].
			return labels[i-1], nil
		case "pod":
			// <a-b-c-d>.<namespace>.pod[...]: DNS may answer for any
			// namespace label, so only the address counts.
			if i != 2 {
				return "", nil
			}
			addr, err := netip.ParseAddr(strings.ReplaceAll(labels[0], "-", "."))
			if err != nil {
				addr, err = netip.ParseAddr(strings.ReplaceAll(labels[0], "-", ":"))
			}
			if err != nil {
				return "", fmt.Errorf("spec.endpoint is a pod name that does not name an address; use the Service's name")
			}
			return "", p.address(addr)
		}
	}
	switch len(labels) {
	case 1:
		return p.GatewayNamespace, nil
	case 2, 3:
		// Through the search path, <service>.<namespace> and
		// <pod>.<service>.<namespace> name a Service, or a headless
		// Service's pod, in the last label's namespace, when it exists.
		namespace := labels[len(labels)-1]
		if len(validation.IsDNS1123Label(namespace)) > 0 {
			return "", nil
		}
		if p.NamespaceExists == nil {
			return namespace, nil
		}
		exists, err := p.NamespaceExists(ctx, namespace)
		if err != nil {
			// Unknown is not allowed: the name may be a Service.
			return "", fmt.Errorf("cannot tell whether spec.endpoint names a Service in namespace %q: %w", namespace, ErrNamespaceUnknown)
		}
		if exists {
			return namespace, nil
		}
	}
	return "", nil
}

// ParsePrefix reads a CIDR for --cluster-cidrs and --console-trusted-proxies,
// masked. An IPv4-mapped IPv6 prefix becomes its IPv4 prefix, since
// addresses are unmapped before they are matched; one shorter than /96
// matches no address and is refused.
func ParsePrefix(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a CIDR", cidr)
	}
	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("%q is an IPv4-mapped prefix shorter than /96, which matches no address", cidr)
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix.Masked(), nil
}

// ParseNamespacePairs reads "from:to,from:to".
func ParseNamespacePairs(value string) ([]NamespacePair, error) {
	var pairs []NamespacePair
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		from, to, ok := strings.Cut(item, ":")
		if !ok || len(validation.IsDNS1123Label(from)) > 0 || len(validation.IsDNS1123Label(to)) > 0 {
			return nil, fmt.Errorf("%q is not <from namespace>:<to namespace>", item)
		}
		pairs = append(pairs, NamespacePair{From: from, To: to})
	}
	return pairs, nil
}

// Audiences maps namespaces, or "*" for all, to the token exchange
// audiences their MCPServers may request (SPEC-0029-R07).
type Audiences map[string][]string

// Allowed reports whether an MCPServer in namespace may request audience.
func (a Audiences) Allowed(namespace, audience string) bool {
	for _, key := range []string{namespace, "*"} {
		for _, allowed := range a[key] {
			if allowed == audience {
				return true
			}
		}
	}
	return false
}

// ParseAudiences reads the JSON object of --token-exchange-audiences; empty
// allows none.
func ParseAudiences(value string) (Audiences, error) {
	audiences := Audiences{}
	if strings.TrimSpace(value) == "" {
		return audiences, nil
	}
	if err := json.Unmarshal([]byte(value), &audiences); err != nil {
		return nil, fmt.Errorf("token exchange audiences must map namespaces to lists of URIs: %w", err)
	}
	for namespace, list := range audiences {
		if namespace != "*" && len(validation.IsDNS1123Label(namespace)) > 0 {
			return nil, fmt.Errorf("token exchange audiences: %q is not a namespace", namespace)
		}
		for _, audience := range list {
			if u, err := url.Parse(audience); err != nil || u.Scheme == "" || strings.ContainsAny(audience, " \t\n") {
				return nil, fmt.Errorf("token exchange audiences: %q is not an absolute URI", audience)
			}
		}
	}
	return audiences, nil
}
