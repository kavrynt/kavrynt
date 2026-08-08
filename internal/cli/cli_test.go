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
}
