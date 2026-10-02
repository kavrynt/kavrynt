package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/registry/model"
)

func TestFileStoreUsesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits through os.FileMode")
	}

	directory := filepath.Join(t.TempDir(), "registry-data")
	path := filepath.Join(directory, "registry.json")
	if _, err := NewFileStore(path); err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("stat registry directory: %v", err)
	}
	if permissions := directoryInfo.Mode().Perm(); permissions&0o077 != 0 {
		t.Fatalf("registry directory permissions = %04o, want owner-only", permissions)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat registry file: %v", err)
	}
	if permissions := fileInfo.Mode().Perm(); permissions&0o077 != 0 {
		t.Fatalf("registry file permissions = %04o, want owner-only", permissions)
	}
}

func TestFileStoreUpsertListGetDelete(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	servers, err := s.List()
	if err != nil {
		t.Fatalf("initial List returned error: %v", err)
	}
	if servers == nil {
		t.Fatal("initial List returned nil slice")
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

	servers, err = s.List()
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
