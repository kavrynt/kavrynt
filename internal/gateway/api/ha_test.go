package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Verifies SPEC-0008-R01.
func TestDrainingFailsReadinessButKeepsServing(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	if rr := get(handler, "/readyz"); rr.Code != http.StatusOK {
		t.Fatalf("readyz before draining = %d, want 200", rr.Code)
	}

	handler.SetDraining()

	rr := get(handler, "/readyz")
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"status":"draining"`) {
		t.Fatalf("readyz while draining = %d %s, want 503 draining", rr.Code, rr.Body.String())
	}
	if rr := get(handler, "/healthz"); rr.Code != http.StatusOK {
		t.Fatalf("healthz while draining = %d, want 200", rr.Code)
	}
	rr = post(handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("MCP call while draining = %d after %d upstream calls, want it proxied", rr.Code, *calls)
	}
	if got := rr.Header().Get("Connection"); got != "close" {
		t.Fatalf("Connection while draining = %q, want close so the client reconnects elsewhere", got)
	}
}

// Verifies SPEC-0008-R06.
func TestTrafficNamesTheReplica(t *testing.T) {
	handler, _ := demoHandler(t, okResponse)
	handler.SetInstance("kavrynt-gateway-7d9f-abcde")
	post(handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)

	rr := get(handler, "/v1/traffic")
	var traffic struct {
		Instance string          `json:"instance"`
		Requests []TrafficRecord `json:"requests"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &traffic); err != nil {
		t.Fatal(err)
	}
	if traffic.Instance != "kavrynt-gateway-7d9f-abcde" || len(traffic.Requests) != 1 {
		t.Fatalf("GET /v1/traffic = %s, want the instance and one request", rr.Body.String())
	}
}
