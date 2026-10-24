package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

const (
	exchangeSecret = "gateway-client-secret"
	exchangedValue = "exchanged-for-tickets"
)

// exchangeHandler routes team-a.tickets, which needs token exchange, with
// client authentication on; tokenAnswer answers the token endpoint.
func exchangeHandler(t *testing.T, logs io.Writer, tokenAnswer func() (*http.Response, error)) (*Handler, *[]*http.Request, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "ES256", Use: "sig"}}})
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case authIssuer + "/jwks":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(set))}, nil
		case authIssuer + "/token":
			return tokenAnswer()
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: http.Header{}}, nil
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client-secret"), []byte(exchangeSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := clientauth.New(clientauth.Config{
		ResourceURL: authResource,
		Issuers: []clientauth.Issuer{{Issuer: authIssuer, JWKSURI: authIssuer + "/jwks",
			TokenExchange: &clientauth.TokenExchangeClient{ClientID: "kavrynt", ClientSecretKey: "client-secret", TokenEndpoint: authIssuer + "/token"}}},
		TokenExchangeSecretDir: dir,
	}, transport, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	var received []*http.Request
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.tickets", Version: "1", Transport: "http", Endpoint: "http://tickets.test/mcp",
		TokenExchange: &kavryntv1alpha1.MCPServerTokenExchange{Audience: "https://tickets.example.com"}}}, time.Now())
	handler := NewHandler(table, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req)
		return okResponse(req)
	})}, Metadata{})
	handler.EnableClientAuth(auth)

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(map[string]any{
		"iss": authIssuer, "sub": "user-42", "client_id": "support-agent",
		"aud": authResource + "/mcp/team-a.tickets", "exp": time.Now().Add(time.Minute).Unix(),
	}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return handler, &received, token
}

func tokenAnswer(status int, body string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

// Verifies SPEC-0015-R03: the server receives only the exchanged token.
func TestServerReceivesOnlyTheExchangedToken(t *testing.T) {
	handler, received, caller := exchangeHandler(t, io.Discard,
		tokenAnswer(http.StatusOK, `{"access_token":"`+exchangedValue+`","token_type":"Bearer","expires_in":300}`))
	rr := callRoute(handler, "/mcp/team-a.tickets", caller)
	if rr.Code != http.StatusOK || len(*received) != 1 {
		t.Fatalf("call: %d %s after %d upstream requests", rr.Code, rr.Body.String(), len(*received))
	}
	if got := (*received)[0].Header.Values("Authorization"); len(got) != 1 || got[0] != "Bearer "+exchangedValue {
		t.Fatalf("the server received Authorization %q, want only the exchanged token", got)
	}
}

// Verifies SPEC-0015-R06 and SPEC-0015-R08: failed exchanges are answered
// without reaching the server, recorded, and leak no token or secret.
func TestFailedExchangesNeverReachTheServer(t *testing.T) {
	for name, c := range map[string]struct {
		answer func() (*http.Response, error)
		status int
	}{
		"rejected":    {tokenAnswer(http.StatusBadRequest, `{"error":"invalid_target","error_description":"no `+exchangeSecret+`"}`), http.StatusBadGateway},
		"unreachable": {func() (*http.Response, error) { return nil, errors.New("dial tcp: connection refused") }, http.StatusServiceUnavailable},
	} {
		var logs bytes.Buffer
		handler, received, caller := exchangeHandler(t, &logs, c.answer)
		w, out := enableAudit(t, handler)
		rr := callRoute(handler, "/mcp/team-a.tickets", caller)
		if rr.Code != c.status || len(*received) != 0 {
			t.Fatalf("%s: %d after %d upstream requests, want %d and none", name, rr.Code, len(*received), c.status)
		}
		metrics := get(handler, "/metrics").Body.String()
		requireSeries(t, metrics, `outcome="exchange_failed",route="team-a.tickets"`)
		list := events(t, w, out)
		if len(list) != 1 || list[0].Outcome != outcomeExchangeFailed || list[0].Decision != "unavailable" {
			t.Fatalf("%s: audit events = %+v", name, list)
		}
		traffic := get(handler, "/v1/traffic").Body.String()
		for _, secret := range []string{caller, exchangeSecret, exchangedValue} {
			for where, text := range map[string]string{"response": rr.Body.String(), "metrics": metrics, "audit": out.String(), "logs": logs.String(), "traffic": traffic} {
				if strings.Contains(text, secret) {
					t.Errorf("%s: a token or secret appears in the %s", name, where)
				}
			}
		}
	}
}

// Verifies SPEC-0015-R07.
func TestTokenExchangeNeedsClientAuthentication(t *testing.T) {
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.tickets", Version: "1", Transport: "http", Endpoint: "http://tickets.test/mcp",
		TokenExchange: &kavryntv1alpha1.MCPServerTokenExchange{Audience: "https://tickets.example.com"}}}, time.Now())
	calls := 0
	handler := NewHandler(table, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return okResponse(req)
	})}, Metadata{})
	for range 2 {
		if rr := callRoute(handler, "/mcp/team-a.tickets", ""); rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "needs MCP client authentication") {
			t.Fatalf("without client authentication: %d %s", rr.Code, rr.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("%d calls reached the server", calls)
	}
	logged := 0
	handler.exchangeWithoutAuth.Range(func(any, any) bool { logged++; return true })
	if logged != 1 {
		t.Fatalf("the misconfiguration was recorded for %d routes, want 1", logged)
	}
}
