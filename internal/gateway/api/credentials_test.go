package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	"github.com/kavrynt/kavrynt/internal/upstreamcred"
)

const storedCredential = "stored-upstream-secret"

// credentialHandler routes team-a.search, which declares a credential, to
// a fake upstream recording what it receives. files are the Secret's keys.
func credentialHandler(t *testing.T, credential *kavryntv1alpha1.MCPServerCredential, files map[string]string) (*Handler, *[]*http.Request) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	table := routing.NewTable()
	endpoint := "http://search.team-a.svc:8080/mcp"
	table.Replace([]model.Route{{Name: "team-a.search", Version: "1", Transport: "http", Endpoint: endpoint, Credential: credential}}, time.Now())
	table.ReplaceServers([]model.Server{{Namespace: "team-a", Name: "search", Route: "team-a.search", Endpoint: endpoint, Routed: true,
		Credential: &model.ServerCredential{Required: true}}})
	var received []*http.Request
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req)
		return okResponse(req)
	})}
	handler := NewHandler(table, client, Metadata{})
	handler.EnableUpstreamCredentials(upstreamcred.Store{Dir: dir})
	return handler, &received
}

func pinned() map[string]string {
	return map[string]string{"team-a.search": storedCredential + "\n", "team-a.search.origin": "http://search.team-a.svc:8080\n"}
}

// Verifies SPEC-0011-R05.
func TestStoredCredentialIsSentInsteadOfTheCallers(t *testing.T) {
	handler, received := credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{Scheme: "Bearer"}, pinned())
	rr := callRoute(handler, "/mcp/team-a.search", "caller-token")
	if rr.Code != http.StatusOK || len(*received) != 1 {
		t.Fatalf("status %d after %d upstream calls", rr.Code, len(*received))
	}
	if got := (*received)[0].Header.Values("Authorization"); len(got) != 1 || got[0] != "Bearer "+storedCredential {
		t.Fatalf("upstream Authorization = %q, want only the stored credential", got)
	}

	handler, received = credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{Header: "X-API-Key"}, pinned())
	callRoute(handler, "/mcp/team-a.search", "", func(r *http.Request) { r.Header.Set("X-API-Key", "caller-chosen") })
	if got := (*received)[0].Header.Values("X-API-Key"); len(got) != 1 || got[0] != storedCredential {
		t.Fatalf("upstream X-API-Key = %q, want only the stored credential", got)
	}
}

// Verifies SPEC-0011-R06.
func TestMissingCredentialIsNotForwarded(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no key":       {"team-a.search.origin": "http://search.team-a.svc:8080"},
		"no pin":       {"team-a.search": storedCredential},
		"pin mismatch": {"team-a.search": storedCredential, "team-a.search.origin": "http://other.example:8080"},
	} {
		handler, received := credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{Scheme: "Bearer"}, files)
		rr := callRoute(handler, "/mcp/team-a.search", "")
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "upstream credential unavailable") || len(*received) != 0 {
			t.Fatalf("%s: %d %s after %d upstream calls, want 503 and no call", name, rr.Code, rr.Body.String(), len(*received))
		}
		requireSeries(t, get(handler, "/metrics").Body.String(), `outcome="credential_unavailable",route="team-a.search"`)
	}
	// Without a mounted Secret at all.
	handler, received := credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{}, nil)
	handler.EnableUpstreamCredentials(upstreamcred.Store{})
	if rr := callRoute(handler, "/mcp/team-a.search", ""); rr.Code != http.StatusServiceUnavailable || len(*received) != 0 {
		t.Fatalf("no Secret: %d after %d upstream calls", rr.Code, len(*received))
	}
}

// Verifies SPEC-0011-R07 and SPEC-0011-R09.
func TestCredentialIsReportedButNeverShown(t *testing.T) {
	handler, _ := credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{Scheme: "Bearer"}, pinned())
	callRoute(handler, "/mcp/team-a.search", "")
	servers := get(handler, "/v1/servers").Body.String()
	if !strings.Contains(servers, `"credential":{"required":true,"available":true}`) {
		t.Fatalf("/v1/servers = %s, want the credential required and available", servers)
	}
	for _, path := range []string{"/v1/servers", "/v1/routes", "/v1/traffic", "/metrics"} {
		body := get(handler, path).Body.String()
		if strings.Contains(body, storedCredential) || strings.Contains(body, ".origin") {
			t.Fatalf("%s shows the credential or its pin: %s", path, body)
		}
	}

	missing, _ := credentialHandler(t, &kavryntv1alpha1.MCPServerCredential{}, nil)
	if body := get(missing, "/v1/servers").Body.String(); !strings.Contains(body, `"credential":{"required":true,"available":false}`) {
		t.Fatalf("/v1/servers = %s, want the credential not available", body)
	}
}
