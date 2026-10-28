package scope

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// namespaces exist in the test cluster.
func namespaces(names ...string) func(context.Context, string) (bool, error) {
	return func(_ context.Context, name string) (bool, error) {
		for _, n := range names {
			if n == name {
				return true, nil
			}
		}
		return false, nil
	}
}

func testPolicy() Policy {
	return Policy{GatewayNamespace: "kavrynt-system", NamespaceExists: namespaces("team-a", "team-b", "kavrynt-system")}
}

// Verifies SPEC-0029-R04: endpoints that name a Service, a headless pod of
// one, or a short name in another namespace are refused; the server's own
// namespace and external names are not.
func TestEndpointNamespaces(t *testing.T) {
	p := testPolicy()
	for endpoint, want := range map[string]string{
		"http://search.team-a.svc:8080/mcp":                    "",
		"http://search.team-a.svc.cluster.local:8080/mcp":      "",
		"http://search.team-a:8080/mcp":                        "",
		"http://pod-0.search.team-a.svc.cluster.local/mcp":     "",
		"https://mcp.example.com/mcp":                          "",
		"https://example.com/mcp":                              "",
		"http://search.team-a.svc.cluster.local./mcp":          "",
		"http://search.team-b.svc:8080/mcp":                    "team-b",
		"http://SEARCH.TEAM-B.SVC.cluster.local/mcp":           "team-b",
		"http://search.team-b:8080/mcp":                        "team-b",
		"http://pod-0.search.team-b.svc.corp.example:9000/mcp": "team-b",
		"http://kavrynt-gateway:8080/mcp":                      "kavrynt-system",
		// Verifies SPEC-0029-R04: <pod>.<service>.<namespace> resolves
		// through the search path to a headless Service's pod.
		"http://pod-0.search.team-b/mcp":                     "team-b",
		"http://pod-0.search.team-a/mcp":                     "",
		"https://mcp.api.example/mcp":                        "",
		"http://search.kavrynt-system.svc.cluster.local/mcp": "kavrynt-system",
	} {
		err := p.Endpoint(context.Background(), "team-a", endpoint)
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: refused: %v", endpoint, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), `namespace "`+want+`"`)):
			t.Errorf("%s: err = %v, want a refusal naming namespace %s", endpoint, err, want)
		}
	}
	if err := p.Endpoint(context.Background(), "team-a", "http://search.team-b.svc/mcp"); err == nil || strings.Contains(err.Error(), "search") {
		t.Errorf("the refusal must name the namespaces only: %v", err)
	}
}

// Verifies SPEC-0029-R04: a namespace lookup that fails refuses instead of
// letting the endpoint through.
func TestEndpointNamespaceLookupFailsClosed(t *testing.T) {
	p := testPolicy()
	p.NamespaceExists = func(context.Context, string) (bool, error) { return false, errors.New("forbidden") }
	err := p.Endpoint(context.Background(), "team-a", "http://search.team-b:8080/mcp")
	if err == nil || !errors.Is(err, ErrNamespaceUnknown) {
		t.Fatalf("a failed namespace lookup: err = %v, want ErrNamespaceUnknown", err)
	}
}

// Verifies SPEC-0029-R05.
func TestCrossNamespaceExceptions(t *testing.T) {
	p := testPolicy()
	p.CrossNamespace = []NamespacePair{{From: "team-a", To: "team-b"}}
	if err := p.Endpoint(context.Background(), "team-a", "http://search.team-b.svc/mcp"); err != nil {
		t.Fatalf("allowed pair refused: %v", err)
	}
	if err := p.Endpoint(context.Background(), "team-b", "http://search.team-a.svc/mcp"); err == nil {
		t.Fatal("a pair allowed the opposite direction")
	}
	if pairs, err := ParseNamespacePairs("team-a:team-b, team-c:team-a"); err != nil || len(pairs) != 2 || pairs[1] != (NamespacePair{From: "team-c", To: "team-a"}) {
		t.Fatalf("ParseNamespacePairs = %v, %v", pairs, err)
	}
	for _, bad := range []string{"team-a", "team-a:", ":team-b", "Team-A:team-b"} {
		if _, err := ParseNamespacePairs(bad); err == nil {
			t.Errorf("ParseNamespacePairs(%q) accepted", bad)
		}
	}
}

// Verifies SPEC-0029-R06: IP literals and pod DNS names inside the cluster's
// ranges are refused when the ranges are configured.
func TestClusterAddresses(t *testing.T) {
	p := testPolicy()
	cluster := []string{
		"http://10.96.0.10:8080/mcp",
		"http://10-244-1-7.team-b.pod.cluster.local:8080/mcp",
		"http://10-244-1-7.team-a.pod:8080/mcp",
		"http://[fd00:10:96::a]:8080/mcp",
		// Verifies SPEC-0029-R06: a zone does not hide a cluster address.
		"http://[fd00:10:96::a%25eth0]:8080/mcp",
	}
	for _, endpoint := range cluster {
		if err := p.Endpoint(context.Background(), "team-a", endpoint); err != nil {
			t.Errorf("%s refused without cluster ranges: %v", endpoint, err)
		}
	}
	p.ClusterCIDRs = []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12"), netip.MustParsePrefix("10.244.0.0/16"), netip.MustParsePrefix("fd00:10:96::/112")}
	for _, endpoint := range cluster {
		err := p.Endpoint(context.Background(), "team-a", endpoint)
		if err == nil || strings.Contains(err.Error(), "10.") || strings.Contains(err.Error(), "fd00") {
			t.Errorf("%s: err = %v, want a refusal without the address", endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://192.0.2.10:8080/mcp", "http://not-an-ip.team-a.pod:8080/mcp"} {
		err := p.Endpoint(context.Background(), "team-a", endpoint)
		if strings.Contains(endpoint, "192.0.2.10") && err != nil {
			t.Errorf("%s outside the cluster ranges refused: %v", endpoint, err)
		}
		if strings.Contains(endpoint, "not-an-ip") && err == nil {
			t.Errorf("%s: a pod name without an address was let through", endpoint)
		}
	}
}

// Verifies SPEC-0029-R07.
func TestAudiences(t *testing.T) {
	audiences, err := ParseAudiences(`{"team-a": ["https://tickets.example.com"], "*": ["urn:shared"]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		namespace, audience string
		allowed             bool
	}{
		{"team-a", "https://tickets.example.com", true},
		{"team-a", "urn:shared", true},
		{"team-b", "urn:shared", true},
		{"team-b", "https://tickets.example.com", false},
		{"team-a", "https://tickets.example.com/", false},
	} {
		if got := audiences.Allowed(c.namespace, c.audience); got != c.allowed {
			t.Errorf("Allowed(%s, %s) = %v, want %v", c.namespace, c.audience, got, c.allowed)
		}
	}
	if none, err := ParseAudiences(""); err != nil || none.Allowed("team-a", "urn:shared") {
		t.Fatalf("an empty list allowed an audience: %v", err)
	}
	for _, bad := range []string{`[]`, `{"team-a": "https://t"}`, `{"Team-A": ["https://t"]}`, `{"team-a": ["not a uri"]}`} {
		if _, err := ParseAudiences(bad); err == nil {
			t.Errorf("ParseAudiences(%s) accepted", bad)
		}
	}
}

// Verifies SPEC-0029-R06: IPv4-mapped prefixes match IPv4 addresses, and
// ones that cannot match any address are refused.
func TestParsePrefix(t *testing.T) {
	for cidr, want := range map[string]string{
		"10.1.2.3/8":          "10.0.0.0/8",
		"::ffff:10.0.0.0/104": "10.0.0.0/8",
		"fd00:10:96::/112":    "fd00:10:96::/112",
	} {
		if got, err := ParsePrefix(cidr); err != nil || got.String() != want {
			t.Errorf("ParsePrefix(%q) = %v, %v; want %s", cidr, got, err, want)
		}
	}
	for _, cidr := range []string{"10.0.0.1", "::ffff:0:0/80", "fe80::/10%eth0"} {
		if _, err := ParsePrefix(cidr); err == nil {
			t.Errorf("ParsePrefix(%q) accepted", cidr)
		}
	}
}
