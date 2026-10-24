# Allows every MCP request from a caller that MCP client authentication
# verified (SPEC-0006), and nothing else. A starting point before writing
# per-tool rules.
package kavrynt.authz

default decision := {"allow": false, "reason": "authentication required"}

decision := {"allow": true} if input.issuer != ""
