package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	"github.com/kavrynt/kavrynt/internal/mcpsession"
	"k8s.io/apimachinery/pkg/types"
)

// statefulPods are the pods of one Service running an MCP server that keeps
// its sessions in memory: a pod answers 404 to a session it did not start.
type statefulPods struct {
	mu       sync.Mutex
	pods     []mcpsession.Endpoint
	sessions map[string]string // server session ID -> pod address
	next     int
	seen     []*http.Request
}

func newStatefulPods(n int) *statefulPods {
	p := &statefulPods{sessions: map[string]string{}}
	for i := range n {
		p.pods = append(p.pods, mcpsession.Endpoint{
			Tag:  mcpsession.TagOf(types.UID(fmt.Sprintf("uid-%d", i))),
			Addr: netip.MustParseAddrPort(fmt.Sprintf("10.0.0.%d:8080", i+1)),
		})
	}
	return p
}

func (p *statefulPods) Ready(_ context.Context, service kavryntv1alpha1.ServiceRef) ([]mcpsession.Endpoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if service != (kavryntv1alpha1.ServiceRef{Namespace: "team-a", Name: "demo", Port: 8080}) {
		return nil, fmt.Errorf("unexpected service %+v", service)
	}
	return append([]mcpsession.Endpoint(nil), p.pods...), nil
}

func (p *statefulPods) RoundTrip(req *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, req)
	body, _ := io.ReadAll(req.Body)
	respond := func(status int, header http.Header, body string) (*http.Response, error) {
		if header == nil {
			header = http.Header{}
		}
		header.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	if strings.Contains(string(body), `"method":"initialize"`) {
		p.next++
		id := fmt.Sprintf("session-%d", p.next)
		p.sessions[id] = req.URL.Host
		return respond(http.StatusOK, http.Header{"Mcp-Session-Id": []string{id}}, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}
	if p.sessions[req.Header.Get("Mcp-Session-Id")] != req.URL.Host {
		return respond(http.StatusNotFound, nil, `{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"Session not found"}}`)
	}
	if req.Method == http.MethodDelete {
		return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: http.NoBody}, nil
	}
	return respond(http.StatusOK, nil, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
}

// affinityHandler is one Gateway replica routing team-a.demo, a Service of
// pods, with MCPSession.
func affinityHandler(pods *statefulPods, affinity bool) *Handler {
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.team-a.svc:8080/mcp", SessionAffinity: affinity}}, time.Now())
	h := NewHandler(table, &http.Client{Transport: pods}, Metadata{})
	h.EnableSessionAffinity(pods)
	return h
}

func send(h *Handler, method, body, session string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/mcp/team-a.demo", reader)
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

const (
	initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}`
	callBody       = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{}}}`
)

// Verifies SPEC-0026-R03, SPEC-0026-R04, and SPEC-0026-R05: ten agents, each
// with a session of twenty calls, through two Gateway replicas that share
// nothing, to a Service of three stateful pods.
func TestSessionAffinityPinsSessionsToPods(t *testing.T) {
	pods := newStatefulPods(3)
	replicas := []*Handler{affinityHandler(pods, true), affinityHandler(pods, true)}

	for agent := range 10 {
		rr := send(replicas[agent%2], http.MethodPost, initializeBody, "")
		session := rr.Header().Get("Mcp-Session-Id")
		if rr.Code != http.StatusOK || session == "" {
			t.Fatalf("agent %d initialize: %d %q", agent, rr.Code, session)
		}
		for call := range 20 {
			if rr := send(replicas[call%2], http.MethodPost, callBody, session); rr.Code != http.StatusOK {
				t.Fatalf("agent %d call %d: %d %s", agent, call, rr.Code, rr.Body.String())
			}
		}
		if rr := send(replicas[1-agent%2], http.MethodGet, "", session); rr.Code != http.StatusOK {
			t.Fatalf("agent %d event stream: %d", agent, rr.Code)
		}
		if rr := send(replicas[agent%2], http.MethodDelete, "", session); rr.Code != http.StatusNoContent {
			t.Fatalf("agent %d delete: %d", agent, rr.Code)
		}
	}
	started := map[string]int{}
	for _, pod := range pods.sessions {
		started[pod]++
	}
	if len(started) != 3 {
		t.Fatalf("sessions per pod = %v, want new sessions spread across the three ready pods", started)
	}
}

// Verifies SPEC-0026-R06.
func TestSessionAffinityHidesPods(t *testing.T) {
	pods := newStatefulPods(1)
	h := affinityHandler(pods, true)
	rr := send(h, http.MethodPost, initializeBody, "")
	session := rr.Header().Get("Mcp-Session-Id")
	if strings.Contains(session, "10.0.0.1") || strings.Contains(session, "uid-0") || !strings.HasPrefix(session, pods.pods[0].Tag+".") {
		t.Fatalf("client session ID %q reveals the pod or lacks its tag", session)
	}
	if rr := send(h, http.MethodPost, callBody, session); rr.Code != http.StatusOK {
		t.Fatalf("call: %d", rr.Code)
	}
	last := pods.seen[len(pods.seen)-1]
	if got := last.Header.Get("Mcp-Session-Id"); got != "session-1" {
		t.Fatalf("the server received session ID %q, want the one it issued", got)
	}
	if last.Host != "demo.team-a.svc:8080" || last.URL.Host != "10.0.0.1:8080" {
		t.Fatalf("request went to %s with Host %q, want the pod with the Service host", last.URL.Host, last.Host)
	}
}

// Verifies SPEC-0026-R07.
func TestSessionAffinityRefusesUnknownSessions(t *testing.T) {
	pods := newStatefulPods(2)
	h := affinityHandler(pods, true)
	session := send(h, http.MethodPost, initializeBody, "").Header().Get("Mcp-Session-Id")
	tag, serverID, _ := mcpsession.Unwrap(session)
	other := pods.pods[0].Tag
	if other == tag {
		other = pods.pods[1].Tag
	}
	forwarded := len(pods.seen)

	for name, id := range map[string]string{
		"edited tag":      mcpsession.TagOf("uid-unknown") + "." + serverID,
		"without the tag": serverID,
		"malformed":       "not a session",
	} {
		if rr := send(h, http.MethodPost, callBody, id); rr.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rr.Code)
		}
	}
	if len(pods.seen) != forwarded {
		t.Fatalf("%d requests reached a pod, want none", len(pods.seen)-forwarded)
	}
	// Another ready pod's tag reaches that pod, which does not know the
	// session; no other pod is tried.
	if rr := send(h, http.MethodPost, callBody, other+"."+serverID); rr.Code != http.StatusNotFound || len(pods.seen) != forwarded+1 {
		t.Fatalf("another pod's tag: %d after %d requests", rr.Code, len(pods.seen)-forwarded)
	}

	// The session's pod goes away: the next request gets 404, and a new
	// session starts on the remaining pod.
	pods.mu.Lock()
	for i, pod := range pods.pods {
		if pod.Tag == tag {
			pods.pods = append(pods.pods[:i], pods.pods[i+1:]...)
			break
		}
	}
	pods.mu.Unlock()
	forwarded = len(pods.seen)
	if rr := send(h, http.MethodPost, callBody, session); rr.Code != http.StatusNotFound || len(pods.seen) != forwarded {
		t.Fatalf("session on a deleted pod: %d after %d requests, want 404 and none", rr.Code, len(pods.seen)-forwarded)
	}
	if rr := send(h, http.MethodPost, initializeBody, ""); rr.Code != http.StatusOK {
		t.Fatalf("new session: %d", rr.Code)
	}
	requireSeries(t, get(h, "/metrics").Body.String(), `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="session_not_found",route="team-a.demo",tool="echo"} 5`)
}

// Verifies SPEC-0026-R03: without ready pods, or without the EndpointSlice
// reader, a new session is refused instead of sent to the Service.
func TestSessionAffinityWithoutReadyPods(t *testing.T) {
	pods := newStatefulPods(0)
	if rr := send(affinityHandler(pods, true), http.MethodPost, initializeBody, ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no ready pods: %d, want 503", rr.Code)
	}
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.demo", Transport: "http", Endpoint: "http://demo.team-a.svc:8080/mcp", SessionAffinity: true}}, time.Now())
	if rr := send(NewHandler(table, &http.Client{Transport: pods}, Metadata{}), http.MethodPost, initializeBody, ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no EndpointSlice reader: %d, want 503", rr.Code)
	}
	if len(pods.seen) != 0 {
		t.Fatalf("%d requests were forwarded", len(pods.seen))
	}
}

// Verifies SPEC-0026-R10: with or without affinity, an upstream 404 to a
// request with a session ID is a lost session, not a client error.
func TestLostSessionsAreNamed(t *testing.T) {
	pods := newStatefulPods(3)
	h := affinityHandler(pods, false)
	// The Service is reached by name; this fake serves it from one address
	// per request, so most sessions land on a pod that did not start them.
	if rr := send(h, http.MethodPost, callBody, "session-from-elsewhere"); rr.Code != http.StatusNotFound {
		t.Fatalf("call: %d", rr.Code)
	}
	if rr := send(h, http.MethodGet, "", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("stream without session: %d", rr.Code)
	}
	metrics := get(h, "/metrics").Body.String()
	requireSeries(t, metrics, `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="session_not_found",route="team-a.demo",tool="echo"} 1`)
	requireSeries(t, metrics, `outcome="client_error"`)
}
