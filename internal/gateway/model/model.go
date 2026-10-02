package model

import "time"

const (
	APIVersion = "kavrynt.io/v1alpha1"
	Kind       = "MCPServer"
)

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

type ServerRecord struct {
	Manifest     Manifest  `json:"manifest"`
	RegisteredAt time.Time `json:"registeredAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type ServerList struct {
	Servers []ServerRecord `json:"servers"`
}

type Route struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
}
