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
