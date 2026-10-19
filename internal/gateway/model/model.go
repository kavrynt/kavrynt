package model

import (
	"encoding/json"
	"time"
)

type Route struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
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
	CreatedAt   time.Time   `json:"createdAt"`
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
}
