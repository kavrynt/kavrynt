package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kavrynt/registry/internal/model"
)

func TestFileStoreUpsertListGetDelete(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	m := model.Manifest{
		APIVersion: model.APIVersion,
		Kind:       model.Kind,
		Metadata: model.Metadata{
			Name: "example",
		},
		Spec: model.Spec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://localhost:8080",
		},
	}

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	record, created, err := s.Upsert(m, now)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if !created {
		t.Fatal("Upsert reported update for first registration")
	}
	if record.Manifest.Metadata.Name != "example" {
		t.Fatalf("record name = %q", record.Manifest.Metadata.Name)
	}
	if record.ID != "example" {
		t.Fatalf("record ID = %q", record.ID)
	}

	m.Spec.Version = "0.2.0"
	record, created, err = s.Upsert(m, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Upsert update returned error: %v", err)
	}
	if created {
		t.Fatal("Upsert reported create for existing registration")
	}
	if record.Manifest.Spec.Version != "0.2.0" {
		t.Fatalf("record version = %q", record.Manifest.Spec.Version)
	}

	servers, err := s.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("server count = %d", len(servers))
	}

	got, err := s.Get("example")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Manifest.Spec.Version != "0.2.0" {
		t.Fatalf("got version = %q", got.Manifest.Spec.Version)
	}

	if err := s.Delete("example"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := s.Get("example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v", err)
	}
}

func TestFileStoreKeepsSameNameInDifferentNamespaces(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, namespace := range []string{"development", "production"} {
		_, created, err := s.Upsert(model.Manifest{
			APIVersion: model.APIVersion,
			Kind:       model.Kind,
			Metadata: model.Metadata{
				Name:      "payments",
				Namespace: namespace,
			},
			Spec: model.Spec{
				Version:   "0.1.0",
				Transport: "http",
				Endpoint:  "http://payments." + namespace + ".svc.cluster.local:8080/mcp",
			},
		}, time.Now())
		if err != nil || !created {
			t.Fatalf("Upsert(%s) created = %v, error = %v", namespace, created, err)
		}
	}

	servers, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("server count = %d, want 2", len(servers))
	}
	if servers[0].ID != "development.payments" || servers[1].ID != "production.payments" {
		t.Fatalf("server IDs = %q, %q", servers[0].ID, servers[1].ID)
	}

	if err := s.Delete("development.payments"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("production.payments"); err != nil {
		t.Fatalf("deleting development server removed production server: %v", err)
	}
}
