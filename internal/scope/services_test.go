package scope

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// services is the test cluster's Services, by "<namespace>/<name>".
type services map[string]Service

func (s services) read(_ context.Context, key ServiceKey) (Service, bool, error) {
	service, found := s[key.Namespace+"/"+key.Name]
	return service, found, nil
}

func servicePolicy(s services) Policy {
	p := testPolicy()
	p.Services = s.read
	return p
}

var clusterRanges = []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12"), netip.MustParsePrefix("10.244.0.0/16")}

func refusal(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Errorf("refused: %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want a refusal with %q", err, want)
		return
	}
	// Messages name namespaces, never Services or addresses.
	for _, secret := range []string{"alias", "10.", "other.team-b"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the message quotes %q: %v", secret, err)
		}
	}
}

// Verifies SPEC-0039-R01: an ExternalName Service is followed to where it
// leads, and another namespace there is refused unless the pair is allowed.
func TestExternalNameServicesAreFollowed(t *testing.T) {
	cluster := services{
		"team-a/alias":     {ExternalName: "other.team-b.svc.cluster.local"},
		"team-a/dotted":    {ExternalName: "Other.Team-B.svc.cluster.local."},
		"team-a/short":     {ExternalName: "other.team-b"},
		"team-a/own":       {ExternalName: "search.team-a.svc.cluster.local"},
		"team-a/search":    {Selector: true},
		"team-a/outside":   {ExternalName: "mcp.example.com"},
		"team-a/address":   {ExternalName: "10.96.0.10"},
		"team-a/hop":       {ExternalName: "alias.team-a.svc.cluster.local"},
		"kavrynt-system/x": {ExternalName: "other.team-b.svc.cluster.local"},
	}
	p := servicePolicy(cluster)
	for endpoint, want := range map[string]string{
		"http://alias.team-a.svc.cluster.local:8080/mcp": `namespace "team-b"`,
		"http://alias.team-a.svc:8080/mcp":               `namespace "team-b"`,
		"http://alias.team-a:8080/mcp":                   `namespace "team-b"`,
		"http://pod-0.alias.team-a.svc:8080/mcp":         `namespace "team-b"`,
		"http://dotted.team-a.svc:8080/mcp":              `namespace "team-b"`,
		"http://short.team-a.svc:8080/mcp":               `namespace "team-b"`,
		"http://hop.team-a.svc:8080/mcp":                 `namespace "team-b"`,
		"http://own.team-a.svc:8080/mcp":                 "",
		"http://search.team-a.svc:8080/mcp":              "",
		"http://outside.team-a.svc:8080/mcp":             "",
		"http://missing.team-a.svc:8080/mcp":             "",
	} {
		t.Run(endpoint, func(t *testing.T) {
			refusal(t, p.Endpoint(context.Background(), "team-a", endpoint), want)
		})
	}
	// A name without a dot is a Service in the Gateway's namespace.
	refusal(t, p.Endpoint(context.Background(), "kavrynt-system", "http://x:8080/mcp"), `namespace "team-b"`)

	p.CrossNamespace = []NamespacePair{{From: "team-a", To: "team-b"}}
	refusal(t, p.Endpoint(context.Background(), "team-a", "http://alias.team-a.svc:8080/mcp"), "")

	p = servicePolicy(cluster)
	p.ClusterCIDRs = clusterRanges
	refusal(t, p.Endpoint(context.Background(), "team-a", "http://address.team-a.svc:8080/mcp"), "Service or Pod ranges")
}

// Verifies SPEC-0039-R01: at most five ExternalName Services are followed;
// a longer chain, or a loop, is refused.
func TestExternalNameChainsAreBounded(t *testing.T) {
	cluster := services{"team-a/end": {Selector: true}, "team-a/loop": {ExternalName: "loop.team-a.svc.cluster.local"}}
	for i := 1; i <= 6; i++ {
		next := "end"
		if i > 1 {
			next = fmt.Sprintf("hop%d", i-1)
		}
		cluster[fmt.Sprintf("team-a/hop%d", i)] = Service{ExternalName: next + ".team-a.svc.cluster.local"}
	}
	p := servicePolicy(cluster)
	seen, err := p.EndpointServices(context.Background(), "team-a", "http://hop5.team-a.svc:8080/mcp")
	if err != nil || len(seen) != 6 || seen[5] != (ServiceKey{Namespace: "team-a", Name: "end"}) {
		t.Fatalf("five ExternalName Services: %v, %v", seen, err)
	}
	for _, endpoint := range []string{"http://hop6.team-a.svc:8080/mcp", "http://loop.team-a.svc:8080/mcp"} {
		if err := p.Endpoint(context.Background(), "team-a", endpoint); err == nil || !strings.Contains(err.Error(), "more than 5 ExternalName Services") {
			t.Errorf("%s: err = %v", endpoint, err)
		}
	}
}

// Verifies SPEC-0039-R02: a Service whose EndpointSlices lead into another
// namespace is refused, also at the end of an ExternalName chain. A
// targetRef is written by whoever writes the slice, so a cluster address
// counts as the Service's own only in a slice Kubernetes manages for a
// Service with a selector.
func TestEndpointSlicesAreFollowed(t *testing.T) {
	pod := func(namespace, address string, managed bool) ServiceEndpoint {
		return ServiceEndpoint{TargetNamespace: namespace, Addresses: []string{address}, Managed: managed}
	}
	cluster := services{
		"team-a/own":       {Selector: true, Endpoints: []ServiceEndpoint{pod("team-a", "10.244.1.5", true)}},
		"team-a/manual":    {Endpoints: []ServiceEndpoint{pod("team-b", "10.244.2.9", false)}},
		"team-a/lying":     {Endpoints: []ServiceEndpoint{pod("team-a", "10.244.2.9", false)}},
		"team-a/labelled":  {Endpoints: []ServiceEndpoint{pod("team-a", "10.244.2.9", true)}},
		"team-a/added":     {Selector: true, Endpoints: []ServiceEndpoint{pod("team-a", "10.244.1.5", true), pod("", "10.244.2.9", false)}},
		"team-a/bare":      {Endpoints: []ServiceEndpoint{pod("", "10.96.0.10", false)}},
		"team-a/database":  {Endpoints: []ServiceEndpoint{pod("", "192.0.2.10", false), pod("", "db.example.com", false)}},
		"team-a/alias":     {ExternalName: "manual.team-a.svc.cluster.local"},
		"team-a/zoned":     {Endpoints: []ServiceEndpoint{pod("", "::ffff:10.96.0.10", false)}},
		"team-a/unmanaged": {Selector: true, Endpoints: []ServiceEndpoint{pod("team-b", "10.244.2.9", true)}},
	}
	with := servicePolicy(cluster)
	with.ClusterCIDRs = clusterRanges
	without := servicePolicy(cluster)
	const ranges, other = "Kubernetes does not manage", `namespace "team-b"`
	for name, want := range map[string][2]string{
		"own":       {"", ""},
		"manual":    {other, other},
		"alias":     {other, other},
		"unmanaged": {other, other},
		"lying":     {ranges, ""},
		"labelled":  {ranges, ""},
		"added":     {ranges, ""},
		"bare":      {ranges, ""},
		"zoned":     {ranges, ""},
		"database":  {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			endpoint := "http://" + name + ".team-a.svc:8080/mcp"
			refusal(t, with.Endpoint(context.Background(), "team-a", endpoint), want[0])
			refusal(t, without.Endpoint(context.Background(), "team-a", endpoint), want[1])
		})
	}
	with.CrossNamespace = []NamespacePair{{From: "team-a", To: "team-b"}}
	refusal(t, with.Endpoint(context.Background(), "team-a", "http://unmanaged.team-a.svc:8080/mcp"), "")
	// An allowed pair does not vouch for an address nobody can attribute.
	refusal(t, with.Endpoint(context.Background(), "team-a", "http://manual.team-a.svc:8080/mcp"), ranges)
}

// Verifies SPEC-0039-R03: the Services an endpoint led through are
// reported, found or not, so their changes can be followed.
func TestEndpointServicesReportsWhatToWatch(t *testing.T) {
	p := servicePolicy(services{"team-a/alias": {ExternalName: "next.team-a.svc.cluster.local"}})
	seen, err := p.EndpointServices(context.Background(), "team-a", "http://alias.team-a.svc:8080/mcp")
	if want := []ServiceKey{{"team-a", "alias"}, {"team-a", "next"}}; err != nil || !slices.Equal(seen, want) {
		t.Fatalf("seen %v, %v; want %v", seen, err, want)
	}
	// The last is somebody's own zone, not the cluster's Service.
	for _, endpoint := range []string{"https://mcp.example.com/mcp", "http://192.0.2.10/mcp", "http://alias.team-a.svc.example.com/mcp"} {
		if seen, err := p.EndpointServices(context.Background(), "team-a", endpoint); err != nil || len(seen) != 0 {
			t.Errorf("%s: seen %v, %v", endpoint, seen, err)
		}
	}
}

// Verifies SPEC-0039-R04: a Service that cannot be read is not allowed.
func TestUnreadableServiceIsRefused(t *testing.T) {
	p := testPolicy()
	p.Services = func(context.Context, ServiceKey) (Service, bool, error) {
		return Service{}, false, errors.New("forbidden: 10.96.0.1")
	}
	err := p.Endpoint(context.Background(), "team-a", "http://search.team-a.svc:8080/mcp")
	if !errors.Is(err, ErrServiceUnknown) || strings.Contains(err.Error(), "10.96") {
		t.Fatalf("err = %v, want ErrServiceUnknown without the cause", err)
	}
}

// Verifies SPEC-0039-R08: with cluster ranges, an ExternalName Service that
// leads to anything but a Service's full name is refused, because DNS
// resolves it outside the cluster and the Gateway sees only the Service.
func TestExternalNameToAnOutsideNameIsRefusedWithClusterRanges(t *testing.T) {
	cluster := services{
		"team-a/outside": {ExternalName: "10-96-0-10.nip.io"},
		"team-a/short":   {ExternalName: "search.team-a"},
		"team-a/zone":    {ExternalName: "search.team-a.svc.example.com"},
		"team-a/partial": {ExternalName: "search.team-a.svc"},
		"team-a/full":    {ExternalName: "search.team-a.svc.cluster.local"},
		"team-a/chain":   {ExternalName: "outside.team-a.svc.cluster.local"},
	}
	with := servicePolicy(cluster)
	with.ClusterCIDRs = clusterRanges
	without := servicePolicy(cluster)
	for name, refused := range map[string]bool{"outside": true, "short": true, "zone": true, "partial": true, "chain": true, "full": false} {
		endpoint := "http://" + name + ".team-a.svc:8080/mcp"
		want := ""
		if refused {
			want = "not a Service's full name"
		}
		refusal(t, with.Endpoint(context.Background(), "team-a", endpoint), want)
		refusal(t, without.Endpoint(context.Background(), "team-a", endpoint), "")
	}
	with.ClusterDomain = "k8s.example"
	refusal(t, with.Endpoint(context.Background(), "team-a", "http://full.team-a.svc:8080/mcp"), "<service>.<namespace>.svc.k8s.example")
}
