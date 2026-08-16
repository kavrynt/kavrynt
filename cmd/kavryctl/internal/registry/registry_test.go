package registry

import (
	"testing"
	"time"

	"github.com/kavrynt/kavryctl/internal/manifest"
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

func TestRegisterKeepsSameNameInDifferentNamespaces(t *testing.T) {
	home := t.TempDir()
	for _, namespace := range []string{"development", "production"} {
		server, created, err := Register(home, manifest.Manifest{
			APIVersion: manifest.APIVersion,
			Kind:       manifest.Kind,
			Metadata: manifest.Metadata{
				Name:      "payments",
				Namespace: namespace,
			},
			Spec: manifest.Spec{
				Version:   "0.1.0",
				Transport: "stdio",
				Command:   "payments",
			},
		}, namespace+".json", time.Now())
		if err != nil || !created {
			t.Fatalf("Register(%s) created = %v, error = %v", namespace, created, err)
		}
		if server.ID != namespace+".payments" {
			t.Fatalf("server ID = %q", server.ID)
		}
	}

	servers, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("server count = %d, want 2", len(servers))
	}
}
