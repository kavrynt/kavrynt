// Package mcpmeta holds the limits the Gateway and the Operator apply to MCP
// metadata reported by MCP servers or clients, which is untrusted input.
package mcpmeta

import "regexp"

const (
	// MaxToolsPerServer bounds the tools tracked for one MCP server: Gateway
	// metric labels per route and tools in MCPServer status.
	MaxToolsPerServer = 200
	// MaxToolNameLength bounds a tool name in bytes.
	MaxToolNameLength = 128
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// ValidName reports whether value is a non-empty name of at most maxLength
// bytes using only letters, digits, and "_./-".
func ValidName(value string, maxLength int) bool {
	return value != "" && len(value) <= maxLength && namePattern.MatchString(value)
}
