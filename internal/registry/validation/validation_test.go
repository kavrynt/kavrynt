package validation

import (
	"testing"

	"github.com/kavrynt/kavrynt/internal/registry/model"
)

func TestManifestAcceptsHTTPServer(t *testing.T) {
	m := model.Manifest{
		APIVersion: model.APIVersion,
		Kind:       model.Kind,
		Metadata: model.Metadata{
			Name: "example",
		},
		Spec: model.Spec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://example.default.svc.cluster.local:8080",
		},
	}

	if err := Manifest(m); err != nil {
		t.Fatalf("Manifest returned error: %v", err)
	}
}

func TestManifestAcceptsNamespacedRegistryName(t *testing.T) {
	m := model.Manifest{
		APIVersion: model.APIVersion,
		Kind:       model.Kind,
		Metadata: model.Metadata{
			Name: "default.example-mcp-server",
		},
		Spec: model.Spec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://example.default.svc.cluster.local:8080",
		},
	}

	if err := Manifest(m); err != nil {
		t.Fatalf("Manifest returned error: %v", err)
	}
}

func TestManifestRejectsInvalidTransport(t *testing.T) {
	m := model.Manifest{
		APIVersion: model.APIVersion,
		Kind:       model.Kind,
		Metadata: model.Metadata{
			Name: "example",
		},
		Spec: model.Spec{
			Version:   "0.1.0",
			Transport: "grpc",
		},
	}

	if err := Manifest(m); err == nil {
		t.Fatal("Manifest returned nil error")
	}
}

func TestManifestRejectsInvalidName(t *testing.T) {
	m := model.Manifest{
		APIVersion: model.APIVersion,
		Kind:       model.Kind,
		Metadata: model.Metadata{
			Name: "Bad_Name",
		},
		Spec: model.Spec{
			Version:   "0.1.0",
			Transport: "stdio",
			Command:   "python3",
		},
	}

	if err := Manifest(m); err == nil {
		t.Fatal("Manifest returned nil error")
	}
}
