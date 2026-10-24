package clientauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testTokenEndpoint = testIssuer + "/token"
	testClientSecret  = "gateway-client-secret-value"
	callerToken       = "caller-subject-token-value"
	exchangedToken    = "exchanged-token-for-tickets"
	ticketsAudience   = "https://tickets.example.com"
)

// tokenEndpoint answers token exchange requests and records them.
type tokenEndpoint struct {
	mu       sync.Mutex
	next     http.RoundTripper
	requests []url.Values
	auth     []string
	status   int
	body     string
	err      error
	block    bool
}

func (e *tokenEndpoint) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost {
		return e.next.RoundTrip(req)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	if e.err != nil {
		return nil, e.err
	}
	data, _ := io.ReadAll(req.Body)
	form, _ := url.ParseQuery(string(data))
	e.requests = append(e.requests, form)
	e.auth = append(e.auth, req.Header.Get("Authorization"))
	status := e.status
	if status == 0 {
		status = http.StatusOK
	}
	body := e.body
	if body == "" {
		body = `{"access_token":"` + exchangedToken + `","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (e *tokenEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.requests)
}

type exchangeEnv struct {
	auth     *Authenticator
	as       *fakeAS
	endpoint *tokenEndpoint
	logs     *bytes.Buffer
	clock    *time.Time
}

// newExchangeEnv trusts testIssuer with token exchange through
// testTokenEndpoint, or the endpoint in its metadata when discover is set.
func newExchangeEnv(t *testing.T, discover bool) *exchangeEnv {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gateway-secret"), []byte(testClientSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	as := newFakeAS()
	as.set(testIssuer+"/jwks", jwks(newECKey(t, "key-1")))
	as.set("https://as.example.com/.well-known/oauth-authorization-server/tenant",
		map[string]string{"issuer": testIssuer, "jwks_uri": testIssuer + "/jwks", "token_endpoint": testTokenEndpoint})
	client := &TokenExchangeClient{ClientID: "kavrynt gateway", ClientSecretKey: "gateway-secret"}
	if !discover {
		client.TokenEndpoint = testTokenEndpoint
	}
	endpoint := &tokenEndpoint{next: as}
	logs := &bytes.Buffer{}
	auth, err := New(Config{
		ResourceURL:            testResource,
		Issuers:                []Issuer{{Issuer: testIssuer, JWKSURI: testIssuer + "/jwks", TokenExchange: client}, {Issuer: otherIssuer}},
		TokenExchangeSecretDir: dir,
	}, endpoint, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	env := &exchangeEnv{auth: auth, as: as, endpoint: endpoint, logs: logs, clock: &clock}
	auth.now = func() time.Time { return *env.clock }
	return env
}

func (e *exchangeEnv) exchange(token string, scopes ...string) (string, error) {
	return e.auth.Exchange(request(token), Identity{Subject: "user-42", Issuer: testIssuer}, ticketsAudience, scopes)
}

// Verifies SPEC-0015-R02: exchange settings need the mounted Secret, a key
// name, and https.
func TestTokenExchangeConfiguration(t *testing.T) {
	base := func(client *TokenExchangeClient, dir string, allowHTTP bool) Config {
		return Config{ResourceURL: testResource, TokenExchangeSecretDir: dir,
			Issuers: []Issuer{{Issuer: "https://as.example.com", AllowHTTP: allowHTTP, TokenExchange: client}}}
	}
	for name, cfg := range map[string]Config{
		"no Secret":           base(&TokenExchangeClient{ClientID: "gw", ClientSecretKey: "k"}, "", false),
		"no client ID":        base(&TokenExchangeClient{ClientSecretKey: "k"}, "/s", false),
		"path in key":         base(&TokenExchangeClient{ClientID: "gw", ClientSecretKey: "../k"}, "/s", false),
		"hidden key":          base(&TokenExchangeClient{ClientID: "gw", ClientSecretKey: "..data"}, "/s", false),
		"http token endpoint": base(&TokenExchangeClient{ClientID: "gw", ClientSecretKey: "k", TokenEndpoint: "http://as/token"}, "/s", false),
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := base(&TokenExchangeClient{ClientID: "gw", ClientSecretKey: "k", TokenEndpoint: "http://as/token"}, "/s", true).Validate(); err != nil {
		t.Errorf("http token endpoint with allowHTTP: %v", err)
	}
}

// Verifies SPEC-0015-R03: an RFC 8693 request with client_secret_basic, and
// only the returned token comes back.
func TestTokenExchangeRequest(t *testing.T) {
	for _, discover := range []bool{false, true} {
		env := newExchangeEnv(t, discover)
		token, err := env.exchange(callerToken, "tickets:write", "tickets:read")
		if err != nil || token != exchangedToken {
			t.Fatalf("discover=%v: token %q, %v", discover, token, err)
		}
		form := env.endpoint.requests[0]
		want := map[string]string{
			"grant_type":           "urn:ietf:params:oauth:grant-type:token-exchange",
			"subject_token":        callerToken,
			"subject_token_type":   "urn:ietf:params:oauth:token-type:access_token",
			"requested_token_type": "urn:ietf:params:oauth:token-type:access_token",
			"audience":             ticketsAudience,
			"scope":                "tickets:read tickets:write",
		}
		for field, value := range want {
			if form.Get(field) != value {
				t.Errorf("discover=%v: %s = %q, want %q", discover, field, form.Get(field), value)
			}
		}
		req := &http.Request{Header: http.Header{"Authorization": {env.endpoint.auth[0]}}}
		id, secret, ok := req.BasicAuth()
		if !ok || id != url.QueryEscape("kavrynt gateway") || secret != testClientSecret {
			t.Errorf("discover=%v: client authentication %q", discover, env.endpoint.auth[0])
		}
	}
}

// Verifies SPEC-0015-R04.
func TestTokenExchangeCache(t *testing.T) {
	env := newExchangeEnv(t, false)
	for range 2 {
		if _, err := env.exchange(callerToken); err != nil {
			t.Fatal(err)
		}
	}
	if env.endpoint.count() != 1 {
		t.Fatalf("%d exchanges for two calls with one token, want 1", env.endpoint.count())
	}
	if _, err := env.exchange("another-caller-token"); err != nil || env.endpoint.count() != 2 {
		t.Fatalf("another caller: %v after %d exchanges", err, env.endpoint.count())
	}
	if _, err := env.exchange(callerToken, "tickets:write"); err != nil || env.endpoint.count() != 3 {
		t.Fatalf("other scopes: %v after %d exchanges", err, env.endpoint.count())
	}
	*env.clock = env.clock.Add(271 * time.Second) // within 30 s of expires_in 300
	if _, err := env.exchange(callerToken); err != nil || env.endpoint.count() != 4 {
		t.Fatalf("near expiry: %v after %d exchanges, want a new exchange", err, env.endpoint.count())
	}
	for _, digest := range env.auth.exchanged.entries {
		if digest.token != exchangedToken {
			t.Fatal("unexpected cache entry")
		}
	}
	for key := range env.auth.exchanged.entries {
		if strings.Contains(key, callerToken) {
			t.Fatal("the cache key holds the caller's token")
		}
	}

	env.endpoint.body = `{"access_token":"short-lived","token_type":"Bearer"}`
	env.auth.exchanged = newExchangeCache(exchangeCacheSize)
	env.exchange(callerToken)
	env.exchange(callerToken)
	if env.endpoint.count() != 6 {
		t.Fatalf("a token without expires_in was cached")
	}

	cache := newExchangeCache(2)
	now := time.Now()
	cache.put("a", "1", now.Add(time.Minute), now)
	cache.put("b", "2", now.Add(time.Minute), now.Add(time.Second))
	cache.put("c", "3", now.Add(time.Minute), now.Add(2*time.Second))
	if _, ok := cache.get("a", now); ok || cache.len() != 2 {
		t.Fatalf("a full cache kept its oldest entry: %d entries", cache.len())
	}
}

// Verifies SPEC-0015-R05 and SPEC-0015-R06: failures are classified, bounded,
// and never follow redirects.
func TestTokenExchangeFailures(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(*tokenEndpoint)
		want  error
	}{
		"rejected":      {func(e *tokenEndpoint) { e.status = http.StatusBadRequest }, ErrExchangeFailed},
		"redirect":      {func(e *tokenEndpoint) { e.status = http.StatusFound }, ErrExchangeFailed},
		"not bearer":    {func(e *tokenEndpoint) { e.body = `{"access_token":"x","token_type":"N_A"}` }, ErrExchangeFailed},
		"no token":      {func(e *tokenEndpoint) { e.body = `{"token_type":"Bearer"}` }, ErrExchangeFailed},
		"header inject": {func(e *tokenEndpoint) { e.body = `{"access_token":"a\r\nX-Evil: 1","token_type":"Bearer"}` }, ErrExchangeFailed},
		"too large":     {func(e *tokenEndpoint) { e.body = `{"access_token":"` + strings.Repeat("x", maxDocumentBytes) + `"}` }, ErrExchangeFailed},
		"unreachable":   {func(e *tokenEndpoint) { e.err = errors.New("dial tcp: connection refused") }, ErrExchangeUnreachable},
	} {
		env := newExchangeEnv(t, false)
		c.setup(env.endpoint)
		if _, err := env.exchange(callerToken); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}

	env := newExchangeEnv(t, false)
	env.endpoint.block = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := request(callerToken).WithContext(ctx)
	if _, err := env.auth.Exchange(r, Identity{Issuer: testIssuer}, ticketsAudience, nil); !errors.Is(err, ErrExchangeUnreachable) {
		t.Errorf("slow endpoint: err = %v", err)
	}

	if _, err := env.auth.Exchange(request(callerToken), Identity{Issuer: otherIssuer}, ticketsAudience, nil); !errors.Is(err, ErrExchangeFailed) {
		t.Errorf("issuer without exchange settings: err = %v", err)
	}
}

// Verifies SPEC-0015-R05: a token endpoint over http from the metadata is
// refused without allowHTTP.
func TestTokenExchangeNeedsHTTPS(t *testing.T) {
	env := newExchangeEnv(t, true)
	env.as.set("https://as.example.com/.well-known/oauth-authorization-server/tenant",
		map[string]string{"issuer": testIssuer, "jwks_uri": testIssuer + "/jwks", "token_endpoint": "http://as.example.com/token"})
	if _, err := env.exchange(callerToken); err == nil || env.endpoint.count() != 0 {
		t.Fatalf("http token endpoint: err = %v after %d requests", err, env.endpoint.count())
	}
}

// Verifies SPEC-0015-R08.
func TestTokenExchangeNeverLogsSecrets(t *testing.T) {
	env := newExchangeEnv(t, false)
	env.exchange(callerToken)
	env.endpoint.status = http.StatusUnauthorized
	env.auth.exchanged = newExchangeCache(exchangeCacheSize)
	_, err := env.exchange(callerToken)
	for _, secret := range []string{callerToken, exchangedToken, testClientSecret} {
		if strings.Contains(env.logs.String(), secret) || strings.Contains(err.Error(), secret) {
			t.Fatalf("%q appears in the logs or error: %s %v", secret, env.logs.String(), err)
		}
	}
	if !strings.Contains(env.logs.String(), "token exchange failed") {
		t.Fatalf("the failure was not logged: %s", env.logs.String())
	}
}
