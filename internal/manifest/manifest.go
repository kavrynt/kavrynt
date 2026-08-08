package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const (
	APIVersion = "kavrynt.io/v1alpha1"
	Kind       = "MCPServer"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type Manifest struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
}

type Metadata struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type Spec struct {
	Version     string            `json:"version"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest JSON: %w", err)
	}

	if err := Validate(m); err != nil {
		return Manifest{}, err
	}

	return m, nil
}

func Validate(m Manifest) error {
	var problems []string

	if strings.TrimSpace(m.APIVersion) == "" {
		problems = append(problems, "apiVersion is required")
	} else if m.APIVersion != APIVersion {
		problems = append(problems, fmt.Sprintf("apiVersion must be %q", APIVersion))
	}

	if strings.TrimSpace(m.Kind) == "" {
		problems = append(problems, "kind is required")
	} else if m.Kind != Kind {
		problems = append(problems, fmt.Sprintf("kind must be %q", Kind))
	}

	name := strings.TrimSpace(m.Metadata.Name)
	if name == "" {
		problems = append(problems, "metadata.name is required")
	} else if !namePattern.MatchString(name) {
		problems = append(problems, "metadata.name must use lowercase DNS-label syntax")
	}

	if strings.TrimSpace(m.Spec.Version) == "" {
		problems = append(problems, "spec.version is required")
	}

	switch strings.TrimSpace(m.Spec.Transport) {
	case "":
		problems = append(problems, "spec.transport is required")
	case "stdio":
		if strings.TrimSpace(m.Spec.Command) == "" {
			problems = append(problems, "spec.command is required when spec.transport is stdio")
		}
	case "http":
		if strings.TrimSpace(m.Spec.Endpoint) == "" {
			problems = append(problems, "spec.endpoint is required when spec.transport is http")
		} else if _, err := url.ParseRequestURI(m.Spec.Endpoint); err != nil {
			problems = append(problems, "spec.endpoint must be a valid URI")
		}
	default:
		problems = append(problems, "spec.transport must be one of: stdio, http")
	}

	if len(problems) > 0 {
		return errors.New("invalid manifest: " + strings.Join(problems, "; "))
	}

	return nil
}
