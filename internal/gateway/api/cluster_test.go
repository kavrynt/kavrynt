package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/egress"
	"github.com/kavrynt/kavrynt/internal/egress/egresstest"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

// Verifies SPEC-0039-R06 and SPEC-0039-R10: with the cluster's ranges, a
// route whose endpoint host comes from outside the cluster answers 502 when
// the name resolves to a cluster address, and the MCP server behind that
// address receives nothing; outside names at other addresses and the
// namespace's own Services work, the Services resolved in the cluster zone.
func TestOutsideNameAtAClusterAddressIsNotProxied(t *testing.T) {
	var received atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upstream.Close()
	network := &egresstest.Network{Listener: upstream.Listener.Addr().String(), Hosts: map[string]string{
		"rebind.example.com":             "10.96.0.10",
		"public.example.com":             "203.0.113.7",
		"demo.team-a.svc.cluster.local.": "10.96.0.20",
		"demo.team-a":                    "10.96.0.10",
		"demo.team-b.svc.cluster.local.": "10.96.0.10",
		"demo.team-b":                    "10.96.0.10",
		"demo.shared.svc.cluster.local.": "10.96.0.30",
		"demo.team-a.svc.example.com":    "10.96.0.10",
	}}
	cluster := &egress.Cluster{
		CIDRs:          []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12")},
		CrossNamespace: func(from, to string) bool { return from == "team-a" && to == "shared" },
	}
	routes := map[string]struct {
		endpoint string
		status   int
		dialed   []string
	}{
		"team-a.rebind": {"http://rebind.example.com:8080/mcp", http.StatusBadGateway, []string{"rebind.example.com"}},
		"team-a.zone":   {"http://demo.team-a.svc.example.com:8080/mcp", http.StatusBadGateway, []string{"demo.team-a.svc.example.com"}},
		// Not Ready for the Operator; if routed all the same, an outside name.
		"team-a.other":  {"http://demo.team-b:8080/mcp", http.StatusBadGateway, []string{"demo.team-b"}},
		"team-a.public": {"http://public.example.com:8080/mcp", http.StatusOK, []string{"public.example.com"}},
		"team-a.short":  {"http://demo.team-a:8080/mcp", http.StatusOK, []string{"demo.team-a.svc.cluster.local."}},
		"team-a.shared": {"http://demo.shared:8080/mcp", http.StatusOK, []string{"demo.shared.svc.cluster.local."}},
	}
	for name, c := range routes {
		t.Run(name, func(t *testing.T) {
			table := routing.NewTable()
			table.Replace([]model.Route{{Name: name, Version: "0.1.0", Transport: "http", Endpoint: c.endpoint}}, time.Now())
			// A transport of its own, so no connection is reused.
			handler := NewHandler(table, &http.Client{Transport: cluster.RoundTripper(false, network.Dial)}, Metadata{})
			handler.SetCluster(cluster)
			before, dialedBefore := received.Load(), len(network.Dialed())
			rr := callRoute(handler, "/mcp/"+name, "")
			if rr.Code != c.status {
				t.Fatalf("status %d, want %d: %s", rr.Code, c.status, rr.Body.String())
			}
			if got := received.Load() - before; (c.status == http.StatusOK) != (got == 1) {
				t.Fatalf("the MCP server received %d requests for status %d", got, c.status)
			}
			if dialed := network.Dialed()[dialedBefore:]; !slices.Equal(dialed, c.dialed) {
				t.Fatalf("dialed %v, want %v", dialed, c.dialed)
			}
		})
	}
}

// Verifies SPEC-0039-R06: without configured ranges, routes connect as
// before.
func TestWithoutClusterRangesOutsideNamesConnectAsBefore(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	handler.SetCluster(&egress.Cluster{})
	if rr := callRoute(handler, "/mcp/team-a.demo", ""); rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("status %d after %d calls", rr.Code, *calls)
	}
}
