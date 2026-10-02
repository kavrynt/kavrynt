package v1alpha1

import (
	"errors"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Validate checks the rules shared by kavryctl and the operator. The CRD
// schema enforces the same rules at admission time where it can.
func (in *MCPServer) Validate() error {
	var problems []string

	if name := in.Name; name == "" {
		problems = append(problems, "metadata.name is required")
	} else if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		problems = append(problems, "metadata.name must use lowercase DNS-subdomain syntax")
	}

	if strings.TrimSpace(in.Spec.Version) == "" {
		problems = append(problems, "spec.version is required")
	}

	switch strings.TrimSpace(in.Spec.Transport) {
	case "":
		problems = append(problems, "spec.transport is required")
	case "stdio":
		if strings.TrimSpace(in.Spec.Command) == "" {
			problems = append(problems, "spec.command is required when spec.transport is stdio")
		}
	case "http":
		if err := validateEndpoint(in.Spec.Endpoint); err != nil {
			problems = append(problems, err.Error())
		}
	default:
		problems = append(problems, "spec.transport must be one of: stdio, http")
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("spec.endpoint is required when spec.transport is http")
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("spec.endpoint must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return errors.New("spec.endpoint must not contain credentials")
	}
	return nil
}
