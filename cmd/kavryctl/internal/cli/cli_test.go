package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterListInspectWorkflow(t *testing.T) {
	home := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "server.json")
	content := `{
  "apiVersion": "kavrynt.io/v1alpha1",
  "kind": "MCPServer",
  "metadata": {"name": "example"},
  "spec": {"version": "0.1.0", "transport": "stdio", "command": "python3"}
}`
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := Execute([]string{"register", "--home", home, manifestPath}, &out, &errOut); code != 0 {
		t.Fatalf("register code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "registered MCP server example@0.1.0") {
		t.Fatalf("unexpected register output: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := Execute([]string{"list", "--home", home}, &out, &errOut); code != 0 {
		t.Fatalf("list code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "example") {
		t.Fatalf("unexpected list output: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := Execute([]string{"inspect", "--home", home, "example"}, &out, &errOut); code != 0 {
		t.Fatalf("inspect code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"name": "example"`) {
		t.Fatalf("unexpected inspect output: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := Execute([]string{"unregister", "--home", home, "example"}, &out, &errOut); code != 0 {
		t.Fatalf("unregister code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "unregistered MCP server example") {
		t.Fatalf("unexpected unregister output: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := Execute([]string{"list", "--home", home}, &out, &errOut); code != 0 {
		t.Fatalf("list after unregister code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no MCP servers registered") {
		t.Fatalf("unexpected list after unregister output: %s", out.String())
	}
}

func TestRegistryAndHomeConflict(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	code := Execute([]string{"list", "--home", t.TempDir(), "--registry", "http://registry.example"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "--registry and --home cannot be used together") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
}

func TestInvalidRegistryURL(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "server.json")
	content := `{
  "apiVersion": "kavrynt.io/v1alpha1",
  "kind": "MCPServer",
  "metadata": {"name": "example"},
  "spec": {"version": "0.1.0", "transport": "stdio", "command": "python3"}
}`
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := Execute([]string{"register", "--registry", "localhost:8080", manifestPath}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "registry URL must use http or https") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
}

func TestReleaseVersion(t *testing.T) {
	tests := map[string]string{
		"v1.2.3":    "1.2.3",
		"1.2.3":     "1.2.3",
		"0.1.0-dev": "",
		"unknown":   "",
	}
	for input, want := range tests {
		if got := releaseVersion(input); got != want {
			t.Errorf("releaseVersion(%q) = %q, want %q", input, got, want)
		}
	}
}
