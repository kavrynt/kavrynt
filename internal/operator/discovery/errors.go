package discovery

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/kavrynt/kavrynt/internal/egress"
)

// MaxMessageLength bounds a discovery error message in characters
// (SPEC-0027-R11).
const MaxMessageLength = 512

// cause is what made a discovery fail.
type cause int

const (
	causeOther cause = iota
	causeEgress
	causeTimeout
	causeDNS
	causeConnect
	causeCertificate
	causeStatus
	causeNotMCP
)

// Error is a failed discovery. Its message names the cause and the next step
// from what the Operator knows: the host, port, and path of spec.endpoint, an
// HTTP status, the kind of address the egress policy refused, and the time
// waited (SPEC-0027-R04 to R10). It never formats the wrapped error, whose
// text holds resolved addresses and the full URL, and never quotes the server
// (SPEC-0027-R11).
type Error struct {
	method     string
	cause      cause
	target     target
	status     int
	egressKind string
	waited     time.Duration
	// detail is the message of causeOther and the reason of causeNotMCP,
	// written by the Operator.
	detail string
	err    error
}

func (e *Error) Error() string {
	var message string
	switch e.cause {
	case causeEgress:
		message = fmt.Sprintf("%s: the egress policy refuses %s addresses for MCP endpoints, and the endpoint resolves to one", e.method, e.egressKind)
	case causeTimeout:
		message = fmt.Sprintf("%s: no answer from %s within %s", e.method, e.target.hostPort(), e.waited)
	case causeDNS:
		message = fmt.Sprintf("%s: host %s does not resolve; check the Service name and namespace in spec.endpoint", e.method, e.target.host)
	case causeConnect:
		message = fmt.Sprintf("%s: cannot connect to %s; check that the Service has ready pods listening on port %s", e.method, e.target.hostPort(), e.target.port)
	case causeCertificate:
		message = fmt.Sprintf("%s: the TLS certificate of %s could not be verified", e.method, e.target.hostPort())
	case causeStatus:
		message = e.statusMessage()
	case causeNotMCP:
		message = fmt.Sprintf("%s: %s; the endpoint does not look like an MCP Streamable HTTP endpoint", e.method, e.detail)
	default:
		message = e.detail
	}
	return truncate(message, MaxMessageLength)
}

func (e *Error) statusMessage() string {
	switch {
	case e.status == http.StatusNotFound && e.method == "initialize" && e.target.path == "/mcp":
		return fmt.Sprintf("initialize returned HTTP 404 for path %q; check the path in spec.endpoint", e.target.path)
	case e.status == http.StatusNotFound && e.method == "initialize":
		return fmt.Sprintf("initialize returned HTTP 404 for path %q; check the path in spec.endpoint, many MCP frameworks serve at /mcp", e.target.path)
	case e.status == http.StatusUnauthorized, e.status == http.StatusForbidden:
		return fmt.Sprintf("%s returned HTTP %d; the server needs a credential: set spec.credential for a stored key, or spec.tokenExchange for the caller's own token", e.method, e.status)
	}
	return fmt.Sprintf("%s returned HTTP %d", e.method, e.status)
}

// Unwrap returns the transport error, for errors.Is and errors.As only.
func (e *Error) Unwrap() error { return e.err }

// notMCP is an answer that is not an MCP Streamable HTTP response; its text
// says why without quoting the answer (SPEC-0027-R10).
type notMCP string

func (n notMCP) Error() string { return string(n) }

const (
	errContentType   notMCP = "the answer is neither application/json nor text/event-stream"
	errNotJSONRPC    notMCP = "the answer is not the JSON-RPC response to the request"
	errNoStreamReply notMCP = "the event stream ended without the JSON-RPC response to the request"
)

// target is the part of spec.endpoint a message may name: never its query or
// user info, and never an address it resolves to.
type target struct {
	host, port, path string
}

func parseTarget(endpoint string) target {
	u, err := url.Parse(endpoint)
	if err != nil {
		return target{}
	}
	t := target{host: u.Hostname(), port: u.Port(), path: u.EscapedPath()}
	if t.port == "" {
		t.port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	if t.path == "" {
		t.path = "/"
	}
	return t
}

func (t target) hostPort() string { return net.JoinHostPort(t.host, t.port) }

// transportError classifies a request that got no answer. Egress comes
// first, because a refusal and a DNS failure also arrive as dial errors.
func (s *session) transportError(method string, err error) *Error {
	e := &Error{method: method, target: s.target, err: err}
	var refused *egress.RefusedError
	var netErr net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.As(err, &refused):
		e.cause, e.egressKind = causeEgress, refused.Kind
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		e.cause, e.waited = causeTimeout, s.waited
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		e.cause = causeDNS
	case errors.As(err, &opErr) && opErr.Op == "dial":
		e.cause = causeConnect
	case errors.As(err, new(*tls.CertificateVerificationError)):
		e.cause = causeCertificate
	default:
		e.detail = fmt.Sprintf("%s: the request to %s failed", method, s.target.hostPort())
	}
	return e
}

// answerError classifies a response body that could not be read as an MCP
// answer.
func (s *session) answerError(method string, err error) *Error {
	var reason notMCP
	switch {
	case errors.As(err, &reason):
		return &Error{method: method, cause: causeNotMCP, target: s.target, detail: string(reason)}
	case errors.Is(err, errResponseTooLarge):
		return s.otherError(method, fmt.Sprintf("%s: %v", method, errResponseTooLarge))
	}
	return s.transportError(method, err)
}

func (s *session) otherError(method, detail string) *Error {
	return &Error{method: method, target: s.target, detail: detail}
}
