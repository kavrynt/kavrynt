# Allows only MCP methods that read: initialize, ping, listing and reading
# tools, resources, and prompts, notifications, and opening or closing the
# session's event stream. Every tool call is denied.
package kavrynt.authz

read_methods := {
	"initialize", "ping", "tools/list",
	"resources/list", "resources/templates/list", "resources/read",
	"prompts/list", "prompts/get",
}

default decision := {"allow": false, "reason": "only read methods are allowed"}

decision := {"allow": true} if input.mcp_method in read_methods

decision := {"allow": true} if startswith(input.mcp_method, "notifications/")

decision := {"allow": true} if {
	input.mcp_method == ""
	input.http_method in {"GET", "DELETE"}
}
