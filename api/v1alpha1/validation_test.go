package v1alpha1

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		server  MCPServer
		wantErr string
	}{
		{
			name:   "valid http server",
			server: server("payments", MCPServerSpec{Version: "1.0.0", Transport: "http", Endpoint: "http://payments.svc:8080/mcp"}),
		},
		{
			name:   "valid stdio server",
			server: server("local", MCPServerSpec{Version: "1.0.0", Transport: "stdio", Command: "/bin/server"}),
		},
		{
			name:    "missing name",
			server:  server("", MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://a"}),
			wantErr: "metadata.name is required",
		},
		{
			name:    "invalid name",
			server:  server("Bad_Name", MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://a"}),
			wantErr: "DNS-subdomain",
		},
		{
			name:    "missing version",
			server:  server("a", MCPServerSpec{Transport: "http", Endpoint: "http://a"}),
			wantErr: "spec.version is required",
		},
		{
			name:    "unknown transport",
			server:  server("a", MCPServerSpec{Version: "1", Transport: "grpc"}),
			wantErr: "spec.transport must be one of",
		},
		{
			name:    "http without endpoint",
			server:  server("a", MCPServerSpec{Version: "1", Transport: "http"}),
			wantErr: "spec.endpoint is required",
		},
		{
			name:    "non-http endpoint",
			server:  server("a", MCPServerSpec{Version: "1", Transport: "http", Endpoint: "file:///etc/passwd"}),
			wantErr: "absolute http or https URL",
		},
		{
			name:    "endpoint with credentials",
			server:  server("a", MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://user:pass@a"}),
			wantErr: "must not contain credentials",
		},
		{
			name:    "stdio without command",
			server:  server("a", MCPServerSpec{Version: "1", Transport: "stdio"}),
			wantErr: "spec.command is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.server.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func server(name string, spec MCPServerSpec) MCPServer {
	return MCPServer{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Spec: spec}
}

// Verifies SPEC-0011-R01.
func TestValidateCredential(t *testing.T) {
	http := func(credential *MCPServerCredential) MCPServer {
		return server("search", MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://search.svc:8080", Credential: credential})
	}
	for _, credential := range []*MCPServerCredential{
		{},
		{Scheme: "Bearer"},
		{Header: "X-API-Key"},
		{Header: "Authorization", Scheme: "Token"},
	} {
		s := http(credential)
		if err := s.Validate(); err != nil {
			t.Errorf("credential %+v rejected: %v", credential, err)
		}
	}
	for want, credential := range map[string]*MCPServerCredential{
		"must not be Cookie":            {Header: "Cookie"},
		"must not be host":              {Header: "host"},
		"must not be Content-Length":    {Header: "Content-Length"},
		"must not be Transfer-Encoding": {Header: "Transfer-Encoding"},
		"must not be Connection":        {Header: "Connection"},
		"must not be X-Kavrynt-Route":   {Header: "X-Kavrynt-Route"},
		"must not be Mcp-Session-Id":    {Header: "Mcp-Session-Id"},
		"must be an HTTP header name":   {Header: "X API Key"},
		"must be a single token":        {Scheme: "Bearer token"},
	} {
		s := http(credential)
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("credential %+v: err = %v, want %q", credential, err, want)
		}
	}
	stdio := server("local", MCPServerSpec{Version: "1", Transport: "stdio", Command: "/bin/server", Credential: &MCPServerCredential{}})
	if err := stdio.Validate(); err == nil || !strings.Contains(err.Error(), "only supported with spec.transport http") {
		t.Errorf("stdio credential: err = %v", err)
	}
}

// Verifies SPEC-0011-R05: the header and value sent for a stored credential.
func TestCredentialHeader(t *testing.T) {
	var none *MCPServerCredential
	if none.HeaderName() != "Authorization" || (&MCPServerCredential{}).HeaderName() != "Authorization" {
		t.Fatal("the default header is not Authorization")
	}
	if got := (&MCPServerCredential{Scheme: "Bearer"}).HeaderValue("abc"); got != "Bearer abc" {
		t.Fatalf("Bearer value = %q", got)
	}
	if got := (&MCPServerCredential{Header: "X-API-Key"}).HeaderValue("abc"); got != "abc" {
		t.Fatalf("raw value = %q", got)
	}
}

// Verifies SPEC-0015-R01.
func TestValidateTokenExchange(t *testing.T) {
	spec := func(exchange *MCPServerTokenExchange) MCPServerSpec {
		return MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://tickets.svc:8080", TokenExchange: exchange}
	}
	for _, exchange := range []*MCPServerTokenExchange{
		{Audience: "https://tickets.example.com"},
		{Audience: "urn:example:tickets", Scopes: []string{"tickets:read", "tickets:write"}},
	} {
		s := server("tickets", spec(exchange))
		if err := s.Validate(); err != nil {
			t.Errorf("token exchange %+v rejected: %v", exchange, err)
		}
	}
	for want, exchange := range map[string]*MCPServerTokenExchange{
		"audience must be an absolute URI":  {Audience: ""},
		"audience must be an absolute URI ": {Audience: "tickets"},
		" audience must be an absolute URI": {Audience: "https://tickets.example.com#x"},
		"must be OAuth scope tokens":        {Audience: "https://t", Scopes: []string{"two words"}},
		"at most 20 scopes":                 {Audience: "https://t", Scopes: make([]string, 21)},
	} {
		s := server("tickets", spec(exchange))
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(want)) {
			t.Errorf("token exchange %+v: err = %v, want %q", exchange, err, strings.TrimSpace(want))
		}
	}
	both := spec(&MCPServerTokenExchange{Audience: "https://t"})
	both.Credential = &MCPServerCredential{}
	if s := server("tickets", both); s.Validate() == nil || !strings.Contains(s.Validate().Error(), "mutually exclusive") {
		t.Errorf("credential and token exchange together: err = %v", s.Validate())
	}
	stdio := server("local", MCPServerSpec{Version: "1", Transport: "stdio", Command: "/bin/server", TokenExchange: &MCPServerTokenExchange{Audience: "https://t"}})
	if err := stdio.Validate(); err == nil || !strings.Contains(err.Error(), "spec.tokenExchange is only supported with spec.transport http") {
		t.Errorf("stdio token exchange: err = %v", err)
	}
	original := &MCPServer{Spec: spec(&MCPServerTokenExchange{Audience: "https://t", Scopes: []string{"a"}})}
	copied := original.DeepCopyObject().(*MCPServer)
	copied.Spec.TokenExchange.Scopes[0] = "changed"
	if original.Spec.TokenExchange.Scopes[0] != "a" {
		t.Fatal("DeepCopyObject shares spec.tokenExchange.scopes")
	}
}
