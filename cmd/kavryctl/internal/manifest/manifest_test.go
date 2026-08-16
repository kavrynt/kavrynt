package manifest

import "testing"

func TestValidateAcceptsStdioManifest(t *testing.T) {
	m := Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			Name: "example-mcp-server",
		},
		Spec: Spec{
			Version:   "0.1.0",
			Transport: "stdio",
			Command:   "python3",
		},
	}

	if err := Validate(m); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateRejectsMissingCommandForStdio(t *testing.T) {
	m := Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			Name: "example",
		},
		Spec: Spec{
			Version:   "0.1.0",
			Transport: "stdio",
		},
	}

	if err := Validate(m); err == nil {
		t.Fatal("Validate returned nil error")
	}
}

func TestValidateRejectsInvalidName(t *testing.T) {
	m := Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			Name: "Bad_Name",
		},
		Spec: Spec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://localhost:8080",
		},
	}

	if err := Validate(m); err == nil {
		t.Fatal("Validate returned nil error")
	}
}

func TestValidateRejectsInvalidNamespace(t *testing.T) {
	m := Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			Name:      "example",
			Namespace: "Bad_Namespace",
		},
		Spec: Spec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://localhost:8080",
		},
	}

	if err := Validate(m); err == nil {
		t.Fatal("Validate returned nil error")
	}
}
