package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseYAML(t *testing.T) {
	server, err := Parse([]byte(`
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: payments
  namespace: team-a
  labels:
    app: payments
spec:
  version: 1.0.0
  transport: http
  endpoint: http://payments.team-a.svc:8080/mcp
`))
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "payments" || server.Namespace != "team-a" || server.Labels["app"] != "payments" {
		t.Fatalf("metadata = %+v", server.ObjectMeta)
	}
	if server.Spec.Endpoint != "http://payments.team-a.svc:8080/mcp" {
		t.Fatalf("endpoint = %q", server.Spec.Endpoint)
	}
}

func TestParseRejectsInvalidManifests(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "wrong kind", data: "apiVersion: kavrynt.io/v1alpha1\nkind: Pod\nmetadata: {name: a}\nspec: {version: '1', transport: stdio, command: x}\n", wantErr: `kind must be "MCPServer"`},
		{name: "wrong apiVersion", data: "apiVersion: v1\nkind: MCPServer\nmetadata: {name: a}\nspec: {version: '1', transport: stdio, command: x}\n", wantErr: "apiVersion must be"},
		{name: "unknown field", data: "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: a}\nspec: {version: '1', transport: stdio, command: x, port: 1}\n", wantErr: "parse manifest"},
		{name: "spec rule", data: "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: a}\nspec: {version: '1', transport: http}\n", wantErr: "spec.endpoint is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadExampleManifests(t *testing.T) {
	paths, err := filepath.Glob("../../../examples/kavryctl/*")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no example manifests found: %v", err)
	}
	for _, path := range paths {
		if _, err := Load(path); err != nil {
			t.Fatalf("Load(%s): %v", path, err)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load missing file error = %v", err)
	}
}

// Verifies SPEC-0011-R01: kavryctl validate accepts a declared credential
// and rejects a header that would replace request framing.
func TestParseCredential(t *testing.T) {
	const base = `apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: search
spec:
  version: 1.0.0
  transport: http
  endpoint: http://search.team-a.svc:8080
  credential:
`
	server, err := Parse([]byte(base + "    scheme: Bearer\n"))
	if err != nil {
		t.Fatalf("credential rejected: %v", err)
	}
	if server.Spec.Credential == nil || server.Spec.Credential.Scheme != "Bearer" {
		t.Fatalf("credential = %+v", server.Spec.Credential)
	}
	if _, err := Parse([]byte(base + "    header: Cookie\n")); err == nil {
		t.Fatal("a Cookie credential header was accepted")
	}
	if _, err := Parse([]byte(base + "    value: secret\n")); err == nil {
		t.Fatal("a credential value in the manifest was accepted")
	}
}
