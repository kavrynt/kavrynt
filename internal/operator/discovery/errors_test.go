package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kavrynt/kavrynt/internal/egress"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// dialFailure is the error the HTTP client returns when dialing fails: its
// text holds the full URL and the resolved address.
func dialFailure(r *http.Request, err error) error {
	return &url.Error{Op: "Post", URL: r.URL.String(), Err: &net.OpError{
		Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.7"), Port: 8080}, Err: err,
	}}
}

func discoveryError(t *testing.T, endpoint string, transport http.RoundTripper) string {
	t.Helper()
	_, err := New("test", transport).ListTools(context.Background(), endpoint, netip.AddrPort{}, nil)
	var discoveryErr *Error
	if !errors.As(err, &discoveryErr) {
		t.Fatalf("ListTools error = %v (%T), want a *discovery.Error", err, err)
	}
	return err.Error()
}

func answering(status int) *fakeServer {
	server := newFakeServer(tool("echo"))
	server.handle = func(_ string, w http.ResponseWriter) bool {
		http.Error(w, "upstream-secret", status)
		return true
	}
	return server
}

// Verifies SPEC-0027-R04: a 404 to initialize names the path and suggests
// /mcp, unless the path is /mcp already.
func TestNotFoundNamesThePath(t *testing.T) {
	got := discoveryError(t, "http://demo.default.svc:8080/", answering(http.StatusNotFound))
	if want := `initialize returned HTTP 404 for path "/"; check the path in spec.endpoint, many MCP frameworks serve at /mcp`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = discoveryError(t, "http://demo.default.svc:8080", answering(http.StatusNotFound))
	if !strings.Contains(got, `path "/"`) {
		t.Fatalf("an endpoint without a path: %q does not name /", got)
	}
	got = discoveryError(t, "http://demo.default.svc:8080/mcp", answering(http.StatusNotFound))
	if want := `initialize returned HTTP 404 for path "/mcp"; check the path in spec.endpoint`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// After initialize, a 404 is not about the path.
	server := newFakeServer(tool("echo"))
	server.handle = func(method string, w http.ResponseWriter) bool {
		if method != "tools/list" {
			return false
		}
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	if got := discoveryError(t, endpoint, server); got != "tools/list returned HTTP 404" {
		t.Fatalf("tools/list 404: %q", got)
	}
}

// Verifies SPEC-0027-R05: a connection that cannot be made names the host and
// port and says to check for ready pods.
func TestConnectionFailureNamesHostAndPort(t *testing.T) {
	for _, cause := range []error{syscall.ECONNREFUSED, syscall.EHOSTUNREACH} {
		got := discoveryError(t, "http://demo.default.svc:8080/mcp", roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, dialFailure(r, cause)
		}))
		if want := "initialize: cannot connect to demo.default.svc:8080; check that the Service has ready pods listening on port 8080"; got != want {
			t.Fatalf("%v: got %q, want %q", cause, got, want)
		}
	}
	// Without a port, the scheme's port is named.
	got := discoveryError(t, "https://demo.default.svc/mcp", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, dialFailure(r, syscall.ECONNREFUSED)
	}))
	if !strings.Contains(got, "demo.default.svc:443") {
		t.Fatalf("got %q, want the https port", got)
	}
}

// Verifies SPEC-0027-R06: a host that does not resolve is named, with the
// Service name and namespace to check.
func TestUnresolvedHostIsNamed(t *testing.T) {
	got := discoveryError(t, "http://demo.other.svc.cluster.local:8080/mcp", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, dialFailure(r, &net.DNSError{Err: "no such host", Name: "demo.other.svc.cluster.local", IsNotFound: true})
	}))
	if want := "initialize: host demo.other.svc.cluster.local does not resolve; check the Service name and namespace in spec.endpoint"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Verifies SPEC-0027-R07: an egress refusal names the kind of address and
// not the address, through the transport the Operator uses.
func TestEgressRefusalNamesTheKind(t *testing.T) {
	for endpoint, kind := range map[string]string{
		"http://169.254.169.254/latest/meta-data/": "cloud metadata",
		"http://127.0.0.1:1/mcp":                   "loopback",
		"http://[fe80::1]:8080/mcp":                "link-local",
	} {
		got := discoveryError(t, endpoint, egress.Transport(false))
		if want := fmt.Sprintf("initialize: the egress policy refuses %s addresses for MCP endpoints, and the endpoint resolves to one", kind); got != want {
			t.Errorf("%s: got %q, want %q", endpoint, got, want)
		}
	}
}

// Verifies SPEC-0027-R08: a 401 or 403 says the server needs a credential and
// names both ways to give it one.
func TestUnauthorizedNamesCredentialFields(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		got := discoveryError(t, endpoint, answering(status))
		if !strings.HasPrefix(got, fmt.Sprintf("initialize returned HTTP %d;", status)) ||
			!strings.Contains(got, "spec.credential") || !strings.Contains(got, "spec.tokenExchange") {
			t.Fatalf("HTTP %d: %q", status, got)
		}
	}
}

// Verifies SPEC-0027-R09: a timeout names the time the session waited.
func TestTimeoutNamesTheWait(t *testing.T) {
	blocked := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, &url.Error{Op: "Post", URL: r.URL.String(), Err: r.Context().Err()}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := New("test", blocked).ListTools(ctx, endpoint, netip.AddrPort{}, nil)
	if err == nil || err.Error() != "initialize: no answer from demo.default.svc:8080 within 300ms" {
		t.Fatalf("err = %v", err)
	}

	// A dial timeout counts too, and the default session waits Timeout.
	got := discoveryError(t, endpoint, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, dialFailure(r, &timeoutError{})
	}))
	if want := "initialize: no answer from demo.default.svc:8080 within " + Timeout.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// Verifies SPEC-0027-R10: answers that are not MCP Streamable HTTP say so.
func TestNonMCPAnswersSaySo(t *testing.T) {
	for name, handle := range map[string]func(string, http.ResponseWriter) bool{
		"HTML page": func(_ string, w http.ResponseWriter) bool {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>upstream-secret</html>")
			return true
		},
		"JSON that is not JSON-RPC": func(_ string, w http.ResponseWriter) bool {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"upstream-secret"}`)
			return true
		},
		"event stream without the response": func(_ string, w http.ResponseWriter) bool {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: upstream-secret\n\n")
			return true
		},
	} {
		server := newFakeServer(tool("echo"))
		server.handle = handle
		got := discoveryError(t, endpoint, server)
		if !strings.HasPrefix(got, "initialize: ") || !strings.HasSuffix(got, "; the endpoint does not look like an MCP Streamable HTTP endpoint") {
			t.Errorf("%s: %q", name, got)
		}
		if strings.Contains(got, "upstream-secret") {
			t.Errorf("%s: message quotes the server: %q", name, got)
		}
	}
}

// Verifies SPEC-0027-R11: no message carries a response body, a header value,
// a credential, a query string, or a resolved address, and none is longer
// than 512 characters.
func TestMessagesCarryNoSecretsAndAreBounded(t *testing.T) {
	const withQuery = "http://demo.default.svc:8080/mcp?token=query-secret"
	header := http.Header{"Authorization": {"Bearer header-secret"}}
	transports := map[string]http.RoundTripper{
		"refused": roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, dialFailure(r, syscall.ECONNREFUSED)
		}),
		"unknown transport failure": roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, dialFailure(r, errors.New("10.0.0.7 said upstream-secret"))
		}),
		"certificate": roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Post", URL: r.URL.String(), Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}
		}),
		"status": answering(http.StatusInternalServerError),
		"body":   answering(http.StatusForbidden),
	}
	for name, transport := range transports {
		_, err := New("test", transport).ListTools(context.Background(), withQuery, netip.AddrPort{}, header)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		for _, secret := range []string{"10.0.0.7", "query-secret", "token=", "header-secret", "upstream-secret"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: %q contains %q", name, err, secret)
			}
		}
	}

	// A session-affinity pod address stays out too: the message names the
	// endpoint's host.
	_, err := New("test", transports["refused"]).ListTools(context.Background(), endpoint, netip.MustParseAddrPort("10.0.0.7:8080"), nil)
	if err == nil || strings.Contains(err.Error(), "10.0.0.7") || !strings.Contains(err.Error(), "demo.default.svc:8080") {
		t.Fatalf("pod session: %v", err)
	}

	long := "http://" + strings.Repeat("a", 60) + ".default.svc:8080/" + strings.Repeat("p", 2000)
	got := discoveryError(t, long, answering(http.StatusNotFound))
	if n := utf8.RuneCountInString(got); n > MaxMessageLength {
		t.Fatalf("message has %d characters, want at most %d", n, MaxMessageLength)
	}
}
