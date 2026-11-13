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

// ErrServiceUnknown reports that a Service an endpoint names could not be
// read, so the endpoint is not allowed for now (SPEC-0039-R04).
var ErrServiceUnknown = errors.New("Service could not be read")

// MaxExternalNames is how many ExternalName Services an endpoint may pass
// through (SPEC-0039-R01).
const MaxExternalNames = 5

// EndpointSliceController is the managed-by label value of the
// EndpointSlices Kubernetes writes for a Service with a selector.
const EndpointSliceController = "endpointslice-controller.k8s.io"

// ServiceKey names a Service.
type ServiceKey struct {
	Namespace string
	Name      string
}

// Service is what endpoint scope reads of a Service and its EndpointSlices.
type Service struct {
	// ExternalName is spec.externalName of an ExternalName Service.
	ExternalName string
	// Selector reports whether the Service selects pods, so Kubernetes
	// writes its EndpointSlices.
	Selector bool
	// Endpoints are the endpoints of every EndpointSlice labelled for the
	// Service, whoever wrote it.
	Endpoints []ServiceEndpoint
}

// ServiceEndpoint is one endpoint of an EndpointSlice.
type ServiceEndpoint struct {
	// TargetNamespace is targetRef.namespace, or "" without a targetRef.
	// Whoever writes the slice chooses it.
	TargetNamespace string
	// Addresses are the endpoint's addresses as written.
	Addresses []string
	// Managed reports whether the slice carries the managed-by label of
	// the EndpointSlice controller.
	Managed bool
}

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
	// ClusterDomain is the cluster DNS domain, "cluster.local" when empty.
	ClusterDomain string
	// Services reads a Service and its EndpointSlices; found is false when
	// the Service does not exist. Nil follows no Service (SPEC-0039).
	Services func(ctx context.Context, key ServiceKey) (service Service, found bool, err error)
}

// Allows reports whether MCPServers in from may use endpoints in to.
func (p Policy) Allows(from, to string) bool {
	if from == to {
		return true
	}
	for _, pair := range p.CrossNamespace {
		if pair.From == from && pair.To == to {
			return true
		}
	}
	return false
}

// Endpoint returns an error when endpoint, used by an MCPServer in
// namespace, reaches another namespace that is not allowed, or a cluster
// address (SPEC-0029-R04 to R06). The error names namespaces, never
// addresses or Service names.
func (p Policy) Endpoint(ctx context.Context, namespace, endpoint string) error {
	_, err := p.EndpointServices(ctx, namespace, endpoint)
	return err
}

// EndpointServices is Endpoint, and also follows the Service the endpoint
// names: through ExternalName Services to where they lead, and into the
// EndpointSlices of each Service on the way (SPEC-0039-R01, R02, R08). It
// returns the Services it looked at, found or not, so the caller can check
// again when one of them changes.
func (p Policy) EndpointServices(ctx context.Context, namespace, endpoint string) ([]ServiceKey, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("spec.endpoint cannot be parsed")
	}
	host := normalizeHost(parsed.Hostname())
	target, err := p.host(ctx, namespace, host, "spec.endpoint")
	if err != nil || target == "" || p.Services == nil {
		return nil, err
	}
	key, ok := p.serviceOf(host, target)
	if !ok {
		return nil, nil
	}
	var seen []ServiceKey
	for hops := 0; ; hops++ {
		seen = append(seen, key)
		service, found, err := p.Services(ctx, key)
		if err != nil {
			return seen, fmt.Errorf("cannot read the Service spec.endpoint leads to in namespace %q: %w", key.Namespace, ErrServiceUnknown)
		}
		if !found {
			return seen, nil
		}
		if service.ExternalName == "" {
			return seen, p.endpoints(namespace, key, service)
		}
		if hops == MaxExternalNames {
			return seen, fmt.Errorf("spec.endpoint leads through more than %d ExternalName Services", MaxExternalNames)
		}
		const through = "spec.endpoint leads through an ExternalName Service whose target"
		next := normalizeHost(service.ExternalName)
		if _, err := p.host(ctx, namespace, next, through); err != nil {
			return seen, err
		}
		// DNS answers an ExternalName Service with its target as a full
		// name: only <service>.<namespace>.svc.<cluster domain> is a
		// Service, and every other name is resolved outside the cluster.
		key, ok = p.clusterService(next)
		if !ok {
			if len(p.ClusterCIDRs) > 0 {
				return seen, fmt.Errorf("%s is not a Service's full name (<service>.<namespace>.svc.%s); with global.endpoints.clusterCIDRs, register the outside name itself as spec.endpoint", through, p.clusterDomain())
			}
			return seen, nil
		}
	}
}

// host returns the namespace host resolves into ("" for an external name),
// or an error when an MCPServer in namespace may not use it (SPEC-0029-R04
// to R06). what names the checked value in the message.
func (p Policy) host(ctx context.Context, namespace, host, what string) (string, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if p.inCluster(addr) {
			return "", fmt.Errorf("%s is an address in the cluster's Service or Pod ranges (global.endpoints.clusterCIDRs); use the Service's name", what)
		}
		return "", nil
	}
	target, err := p.namespaceOf(ctx, host, what)
	if err != nil {
		return "", err
	}
	if target != "" && !p.Allows(namespace, target) {
		return "", fmt.Errorf("%s reaches namespace %q, and MCPServers in namespace %q may use endpoints only in their own namespace unless global.endpoints.crossNamespace allows it", what, target, namespace)
	}
	return target, nil
}

// endpoints refuses a Service whose EndpointSlices lead out of the
// namespaces an MCPServer in namespace may use (SPEC-0039-R02). Anyone who
// may write EndpointSlices in the Service's namespace can add one, with any
// targetRef, so a cluster address counts as the Service's own only in a
// slice Kubernetes manages for a Service with a selector.
func (p Policy) endpoints(namespace string, key ServiceKey, service Service) error {
	for _, endpoint := range service.Endpoints {
		if target := endpoint.TargetNamespace; target != "" && !p.Allows(namespace, target) {
			return fmt.Errorf("spec.endpoint names a Service with an EndpointSlice that reaches namespace %q, and MCPServers in namespace %q may use endpoints only in their own namespace unless global.endpoints.crossNamespace allows it", target, namespace)
		}
		if service.Selector && endpoint.Managed {
			continue
		}
		for _, raw := range endpoint.Addresses {
			if addr, err := netip.ParseAddr(raw); err == nil && p.inCluster(addr) {
				return fmt.Errorf("spec.endpoint names a Service in namespace %q with an EndpointSlice that Kubernetes does not manage and that lists an address in the cluster's Service or Pod ranges (global.endpoints.clusterCIDRs); give the Service a selector", key.Namespace)
			}
		}
	}
	return nil
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func (p Policy) clusterDomain() string {
	if p.ClusterDomain != "" {
		return p.ClusterDomain
	}
	return "cluster.local"
}

// clusterService returns the Service a full cluster DNS name belongs to:
// <service>.<namespace>.svc.<cluster domain>, or a name below it.
func (p Policy) clusterService(host string) (ServiceKey, bool) {
	labels := strings.Split(host, ".")
	for i := 2; i < len(labels); i++ {
		if labels[i] == "svc" {
			if strings.Join(labels[i+1:], ".") != p.clusterDomain() {
				break
			}
			return ServiceKey{Namespace: labels[i-1], Name: labels[i-2]}, true
		}
	}
	return ServiceKey{}, false
}

// serviceOf returns the Service that host, which resolves into namespace,
// names from the Gateway's pod. A name with svc followed by another zone
// than the cluster's is attributed to the namespace it claims (SPEC-0029)
// and names no Service: DNS resolves it outside the cluster.
func (p Policy) serviceOf(host, namespace string) (ServiceKey, bool) {
	labels := strings.Split(host, ".")
	for i := 2; i < len(labels); i++ {
		switch labels[i] {
		case "svc":
			if rest := strings.Join(labels[i+1:], "."); rest != "" && rest != p.clusterDomain() {
				return ServiceKey{}, false
			}
			return ServiceKey{Namespace: namespace, Name: labels[i-2]}, true
		case "pod":
			return ServiceKey{}, false
		}
	}
	switch len(labels) {
	case 1, 2:
		return ServiceKey{Namespace: namespace, Name: labels[0]}, true
	case 3:
		return ServiceKey{Namespace: namespace, Name: labels[1]}, true
	}
	return ServiceKey{}, false
}

// DependsOnNamespace reports whether Endpoint's answer for endpoint depends
// on whether a namespace exists: a two- or three-label name without svc or
// pod labels, which is external only while no namespace of its last label
// exists. Such endpoints are checked again, since the namespace may appear.
func DependsOnNamespace(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) != 2 && len(labels) != 3 {
		return false
	}
	for _, label := range labels[1:] {
		if label == "svc" || label == "pod" {
			return false
		}
	}
	return true
}

// inCluster reports whether addr is inside the cluster's ranges, which
// belong to some namespace the Operator cannot name.
func (p Policy) inCluster(addr netip.Addr) bool {
	// Prefixes hold no zone, and Contains is false for a zoned address.
	addr = addr.WithZone("").Unmap()
	for _, prefix := range p.ClusterCIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// namespaceOf returns the namespace an in-cluster name resolves into, or
// "" for an external name, the way cluster DNS resolves it from the
// Gateway's pod.
func (p Policy) namespaceOf(ctx context.Context, host, what string) (string, error) {
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
				return "", fmt.Errorf("%s is a pod name that does not name an address; use the Service's name", what)
			}
			if p.inCluster(addr) {
				return "", fmt.Errorf("%s is an address in the cluster's Service or Pod ranges (global.endpoints.clusterCIDRs); use the Service's name", what)
			}
			return "", nil
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
			return "", fmt.Errorf("cannot tell whether %s names a Service in namespace %q: %w", what, namespace, ErrNamespaceUnknown)
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
