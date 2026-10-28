package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	MCPServerKind = "MCPServer"

	// ConditionAccepted reports whether the spec passed validation.
	ConditionAccepted = "Accepted"
	// ConditionReady reports whether the Gateway can route to the server.
	ConditionReady = "Ready"
	// ConditionToolsDiscovered reports whether the Operator listed the
	// server's tools. It never affects Ready.
	ConditionToolsDiscovered = "ToolsDiscovered"

	ReasonValid                = "Valid"
	ReasonInvalidSpec          = "InvalidSpec"
	ReasonRoutable             = "Routable"
	ReasonUnsupportedTransport = "UnsupportedTransport"
	ReasonNamespaceNotWatched  = "NamespaceNotWatched"
	ReasonDiscovered           = "Discovered"
	ReasonDiscoveryFailed      = "DiscoveryFailed"
	ReasonNotRoutable          = "NotRoutable"
	// ReasonCredentialUnavailable: the server declares a credential that is
	// not stored, or not pinned to its endpoint (SPEC-0011).
	ReasonCredentialUnavailable = "CredentialUnavailable" // #nosec G101 -- a condition reason, not a credential.
	// ReasonCallerTokenRequired: the server gets per-caller tokens by token
	// exchange, so the Operator, which has no caller, does not list its tools
	// (SPEC-0015-R10).
	ReasonCallerTokenRequired = "CallerTokenRequired" // #nosec G101 -- a condition reason, not a credential.
	// ReasonEndpointNotAllowed: the endpoint reaches another namespace, or
	// a cluster address, that the platform team did not allow (SPEC-0029).
	ReasonEndpointNotAllowed = "EndpointNotAllowed"
	// ReasonAudienceNotAllowed: spec.tokenExchange.audience is not allowed
	// for the server's namespace (SPEC-0029).
	ReasonAudienceNotAllowed = "AudienceNotAllowed"

	// DescriptionAnnotation holds an optional human-readable description.
	DescriptionAnnotation = "kavrynt.io/description"
)

type MCPServerSpec struct {
	Version     string            `json:"version"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	// Credential declares that the endpoint needs a credential, which the
	// platform team stores for this server (SPEC-0011). The value is never
	// part of the MCPServer.
	Credential *MCPServerCredential `json:"credential,omitempty"`
	// TokenExchange makes the Gateway exchange each caller's access token
	// for one issued to this server (RFC 8693, SPEC-0015). It excludes
	// Credential.
	TokenExchange *MCPServerTokenExchange `json:"tokenExchange,omitempty"`
	// SessionAffinity is None (or empty) or MCPSession, which sends every
	// request of an MCP session to the pod that started it (SPEC-0026).
	SessionAffinity string `json:"sessionAffinity,omitempty"`
	// ForwardSubpaths makes the Gateway forward requests for paths below
	// the route to the same paths below the endpoint (SPEC-0029-R03).
	ForwardSubpaths bool `json:"forwardSubpaths,omitempty"`
}

const (
	// SessionAffinityNone forwards every request to the endpoint as is.
	SessionAffinityNone = "None"
	// SessionAffinityMCPSession pins each Mcp-Session-Id to one pod of the
	// endpoint's Service.
	SessionAffinityMCPSession = "MCPSession"
)

// ServiceRef names a Service port by the Service's namespace and name.
type ServiceRef struct {
	Namespace string
	Name      string
	Port      int
}

// MCPServerTokenExchange names the token the Gateway asks the caller's
// authorization server for.
type MCPServerTokenExchange struct {
	// Audience is the server's audience, an absolute URI.
	Audience string `json:"audience"`
	// Scopes are requested for the exchanged token.
	Scopes []string `json:"scopes,omitempty"`
}

// Copy returns a deep copy.
func (in *MCPServerTokenExchange) Copy() *MCPServerTokenExchange {
	if in == nil {
		return nil
	}
	out := *in
	out.Scopes = append([]string(nil), in.Scopes...)
	return &out
}

// MCPServerCredential says how the Gateway and the Operator send a server's
// stored credential.
type MCPServerCredential struct {
	// Header carries the credential; empty means Authorization.
	Header string `json:"header,omitempty"`
	// Scheme prefixes the value with a space, for example Bearer.
	Scheme string `json:"scheme,omitempty"`
}

// HeaderName returns the header that carries the credential.
func (c *MCPServerCredential) HeaderName() string {
	if c == nil || c.Header == "" {
		return "Authorization"
	}
	return c.Header
}

// HeaderValue returns the header value for a stored credential.
func (c *MCPServerCredential) HeaderValue(value string) string {
	if c == nil || c.Scheme == "" {
		return value
	}
	return c.Scheme + " " + value
}

type MCPServerStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	// Tools are the server's tools from MCP tools/list, bounded by the
	// Operator. They come from the MCP server and are untrusted.
	Tools []MCPServerTool `json:"tools,omitempty"`
	// ServerInfo is the name and version the server reported in MCP
	// initialize, bounded by the Operator. Self-reported and untrusted.
	ServerInfo *MCPServerInfo `json:"serverInfo,omitempty"`
}

// MCPServerInfo is the serverInfo an MCP server reports in initialize.
type MCPServerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// CopyServerInfo returns a copy of info.
func CopyServerInfo(info *MCPServerInfo) *MCPServerInfo {
	if info == nil {
		return nil
	}
	out := *info
	return &out
}

// MCPServerTool is one tool reported by an MCP server.
type MCPServerTool struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	InputSchema *runtime.RawExtension     `json:"inputSchema,omitempty"`
	Annotations *MCPServerToolAnnotations `json:"annotations,omitempty"`
}

// MCPServerToolAnnotations are the behavior hints an MCP server reports for
// a tool (SPEC-0028). They are the server's own claims and untrusted. A nil
// hint was not reported, which differs from false.
type MCPServerToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// Hints returns the reported hints by their MCP names, without the absent
// ones; it is empty, never nil, for nil annotations.
func (a *MCPServerToolAnnotations) Hints() map[string]bool {
	hints := map[string]bool{}
	if a == nil {
		return hints
	}
	for name, hint := range map[string]*bool{
		"readOnlyHint":    a.ReadOnlyHint,
		"destructiveHint": a.DestructiveHint,
		"idempotentHint":  a.IdempotentHint,
		"openWorldHint":   a.OpenWorldHint,
	} {
		if hint != nil {
			hints[name] = *hint
		}
	}
	return hints
}

// Copy returns a deep copy of a.
func (a *MCPServerToolAnnotations) Copy() *MCPServerToolAnnotations {
	if a == nil {
		return nil
	}
	clone := func(hint *bool) *bool {
		if hint == nil {
			return nil
		}
		return new(*hint)
	}
	return &MCPServerToolAnnotations{
		ReadOnlyHint:    clone(a.ReadOnlyHint),
		DestructiveHint: clone(a.DestructiveHint),
		IdempotentHint:  clone(a.IdempotentHint),
		OpenWorldHint:   clone(a.OpenWorldHint),
	}
}

type MCPServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MCPServerSpec   `json:"spec,omitempty"`
	Status MCPServerStatus `json:"status,omitempty"`
}

type MCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []MCPServer `json:"items"`
}

func (in *MCPServer) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(MCPServer)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	if in.Spec.Args != nil {
		out.Spec.Args = append([]string(nil), in.Spec.Args...)
	}
	if in.Spec.Environment != nil {
		out.Spec.Environment = make(map[string]string, len(in.Spec.Environment))
		for key, value := range in.Spec.Environment {
			out.Spec.Environment[key] = value
		}
	}
	if in.Spec.Credential != nil {
		credential := *in.Spec.Credential
		out.Spec.Credential = &credential
	}
	out.Spec.TokenExchange = in.Spec.TokenExchange.Copy()
	if in.Status.Conditions != nil {
		out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
	}
	out.Status.Tools = CopyTools(in.Status.Tools)
	out.Status.ServerInfo = CopyServerInfo(in.Status.ServerInfo)
	return out
}

// CopyTools returns a deep copy of tools.
func CopyTools(tools []MCPServerTool) []MCPServerTool {
	if tools == nil {
		return nil
	}
	out := make([]MCPServerTool, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if tool.InputSchema != nil {
			out[i].InputSchema = tool.InputSchema.DeepCopy()
		}
		out[i].Annotations = tool.Annotations.Copy()
	}
	return out
}

func (in *MCPServerList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(MCPServerList)
	*out = *in
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		out.Items = make([]MCPServer, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopyObject().(*MCPServer)
		}
	}
	return out
}

// RouteName is the stable Gateway route identifier: <namespace>.<name>.
func (in *MCPServer) RouteName() string {
	if in.Namespace == "" {
		return in.Name
	}
	return in.Namespace + "." + in.Name
}
