package model

import (
	"encoding/json"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
)

type Route struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
	// Credential is set when the server needs its stored upstream credential
	// (SPEC-0011). It never holds the value.
	Credential *kavryntv1alpha1.MCPServerCredential `json:"-"`
	// TokenExchange is set when the server gets each caller's exchanged
	// token (SPEC-0015).
	TokenExchange *kavryntv1alpha1.MCPServerTokenExchange `json:"-"`
	// Tools maps each tool of the server's last successful discovery to
	// its recorded behavior hints, for the policy input (SPEC-0028). It is
	// never modified after the route is built.
	Tools map[string]map[string]bool `json:"-"`
	// SessionAffinity sends every request of an MCP session to the pod of
	// the endpoint's Service that started it (SPEC-0026).
	SessionAffinity bool `json:"-"`
	// ForwardSubpaths forwards /mcp/<route>/<suffix> to the endpoint path
	// plus the suffix; without it only the route itself is (SPEC-0029).
	ForwardSubpaths bool `json:"-"`
}

// ServerTokenExchange tells the console that a server gets per-caller
// tokens by token exchange (SPEC-0015).
type ServerTokenExchange struct {
	Audience string `json:"audience"`
}

// ServerCredential reports whether a server needs a stored credential and
// whether the Gateway has it for the current endpoint (SPEC-0011-R07).
type ServerCredential struct {
	Required  bool `json:"required"`
	Available bool `json:"available"`
}

// Server is the read-only view of one MCPServer served at /v1/servers. It
// leaves out spec.command, spec.args, and spec.environment, which may hold
// secrets, and the endpoint's query string.
type Server struct {
	Namespace   string      `json:"namespace"`
	Name        string      `json:"name"`
	Route       string      `json:"route"`
	Description string      `json:"description,omitempty"`
	Version     string      `json:"version"`
	Transport   string      `json:"transport"`
	Endpoint    string      `json:"endpoint,omitempty"`
	Routed      bool        `json:"routed"`
	Conditions  []Condition `json:"conditions"`
	Tools       []Tool      `json:"tools"`
	// ServerInfo is the name and version the server reported to the
	// Operator in MCP initialize. Self-reported and untrusted.
	ServerInfo *ServerInfo `json:"serverInfo,omitempty"`
	// Credential is set for servers that declare spec.credential.
	Credential *ServerCredential `json:"credential,omitempty"`
	// TokenExchange is set for servers that declare spec.tokenExchange.
	TokenExchange *ServerTokenExchange `json:"tokenExchange,omitempty"`
	// SessionAffinity is None or MCPSession (SPEC-0026).
	SessionAffinity string    `json:"sessionAffinity"`
	CreatedAt       time.Time `json:"createdAt"`
}

// ServerInfo is an MCP server's reported name and version.
type ServerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// Tool is a tool the Operator discovered. Its fields come from the MCP
// server and are untrusted.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	// Annotations are the recorded behavior hints by MCP name (SPEC-0028).
	Annotations map[string]bool `json:"annotations,omitempty"`
}
