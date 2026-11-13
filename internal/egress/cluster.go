package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
)

// DefaultClusterDomain is the cluster DNS domain when the resolver
// configuration names none. Nobody outside the cluster can register a name
// under it, so assuming it never opens a way in.
const DefaultClusterDomain = "cluster.local"

// KindClusterAddress is the RefusedError kind of a cluster address reached
// through a host name from outside the cluster (SPEC-0039-R06).
const KindClusterAddress = "cluster"

// Cluster is what the platform team configured about the cluster's network
// (SPEC-0039-R06, R09). With CIDRs, a host name from outside the cluster
// never connects to an address inside them, and a Service name is resolved
// only in the cluster's own DNS zone, so a name that also exists in public
// DNS cannot pass as one.
type Cluster struct {
	// CIDRs are the cluster's Service and Pod ranges
	// (global.endpoints.clusterCIDRs). Empty turns the checks off.
	CIDRs []netip.Prefix
	// Domain is the cluster DNS domain. Empty means DefaultClusterDomain.
	Domain string
	// CrossNamespace reports whether MCPServers in from may use Services
	// in to (global.endpoints.crossNamespace). Nil allows none.
	CrossNamespace func(from, to string) bool
}

// target says how to connect to one endpoint host.
type target struct {
	// host is the endpoint host the decision was made for.
	host string
	// dial replaces host when connecting: the Service name completed to
	// its name in the cluster's zone, with the trailing dot that keeps the
	// resolver's search path out. Empty connects to host.
	dial string
	// outside refuses cluster addresses.
	outside bool
	// outsideIfMissing connects to host as an outside name when dial does
	// not exist: a short name whose last label is also a namespace
	// (app.dev) and names no Service there.
	outsideIfMissing bool
}

type targetKey struct{}

// Context returns ctx for a connection to endpointHost made for an MCPServer
// in namespace. Transport reads it when it connects. A nil Cluster, or one
// without CIDRs, changes nothing.
func (c *Cluster) Context(ctx context.Context, namespace, endpointHost string) context.Context {
	if !c.enabled() {
		return ctx
	}
	return context.WithValue(ctx, targetKey{}, c.target(namespace, endpointHost))
}

func (c *Cluster) enabled() bool { return c != nil && len(c.CIDRs) > 0 }

func (c *Cluster) domain() string {
	if c.Domain != "" {
		return c.Domain
	}
	return DefaultClusterDomain
}

// contains reports whether addr is inside the cluster's ranges.
func (c *Cluster) contains(addr netip.Addr) bool {
	// Prefixes hold no zone, and Contains is false for a zoned address.
	addr = addr.WithZone("").Unmap()
	for _, prefix := range c.CIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// target classifies host the way cluster DNS resolves it from a pod:
// <service>.<namespace>.svc[.<domain>] and the pod forms are the cluster's,
// <service>.<namespace> is when the namespace is the MCPServer's own or one
// it may use, and a name without a dot is a Service next to the component.
// Everything else comes from outside the cluster.
func (c *Cluster) target(namespace, host string) target {
	host = normalizeHost(host)
	t := target{host: host}
	if _, err := netip.ParseAddr(host); err == nil {
		// The Operator compares IP literals with the ranges (SPEC-0029-R06).
		return t
	}
	labels := strings.Split(host, ".")
	if len(labels) == 1 {
		return t
	}
	for i := 2; i < len(labels); i++ {
		if labels[i] != "svc" && labels[i] != "pod" {
			continue
		}
		switch rest := strings.Join(labels[i+1:], "."); {
		case labels[i] == "pod" && i != 2:
			t.outside = true
		case rest == "":
			t.dial = host + "." + c.domain() + "."
		case rest == c.domain():
			t.dial = host + "."
		default:
			// <service>.<namespace>.svc.example.com is somebody's own zone.
			t.outside = true
		}
		return t
	}
	last := labels[len(labels)-1]
	if len(labels) <= 3 && (last == namespace || c.CrossNamespace != nil && c.CrossNamespace(namespace, last)) {
		t.dial = host + ".svc." + c.domain() + "."
		t.outsideIfMissing = true
		return t
	}
	t.outside = true
	return t
}

// control refuses what the egress policy refuses and, for outside names,
// the cluster's own addresses.
func (c *Cluster) control(allowLoopback, outside bool) func(network, address string, conn syscall.RawConn) error {
	base := Control(allowLoopback)
	return func(network, address string, conn syscall.RawConn) error {
		if err := base(network, address, conn); err != nil || !outside {
			return err
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if addr, err := netip.ParseAddr(host); err == nil && c.contains(addr) {
			return &RefusedError{Kind: KindClusterAddress, Addr: addr.Unmap()}
		}
		return nil
	}
}

// DialFunc connects address with dialer, like net.Dialer.DialContext. Tests
// replace it to resolve names without a network.
type DialFunc func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error)

func dialNet(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	return dialer.DialContext(ctx, network, address)
}

// DialContext returns the http.Transport DialContext that applies c to the
// hosts named with Context, connecting through dial (nil: the network).
func (c *Cluster) DialContext(allowLoopback bool, dial DialFunc) func(ctx context.Context, network, address string) (net.Conn, error) {
	if dial == nil {
		dial = dialNet
	}
	dialer := func(outside bool) *net.Dialer {
		return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: c.control(allowLoopback, outside)}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return dial(ctx, dialer(false), network, address)
		}
		t, ok := ctx.Value(targetKey{}).(target)
		if !ok || t.host != normalizeHost(host) {
			// A host nobody classified is from outside.
			t = target{host: host, outside: true}
		}
		if t.dial == "" {
			return dial(ctx, dialer(t.outside), network, address)
		}
		conn, err := dial(ctx, dialer(false), network, net.JoinHostPort(t.dial, port))
		var dnsErr *net.DNSError
		if err != nil && t.outsideIfMissing && errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return dial(ctx, dialer(true), network, address)
		}
		return conn, err
	}
}

// ClusterTransport is Transport with the checks of cluster for the endpoint
// hosts named with Cluster.Context. A request without that context, or to
// another host than it names, is connected as a name from outside.
func ClusterTransport(allowLoopback bool, cluster *Cluster) http.RoundTripper {
	return cluster.RoundTripper(allowLoopback, nil)
}

// RoundTripper is ClusterTransport connecting through dial (nil: the
// network).
func (c *Cluster) RoundTripper(allowLoopback bool, dial DialFunc) http.RoundTripper {
	if !c.enabled() {
		return Transport(allowLoopback)
	}
	inside, outside := Transport(allowLoopback), Transport(allowLoopback)
	inside.DialContext = c.DialContext(allowLoopback, dial)
	// No context: every name this transport connects is from outside.
	connect := c.DialContext(allowLoopback, dial)
	outside.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return connect(context.WithValue(ctx, targetKey{}, nil), network, address)
	}
	return &clusterTransport{inside: inside, outside: outside}
}

// clusterTransport keeps the connections to outside names apart from those
// to the cluster's own names. A transport reuses connections by host
// without asking DialContext again, so with one pool a connection opened to
// a Service could carry a request whose host, for its namespace, is a name
// from outside.
type clusterTransport struct {
	inside, outside *http.Transport
}

func (t *clusterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if _, err := netip.ParseAddr(host); err == nil {
		return t.inside.RoundTrip(req)
	}
	if target, ok := req.Context().Value(targetKey{}).(target); ok && !target.outside && target.host == normalizeHost(host) {
		return t.inside.RoundTrip(req)
	}
	return t.outside.RoundTrip(req)
}

// CloseIdleConnections closes the idle connections of both pools.
func (t *clusterTransport) CloseIdleConnections() {
	t.inside.CloseIdleConnections()
	t.outside.CloseIdleConnections()
}

// ResolvConfPath is the resolver configuration a pod gets from the kubelet.
const ResolvConfPath = "/etc/resolv.conf"

// ClusterDomainFrom returns the cluster DNS domain named by the search path
// in the resolver configuration at path (svc.<domain>), or
// DefaultClusterDomain when the file or the entry is missing.
func ClusterDomainFrom(path string) string {
	data, err := os.ReadFile(path) // #nosec G304 -- the path is the component's own resolver configuration.
	if err != nil {
		return DefaultClusterDomain
	}
	domain := DefaultClusterDomain
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "search" {
			continue
		}
		// The last search line counts, as it does for the resolver.
		for _, entry := range fields[1:] {
			if rest, ok := strings.CutPrefix(normalizeHost(entry), "svc."); ok && rest != "" {
				domain = rest
				break
			}
		}
	}
	return domain
}
