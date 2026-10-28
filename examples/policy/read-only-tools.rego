# Allows the read methods of read-only.rego everywhere, and tool calls only
# for tools that the MCP server reported as read-only at its last discovery,
# on servers listed in trusted_servers.
#
# Hints are the server's own claims: a server can call a destructive tool
# read-only. List only servers whose code you trust. Tools the server added
# since its last discovery are denied (tool_discovered is false).
package kavrynt.authz

trusted_servers := {
	{"namespace": "team-a", "name": "search"},
}

read_methods := {
	"initialize", "ping", "tools/list",
	"resources/list", "resources/templates/list", "resources/read",
	"prompts/list", "prompts/get",
}

default decision := {"allow": false, "reason": "only read methods and read-only tools of trusted servers are allowed"}

decision := {"allow": true} if input.mcp_method in read_methods

decision := {"allow": true} if startswith(input.mcp_method, "notifications/")

decision := {"allow": true} if {
	input.mcp_method == ""
	input.http_method in {"GET", "DELETE"}
}

decision := {"allow": true} if {
	input.mcp_method == "tools/call"
	input.route in trusted_servers
	input.tool_discovered
	input.tool_annotations.readOnlyHint == true
}
