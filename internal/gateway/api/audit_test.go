package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/gateway/audit"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func enableAudit(t *testing.T, h *Handler) (*audit.Writer, *lockedBuffer) {
	t.Helper()
	out := &lockedBuffer{}
	w, err := audit.New(out, "gw-test/1", audit.Reject, 100)
	if err != nil {
		t.Fatal(err)
	}
	h.EnableAudit(w)
	return w, out
}

func events(t *testing.T, w *audit.Writer, out *lockedBuffer) []auditv1.Event {
	t.Helper()
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var list []auditv1.Event
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var e auditv1.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("not a JSON event: %s", line)
		}
		list = append(list, e)
	}
	return list
}

// Verifies SPEC-0013-R01, SPEC-0013-R03, SPEC-0013-R04, and SPEC-0013-R05.
func TestEveryDecisionIsAudited(t *testing.T) {
	handler, _, mint := authHandler(t, true)
	enablePolicy(t, handler, testPolicy)
	w, out := enableAudit(t, handler)

	token := mint(nil)
	allowed := callRoute(handler, "/mcp/team-a.demo", token)
	denied := callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"client_id": "etl"}))
	callRoute(handler, "/mcp/team-a.demo", "")
	callRoute(handler, "/mcp/team-z.unknown", mint(map[string]any{"aud": authResource + "/mcp/team-z.unknown"}))
	if allowed.Code != http.StatusOK || denied.Code != http.StatusForbidden {
		t.Fatalf("calls: %d, %d", allowed.Code, denied.Code)
	}

	list := events(t, w, out)
	if len(list) != 4 {
		t.Fatalf("%d events for 4 requests:\n%s", len(list), out.String())
	}
	first := list[0]
	if first.RequestID != allowed.Header().Get(requestIDHeader) || len(first.RequestID) != 32 {
		t.Fatalf("request_id %q, X-Request-Id %q", first.RequestID, allowed.Header().Get(requestIDHeader))
	}
	if first.Subject != "user-42" || first.ClientID != "support-agent" || first.Issuer != authIssuer ||
		first.Route != (auditv1.Route{Namespace: "team-a", Name: "demo"}) || first.HTTPMethod != "POST" ||
		first.MCPMethod != "tools/call" || first.Tool != "echo" || first.Decision != auditv1.DecisionAllow ||
		first.Outcome != "ok" || first.Status != 200 || first.PolicyRevision == "" || first.Instance != "gw-test/1" {
		t.Fatalf("allowed event = %+v", first)
	}
	if list[1].Decision != auditv1.DecisionDeny || list[1].ClientID != "etl" || list[1].Reason != "tool not allowed for client" || list[1].Status != 403 {
		t.Fatalf("denied event = %+v", list[1])
	}
	if list[2].Decision != auditv1.DecisionUnauthenticated || list[2].Subject != "" || list[2].Status != 401 {
		t.Fatalf("unauthenticated event = %+v", list[2])
	}
	if list[3].Decision != auditv1.DecisionInvalid || list[3].Status != 404 {
		t.Fatalf("unknown route event = %+v", list[3])
	}
	for _, secret := range append(strings.Split(token, "."), "secret-argument", "Authorization", "Bearer") {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("audit output contains %q", secret)
		}
	}
	summary, _ := auditv1.Verify(strings.NewReader(out.String()))
	if len(summary.Breaks) != 0 {
		t.Fatalf("chain breaks: %v", summary.Breaks)
	}
}

// Verifies SPEC-0013-R01: a missing upstream credential is audited too.
func TestCredentialRefusalIsAudited(t *testing.T) {
	handler, _ := credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{}, nil)
	w, out := enableAudit(t, handler)
	callRoute(handler, "/mcp/team-a.search", "")
	list := events(t, w, out)
	if len(list) != 1 || list[0].Decision != auditv1.DecisionUnavailable || list[0].Outcome != "credential_unavailable" {
		t.Fatalf("events = %+v", list)
	}
}

// Verifies SPEC-0013-R05: requests get an ID without audit as well.
func TestRequestIDWithoutAudit(t *testing.T) {
	handler, _ := demoHandler(t, okResponse)
	if id := callRoute(handler, "/mcp/team-a.demo", "").Header().Get(requestIDHeader); len(id) != 32 {
		t.Fatalf("X-Request-Id = %q", id)
	}
}

type stalledWriter struct{ release chan struct{} }

func (s stalledWriter) Write(p []byte) (int, error) {
	<-s.release
	return len(p), nil
}

// Verifies SPEC-0013-R08: with a full queue, requests are refused before
// they reach the MCP server.
func TestFullAuditQueueRejectsRequests(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	release := make(chan struct{})
	w, err := audit.New(stalledWriter{release}, "gw", audit.Reject, 1)
	if err != nil {
		t.Fatal(err)
	}
	handler.EnableAudit(w)
	codes := []int{}
	for i := 0; i < 4; i++ {
		codes = append(codes, callRoute(handler, "/mcp/team-a.demo", "").Code)
		time.Sleep(10 * time.Millisecond)
	}
	if codes[len(codes)-1] != http.StatusServiceUnavailable || *calls >= 4 {
		t.Fatalf("codes %v after %d upstream calls, want later requests refused", codes, *calls)
	}
	close(release)
	_ = w.Close(context.Background())
}
