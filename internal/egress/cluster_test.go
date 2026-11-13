package egress_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kavrynt/kavrynt/internal/egress"
	"github.com/kavrynt/kavrynt/internal/egress/egresstest"
)

func testCluster() *egress.Cluster {
	return &egress.Cluster{
		CIDRs:          []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12"), netip.MustParsePrefix("fd00:10:96::/112")},
		CrossNamespace: func(from, to string) bool { return from == "team-a" && to == "shared" },
	}
}

// dial connects to host for an MCPServer in team-a and returns the refusal
// kind ("" when the connection was allowed) and the names dialed.
func dial(t *testing.T, cluster *egress.Cluster, hosts map[string]string, host string, classify bool) (string, []string) {
	t.Helper()
	network := &egresstest.Network{Hosts: hosts}
	ctx := context.Background()
	if classify {
		ctx = cluster.Context(ctx, "team-a", host)
	}
	_, err := cluster.DialContext(false, network.Dial)(ctx, "tcp", host+":8080")
	var refused *egress.RefusedError
	switch {
	case errors.As(err, &refused):
		return refused.Kind, network.Dialed()
	case errors.Is(err, egresstest.ErrNoListener):
		return "", network.Dialed()
	}
	return "error: " + err.Error(), network.Dialed()
}

// Verifies SPEC-0039-R06: a host name from outside the cluster never
// connects to a cluster address, whatever form it takes, and still connects
// to addresses outside the ranges.
func TestOutsideNamesNeverReachClusterAddresses(t *testing.T) {
	hosts := map[string]string{
		"10-96-0-10.nip.io":                "10.96.0.10",
		"rebind.example.com":               "10.100.3.4",
		"v6.example.com":                   "fd00:10:96::a",
		"mapped.example.com":               "::ffff:10.96.0.10",
		"nat64.example.com":                "64:ff9b::a60:a",
		"search.team-b.svc.example.com":    "10.96.0.10",
		"search.team-a.svc.example.com":    "10.96.0.10",
		"a.b.c.pod.example.com":            "10.96.0.10",
		"evil.io":                          "10.96.0.10",
		"search.team-b":                    "10.96.0.10",
		"deep.name.under.example.com":      "10.96.0.10",
		"public.example.com":               "203.0.113.7",
		"search.team-a.svc.cluster.remote": "203.0.113.7",
	}
	for host, address := range hosts {
		kind, dialed := dial(t, testCluster(), hosts, host, true)
		want := egress.KindClusterAddress
		if address == "203.0.113.7" {
			want = ""
		}
		if kind != want || !slices.Equal(dialed, []string{host}) {
			t.Errorf("%s (%s): refusal %q after dialing %v, want %q after dialing the name itself", host, address, kind, dialed, want)
		}
	}
}

// Verifies SPEC-0039-R06: the address is checked at every connection, so a
// name that resolved outside the cluster is refused once it resolves inside.
func TestOutsideNameIsCheckedAtEveryConnection(t *testing.T) {
	hosts := map[string]string{"rebind.example.com": "203.0.113.7"}
	if kind, _ := dial(t, testCluster(), hosts, "rebind.example.com", true); kind != "" {
		t.Fatalf("a public address was refused: %s", kind)
	}
	hosts["rebind.example.com"] = "10.96.0.10"
	if kind, _ := dial(t, testCluster(), hosts, "rebind.example.com", true); kind != egress.KindClusterAddress {
		t.Fatalf("after the name moved to a cluster address: refusal %q", kind)
	}
}

// Verifies SPEC-0039-R10: a Service name is resolved in the cluster's own
// zone, as a full name, so the same name in public DNS is never asked.
func TestServiceNamesResolveInTheClusterZone(t *testing.T) {
	hosts := map[string]string{
		"search.team-a.svc.cluster.local.":       "10.96.0.10",
		"pod-0.search.team-a.svc.cluster.local.": "10.100.0.5",
		"search.shared.svc.cluster.local.":       "10.96.0.11",
		"10-100-0-5.team-a.pod.cluster.local.":   "10.100.0.5",
		// The same short names exist in public DNS and point at another
		// team's Service: they must not be asked.
		"search.team-a": "10.96.0.99",
		"search.shared": "10.96.0.99",
	}
	for host, want := range map[string]string{
		"search.team-a":                         "search.team-a.svc.cluster.local.",
		"Search.Team-A.":                        "search.team-a.svc.cluster.local.",
		"search.team-a.svc":                     "search.team-a.svc.cluster.local.",
		"search.team-a.svc.cluster.local":       "search.team-a.svc.cluster.local.",
		"pod-0.search.team-a":                   "pod-0.search.team-a.svc.cluster.local.",
		"pod-0.search.team-a.svc.cluster.local": "pod-0.search.team-a.svc.cluster.local.",
		"search.shared":                         "search.shared.svc.cluster.local.",
		"10-100-0-5.team-a.pod":                 "10-100-0-5.team-a.pod.cluster.local.",
	} {
		kind, dialed := dial(t, testCluster(), hosts, host, true)
		if kind != "" || !slices.Equal(dialed, []string{want}) {
			t.Errorf("%s: refusal %q after dialing %v, want %s allowed", host, kind, dialed, want)
		}
	}
}

// Verifies SPEC-0039-R10: a short name that names no Service in the
// cluster's zone is an outside name, such as a public name whose last label
// is also the server's namespace.
func TestShortNameWithoutAServiceIsAnOutsideName(t *testing.T) {
	cluster := testCluster()
	network := &egresstest.Network{Hosts: map[string]string{"app.dev": "203.0.113.7", "evil.dev": "10.96.0.10"}}
	for host, want := range map[string]string{"app.dev": "", "evil.dev": egress.KindClusterAddress} {
		_, err := cluster.DialContext(false, network.Dial)(cluster.Context(context.Background(), "dev", host), "tcp", host+":443")
		var refused *egress.RefusedError
		kind := ""
		if errors.As(err, &refused) {
			kind = refused.Kind
		} else if !errors.Is(err, egresstest.ErrNoListener) {
			t.Fatalf("%s: %v", host, err)
		}
		if kind != want {
			t.Errorf("%s: refusal %q, want %q", host, kind, want)
		}
	}
	if want := []string{"app.dev.svc.cluster.local.", "app.dev", "evil.dev.svc.cluster.local.", "evil.dev"}; !slices.Equal(sorted(network.Dialed()), sorted(want)) {
		t.Fatalf("dialed %v, want the cluster zone first and then the name: %v", network.Dialed(), want)
	}
}

func sorted(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return values
}

// Verifies SPEC-0039-R06: names without a dot and IP literals are not
// outside names, and a host nobody classified, such as another host than
// the endpoint's, is.
func TestUnclassifiedHostsAreOutsideNames(t *testing.T) {
	hosts := map[string]string{"search": "10.96.0.10", "other.example.com": "10.96.0.10"}
	if kind, _ := dial(t, testCluster(), hosts, "search", true); kind != "" {
		t.Errorf("a name without a dot was refused: %s", kind)
	}
	if kind, _ := dial(t, testCluster(), hosts, "10.96.0.10", true); kind != "" {
		t.Errorf("an IP literal, which the Operator checks, was refused: %s", kind)
	}
	if kind, _ := dial(t, testCluster(), hosts, "169.254.169.254", true); kind != "cloud metadata" {
		t.Errorf("the egress policy no longer refuses metadata addresses: %q", kind)
	}
	if kind, _ := dial(t, testCluster(), hosts, "other.example.com", false); kind != egress.KindClusterAddress {
		t.Errorf("a host without a classification reached a cluster address: %q", kind)
	}
	cluster := testCluster()
	network := &egresstest.Network{Hosts: hosts}
	ctx := cluster.Context(context.Background(), "team-a", "search.team-a.svc")
	// A pod of the Service, connected by address for session affinity.
	if _, err := cluster.DialContext(false, network.Dial)(ctx, "tcp", "10.100.0.5:8080"); !errors.Is(err, egresstest.ErrNoListener) {
		t.Errorf("a pod address of the classified Service was refused: %v", err)
	}
	_, err := cluster.DialContext(false, network.Dial)(ctx, "tcp", "other.example.com:80")
	var refused *egress.RefusedError
	if !errors.As(err, &refused) || refused.Kind != egress.KindClusterAddress {
		t.Errorf("a host other than the classified one reached a cluster address: %v", err)
	}
}

// Verifies SPEC-0039-R06: without configured ranges nothing changes.
func TestNoClusterRangesChangesNothing(t *testing.T) {
	for _, cluster := range []*egress.Cluster{nil, {}} {
		ctx := context.Background()
		if got := cluster.Context(ctx, "team-a", "rebind.example.com"); got != ctx {
			t.Errorf("%+v: the context changed", cluster)
		}
		if transport, ok := egress.ClusterTransport(false, cluster).(*http.Transport); !ok || transport.DialContext == nil || transport.Proxy != nil {
			t.Errorf("%+v: not the egress transport", cluster)
		}
	}
}

// Verifies SPEC-0039-R10: the cluster domain comes from the search path the
// kubelet writes, and is cluster.local without one.
func TestClusterDomainFromTheResolverConfiguration(t *testing.T) {
	for content, want := range map[string]string{
		"search team-a.svc.k8s.example svc.k8s.example k8s.example\nnameserver 10.96.0.10\noptions ndots:5\n": "k8s.example",
		"search kavrynt-system.svc.cluster.local svc.cluster.local cluster.local corp.example\n":              "cluster.local",
		"nameserver 192.0.2.53\nsearch corp.example\n":                                                        "cluster.local",
		"": "cluster.local",
	} {
		path := filepath.Join(t.TempDir(), "resolv.conf")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := egress.ClusterDomainFrom(path); got != want {
			t.Errorf("%q: domain %q, want %q", content, got, want)
		}
	}
	if got := egress.ClusterDomainFrom(filepath.Join(t.TempDir(), "missing")); got != egress.DefaultClusterDomain {
		t.Errorf("missing file: %q", got)
	}
}

// Verifies SPEC-0039-R06: a connection opened to a Service is not reused for
// a request whose host, for its namespace, is a name from outside, although
// the transport pools connections by host.
func TestConnectionsAreNotReusedAcrossClassifications(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer upstream.Close()
	cluster := testCluster()
	network := &egresstest.Network{Listener: upstream.Listener.Addr().String(), Hosts: map[string]string{
		"search.dev.svc.cluster.local.": "10.96.0.10",
		"search.dev":                    "10.96.0.10",
	}}
	client := &http.Client{Transport: cluster.RoundTripper(false, network.Dial)}
	get := func(namespace string) error {
		req, err := http.NewRequestWithContext(cluster.Context(context.Background(), namespace, "search.dev"), http.MethodGet, "http://search.dev:8080/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}
	// Twice, so the second request finds the first one's idle connection.
	for range 2 {
		if err := get("dev"); err != nil {
			t.Fatalf("the namespace's own Service: %v", err)
		}
	}
	var refused *egress.RefusedError
	if err := get("team-a"); !errors.As(err, &refused) || refused.Kind != egress.KindClusterAddress {
		t.Fatalf("another namespace's request for the same host: %v, want a refusal", err)
	}
	if dialed := network.Dialed(); !slices.Equal(dialed, []string{"search.dev.svc.cluster.local.", "search.dev"}) {
		t.Fatalf("dialed %v, want one connection to the Service and one refused connection to the name", dialed)
	}
}
