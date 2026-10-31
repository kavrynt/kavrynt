package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
)

// callOfSize is a tools/call whose body is exactly size bytes.
func callOfSize(size int) string {
	prefix := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"message":"`
	suffix := `"}}}`
	return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
}

// chunked sends body without a Content-Length, as a chunked request does.
func chunked(h *Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", io.NopCloser(strings.NewReader(body)))
	req.ContentLength = -1
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// readingHandler is demoHandler with an MCP server that reads the whole
// body, as a real one does, and reports how many bytes it received.
func readingHandler(t *testing.T) (*Handler, *int, *int64) {
	t.Helper()
	var received int64
	handler, calls := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		n, err := io.Copy(io.Discard, req.Body)
		received += n
		if err != nil {
			return nil, err
		}
		return okResponse(req)
	})
	return handler, calls, &received
}

// Verifies SPEC-0038-R01: a Content-Length over the limit gets 413 and the
// MCP server is never called.
func TestOversizedContentLengthIsRefusedBeforeTheServer(t *testing.T) {
	handler, calls, received := readingHandler(t)

	rr := post(handler, callOfSize(DefaultMaxRequestBytes+1), nil)
	if rr.Code != http.StatusRequestEntityTooLarge || *calls != 0 || *received != 0 {
		t.Fatalf("oversized POST: %d after %d upstream calls (%d bytes), want 413 and none", rr.Code, *calls, *received)
	}
	var answer struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatalf("not a JSON-RPC answer: %s", rr.Body.String())
	}
	if answer.JSONRPC != "2.0" || string(answer.ID) != "null" || answer.Error.Code != jsonRPCInvalidRequest ||
		!strings.Contains(answer.Error.Message, strconv.Itoa(DefaultMaxRequestBytes)) || !strings.Contains(answer.Error.Message, "gateway.config.maxRequestBytes") {
		t.Fatalf("oversized POST answer: %s", rr.Body.String())
	}
	if rr.Header().Get("Connection") != "close" {
		t.Errorf("oversized POST keeps the connection open")
	}

	req := httptest.NewRequest(http.MethodDelete, "/mcp/team-a.demo", strings.NewReader(strings.Repeat("a", DefaultMaxRequestBytes+1)))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rr.Body.String(), `"error":"request body is larger than`) || *calls != 0 {
		t.Fatalf("oversized DELETE: %d %s after %d upstream calls", rr.Code, rr.Body.String(), *calls)
	}

	if rr := post(handler, callOfSize(DefaultMaxRequestBytes), nil); rr.Code != http.StatusOK || *received != DefaultMaxRequestBytes {
		t.Fatalf("a body of exactly the limit: %d, server received %d bytes", rr.Code, *received)
	}
}

// Verifies SPEC-0038-R02: a body without Content-Length is cut at the limit,
// both while it is classified and while it is forwarded, and gets 413.
func TestOversizedChunkedBodyIsCutAndRefused(t *testing.T) {
	// Larger than the part read to classify the request, so the limit is
	// passed while the body is forwarded.
	handler, calls, received := readingHandler(t)
	limit := int64(maxInspectBytes + 64<<10)
	handler.SetMaxRequestBytes(limit)
	rr := chunked(handler, callOfSize(int(limit)+512<<10))
	if rr.Code != http.StatusRequestEntityTooLarge || *received > limit {
		t.Fatalf("chunked body over the limit while forwarded: %d, server received %d bytes, want 413 and at most %d", rr.Code, *received, limit)
	}
	if *calls != 1 {
		t.Fatalf("%d upstream calls, want the one cut request", *calls)
	}

	// Smaller than that part, so the limit is passed while classifying.
	handler, calls, received = readingHandler(t)
	handler.SetMaxRequestBytes(2048)
	if rr := chunked(handler, callOfSize(8192)); rr.Code != http.StatusRequestEntityTooLarge || *calls != 0 || *received != 0 {
		t.Fatalf("chunked body over the limit while classified: %d after %d upstream calls", rr.Code, *calls)
	}
	if rr := chunked(handler, callOfSize(2048)); rr.Code != http.StatusOK || *received != 2048 {
		t.Fatalf("chunked body of exactly the limit: %d, server received %d bytes", rr.Code, *received)
	}
}

// Verifies SPEC-0038-R02: a body that passed the limit before the MCP server
// answered gets 413 even when the server answers instead of failing, and a
// server that answers before reading the body (the transport then sends it
// after RoundTrip returns) is not a data race.
func TestOversizedBodyWithAnUpstreamAnswer(t *testing.T) {
	t.Run("answer after the limit", func(t *testing.T) {
		handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
			_, _ = io.Copy(io.Discard, req.Body)
			return okResponse(req)
		})
		handler.SetMaxRequestBytes(int64(maxInspectBytes + 64<<10))
		if rr := chunked(handler, callOfSize(maxInspectBytes+512<<10)); rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("server answered a body cut at the limit: %d, want 413", rr.Code)
		}
	})
	t.Run("answer before the body", func(t *testing.T) {
		read := make(chan struct{})
		handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
			go func() {
				defer close(read)
				_, _ = io.Copy(io.Discard, req.Body)
			}()
			return okResponse(req)
		})
		if rr := chunked(handler, callOfSize(4096)); rr.Code != http.StatusOK {
			t.Fatalf("server answering before reading the body: %d, want 200", rr.Code)
		}
		<-read
	})
}

// Verifies SPEC-0038-R03: 4 MiB by default, and SetMaxRequestBytes (the
// --max-request-bytes flag) changes it.
func TestRequestSizeLimitDefaultAndSetting(t *testing.T) {
	if DefaultMaxRequestBytes != 4194304 {
		t.Fatalf("DefaultMaxRequestBytes = %d, want 4 MiB", DefaultMaxRequestBytes)
	}
	handler, _, _ := readingHandler(t)
	if handler.maxRequestBytes != DefaultMaxRequestBytes {
		t.Fatalf("new handler limit = %d", handler.maxRequestBytes)
	}
	six := callOfSize(6 << 20)
	if rr := post(handler, six, nil); rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("6 MiB with the default limit: %d, want 413", rr.Code)
	}
	handler.SetMaxRequestBytes(10 << 20)
	if rr := post(handler, six, nil); rr.Code != http.StatusOK {
		t.Fatalf("6 MiB with a 10 MiB limit: %d, want it forwarded", rr.Code)
	}
	handler.SetMaxRequestBytes(0)
	if handler.maxRequestBytes != 10<<20 {
		t.Fatalf("SetMaxRequestBytes(0) changed the limit to %d", handler.maxRequestBytes)
	}
}

// Verifies SPEC-0038-R04: refusals are client errors with 413 in the
// metrics and invalid in the audit, with a reason that names the limit.
func TestOversizedRequestIsRecordedAsClientError(t *testing.T) {
	handler, _, _ := readingHandler(t)
	handler.SetMaxRequestBytes(2048)
	w, out := enableAudit(t, handler)

	post(handler, callOfSize(4096), nil)
	chunked(handler, callOfSize(4096))

	requireSeries(t, get(handler, "/metrics").Body.String(),
		`kavrynt_gateway_mcp_requests_total{method="unknown",outcome="client_error",route="team-a.demo",tool="none"} 1`)
	list := events(t, w, out)
	if len(list) != 2 {
		t.Fatalf("%d audit events, want 2", len(list))
	}
	for _, e := range list {
		if e.Decision != auditv1.DecisionInvalid || e.Status != http.StatusRequestEntityTooLarge || !strings.Contains(e.Reason, "larger than 2048 bytes") {
			t.Errorf("audit event: decision %q status %d reason %q", e.Decision, e.Status, e.Reason)
		}
	}
	for _, rec := range handler.recent.list() {
		if rec.Status != http.StatusRequestEntityTooLarge || rec.Outcome != outcomeClientError {
			t.Errorf("recent traffic: status %d outcome %q", rec.Status, rec.Outcome)
		}
	}
}
