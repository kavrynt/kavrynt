// Package manifest loads MCPServer resources from YAML or JSON files.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"strings"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// document mirrors an MCPServer. Unknown fields are rejected.
type document struct {
	APIVersion string                        `json:"apiVersion"`
	Kind       string                        `json:"kind"`
	Metadata   metadata                      `json:"metadata"`
	Spec       kavryntv1alpha1.MCPServerSpec `json:"spec"`
}

type metadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Load reads, parses, and validates one MCPServer manifest.
func Load(path string) (*kavryntv1alpha1.MCPServer, error) {
	// #nosec G304 -- reading the explicit manifest path is the command's purpose.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return Parse(data)
}

// Parse decodes and validates one MCPServer manifest in YAML or JSON.
func Parse(data []byte) (*kavryntv1alpha1.MCPServer, error) {
	var doc document
	if err := yaml.UnmarshalStrict(data, &doc); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	var problems []string
	if doc.APIVersion != kavryntv1alpha1.GroupVersion.String() {
		problems = append(problems, fmt.Sprintf("apiVersion must be %q", kavryntv1alpha1.GroupVersion.String()))
	}
	if doc.Kind != kavryntv1alpha1.MCPServerKind {
		problems = append(problems, fmt.Sprintf("kind must be %q", kavryntv1alpha1.MCPServerKind))
	}

	server := &kavryntv1alpha1.MCPServer{
		TypeMeta: metav1.TypeMeta{APIVersion: doc.APIVersion, Kind: doc.Kind},
		ObjectMeta: metav1.ObjectMeta{
			Name:        doc.Metadata.Name,
			Namespace:   doc.Metadata.Namespace,
			Labels:      doc.Metadata.Labels,
			Annotations: doc.Metadata.Annotations,
		},
		Spec: doc.Spec,
	}
	if err := server.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return nil, errors.New("invalid manifest: " + strings.Join(problems, "; "))
	}
	return server, nil
}
