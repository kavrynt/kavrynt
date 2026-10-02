package validation

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/kavrynt/kavrynt/internal/registry/model"
)

var namePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$`)

func Manifest(m model.Manifest) error {
	var problems []string

	if strings.TrimSpace(m.APIVersion) == "" {
		problems = append(problems, "apiVersion is required")
	} else if m.APIVersion != model.APIVersion {
		problems = append(problems, fmt.Sprintf("apiVersion must be %q", model.APIVersion))
	}

	if strings.TrimSpace(m.Kind) == "" {
		problems = append(problems, "kind is required")
	} else if m.Kind != model.Kind {
		problems = append(problems, fmt.Sprintf("kind must be %q", model.Kind))
	}

	name := strings.TrimSpace(m.Metadata.Name)
	if name == "" {
		problems = append(problems, "metadata.name is required")
	} else if !namePattern.MatchString(name) {
		problems = append(problems, "metadata.name must use lowercase DNS-subdomain syntax")
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
