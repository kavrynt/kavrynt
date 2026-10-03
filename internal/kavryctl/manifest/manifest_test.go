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
