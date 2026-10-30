package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	"github.com/kavrynt/kavrynt/internal/gateway/audit"
	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
)

// requestIDHeader returns each MCP request's ID, the request_id of its audit
// event (SPEC-0013-R05).
const requestIDHeader = "X-Request-Id"

// auditRecord collects what the proxy path decided about one request.
type auditRecord struct {
	identity       clientauth.Identity
	decision       string
	reason         string
	policyRevision string
	call           mcpCall
	outcome        string
	bytesIn        int64
	bytesOut       int64
	// filteredTools counts tools removed from a tools/list answer
	// (SPEC-0016-R05).
	filteredTools int
	// gatewayAudience is set when the Gateway audience is enabled.
	gatewayAudience bool
}

// audience is the audit event's audience, recorded only while the Gateway
// audience is enabled (SPEC-0031-R05).
func (rec *auditRecord) audience() string {
	if !rec.gatewayAudience {
		return ""
	}
	return rec.identity.Audience
}

type auditKey struct{}

func auditRecordFrom(r *http.Request) *auditRecord {
	rec, _ := r.Context().Value(auditKey{}).(*auditRecord)
	return rec
}

// serveMCP gives every MCP request an ID and, with audit on, one audit event
// (SPEC-0013-R01). The event is written when the request completes; a full
// queue in reject mode refuses the request first (R08).
func (h *Handler) serveMCP(w http.ResponseWriter, r *http.Request) {
	requestID := newRequestID()
	w.Header().Set(requestIDHeader, requestID)
	if h.audit == nil {
		h.proxy(w, r)
		return
	}
	reserved := h.audit.Reserve()
	if !reserved && h.audit.Mode() == audit.Reject {
		writeError(w, http.StatusServiceUnavailable, errors.New("audit queue full; try again"))
		return
	}

	start := time.Now()
	rec := &auditRecord{gatewayAudience: h.clientAuth != nil && h.clientAuth.GatewayAudienceEnabled()}
	status := &statusWriter{ResponseWriter: w}
	h.proxy(status, r.WithContext(context.WithValue(r.Context(), auditKey{}, rec)))

	name, _, _ := parseMCPPath(r.URL.Path)
	namespace, server, _ := strings.Cut(name, ".")
	event := auditv1.Event{
		Time:           start.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		RequestID:      requestID,
		Subject:        rec.identity.Subject,
		ClientID:       rec.identity.ClientID,
		Issuer:         rec.identity.Issuer,
		Audience:       rec.audience(),
		Route:          auditv1.Route{Namespace: bound(namespace), Name: bound(server)},
		HTTPMethod:     r.Method,
		MCPMethod:      rec.call.body.method,
		Tool:           rec.call.body.tool,
		Decision:       rec.decision,
		Reason:         rec.reason,
		PolicyRevision: rec.policyRevision,
		Outcome:        rec.outcome,
		Status:         status.status,
		DurationMS:     math.Round(float64(time.Since(start).Microseconds())) / 1000,
		BytesIn:        rec.bytesIn,
		BytesOut:       rec.bytesOut,
		FilteredTools:  rec.filteredTools,
	}
	if event.Decision == "" {
		// Refused before any decision: unknown route, bad path, loop.
		event.Decision = auditv1.DecisionInvalid
	}
	if event.Status == 0 {
		event.Status = http.StatusOK
	}
	h.audit.Emit(event, reserved)
}

// finish records a request in the metrics and recent traffic, and in its
// audit record.
func (h *Handler) finish(r *http.Request, route string, call mcpCall, outcome string, status int, elapsed time.Duration, bytesIn, bytesOut int64) {
	h.observe(route, call, outcome, status, elapsed, bytesIn, bytesOut)
	if rec := auditRecordFrom(r); rec != nil {
		rec.call, rec.outcome, rec.bytesIn, rec.bytesOut = call, outcome, bytesIn, bytesOut
	}
}

// refusalDecision maps an authentication refusal to an audit decision.
func refusalDecision(outcome string, status int) string {
	switch {
	case status == http.StatusBadRequest:
		return auditv1.DecisionInvalid
	case outcome == outcomeForbidden:
		return auditv1.DecisionForbidden
	case outcome == outcomeAuthUnavailable:
		return auditv1.DecisionUnavailable
	default:
		return auditv1.DecisionUnauthenticated
	}
}

func newRequestID() string {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return hex.EncodeToString(id)
}

// bound limits route names taken from the request path.
func bound(value string) string {
	if len(value) > 253 {
		return value[:253]
	}
	return value
}

// statusWriter remembers the response status for the audit event. Unwrap
// keeps flushing (event streams) working through http.ResponseController.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
