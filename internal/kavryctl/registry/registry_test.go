package registry

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
)

func TestRegisterCreatesAndUpdatesServer(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	m := manifest.Manifest{
		APIVersion: manifest.APIVersion,
		Kind:       manifest.Kind,
		Metadata: manifest.Metadata{
			Name: "example",
		},
		Spec: manifest.Spec{
			Version:   "0.1.0",
			Transport: "stdio",
			Command:   "python3",
		},
	}

	server, created, err := Register(home, m, "example.json", now)
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if !created {
		t.Fatal("Register reported update for first registration")
	}
	if server.Manifest.Metadata.Name != "example" {
		t.Fatalf("registered name = %q", server.Manifest.Metadata.Name)
	}

	m.Spec.Version = "0.2.0"
	server, created, err = Register(home, m, "example.json", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Register update returned error: %v", err)
	}
	if created {
		t.Fatal("Register reported create for existing server")
	}
	if server.Manifest.Spec.Version != "0.2.0" {
		t.Fatalf("updated version = %q", server.Manifest.Spec.Version)
	}

	servers, err := List(home)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("server count = %d", len(servers))
	}

	if err := Unregister(home, "example"); err != nil {
		t.Fatalf("Unregister returned error: %v", err)
	}
	servers, err = List(home)
	if err != nil {
		t.Fatalf("List after unregister returned error: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("server count after unregister = %d", len(servers))
	}
}

func TestRegistryUsesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits through os.FileMode")
	}

	parent := t.TempDir()
	home := parent + "/state"

	if err := Init(home); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	homeInfo, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat home: %v", err)
	}
	if got := homeInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("home permissions = %o, want 700", got)
	}

	fileInfo, err := os.Stat(Path(home))
	if err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("registry permissions = %o, want 600", got)
	}
}
