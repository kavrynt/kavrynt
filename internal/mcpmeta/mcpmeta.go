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

// MCP protocol revisions the Gateway accepts in MCP-Protocol-Version, oldest
// first (SPEC-0018-R04).
var ProtocolVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", "2026-07-28"}

const (
	// DefaultProtocolVersion is assumed for requests without
	// MCP-Protocol-Version, as the MCP specification says.
	DefaultProtocolVersion = "2025-03-26"
	// HeaderProtocolVersion is the first revision that requires the
	// Mcp-Method header on every request (SPEC-0018-R02).
	HeaderProtocolVersion = "2026-07-28"
)

// KnownProtocolVersion reports whether version is in ProtocolVersions.
func KnownProtocolVersion(version string) bool {
	for _, known := range ProtocolVersions {
		if version == known {
			return true
		}
	}
	return false
}
