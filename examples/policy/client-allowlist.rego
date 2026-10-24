# Lets each client call only the tools listed for it, per route. Other MCP
# methods (initialize, tools/list, notifications, ...) are allowed for any
# authenticated client. Clients are the token's client_id (or azp).
package kavrynt.authz

# "<namespace>.<name>" of the route -> client_id -> tools it may call.
allowed_tools := {"team-a.search": {"support-agent": {"search", "get_document"}}}

default decision := {"allow": false, "reason": "tool not allowed for client"}

route := sprintf("%s.%s", [input.route.namespace, input.route.name])

decision := {"allow": true} if {
	input.issuer != ""
	input.mcp_method != "tools/call"
}

decision := {"allow": true} if {
	input.issuer != ""
	input.mcp_method == "tools/call"
	input.tool in allowed_tools[route][input.client_id]
}
