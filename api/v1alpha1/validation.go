package v1alpha1

import (
	"errors"
	"net/url"
	"regexp"
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
	if in.Spec.Credential != nil {
		problems = append(problems, validateCredential(in.Spec.Credential, in.Spec.Transport)...)
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// headerName is an HTTP field name (RFC 9110 token).
var headerName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,128}$")

// reservedHeaders are headers a credential must not replace: they frame the
// request, route it, or carry MCP protocol state (SPEC-0011-R01).
var reservedHeaders = map[string]bool{
	"host": true, "content-length": true, "content-type": true, "transfer-encoding": true, "connection": true,
	"keep-alive": true, "te": true, "trailer": true, "upgrade": true, "proxy-connection": true,
	"cookie": true, "x-kavrynt-route": true, "x-forwarded-host": true,
}

func validateCredential(credential *MCPServerCredential, transport string) []string {
	var problems []string
	if strings.TrimSpace(transport) != "http" {
		problems = append(problems, "spec.credential is only supported with spec.transport http")
	}
	header := credential.HeaderName()
	lower := strings.ToLower(header)
	switch {
	case !headerName.MatchString(header):
		problems = append(problems, "spec.credential.header must be an HTTP header name")
	case reservedHeaders[lower] || strings.HasPrefix(lower, "mcp-"):
		problems = append(problems, "spec.credential.header must not be "+header)
	}
	if credential.Scheme != "" && !headerName.MatchString(credential.Scheme) {
		problems = append(problems, "spec.credential.scheme must be a single token, for example Bearer")
	}
	return problems
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
