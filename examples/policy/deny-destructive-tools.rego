# Allows every call except tool calls that may be destructive, which only the
# clients in ops_clients may make. A client ID is unique only within its
# issuer, so each entry names both.
#
# Fails closed: a tool counts as safe only on a server listed in
# trusted_servers, and only when that server's last discovery of the current
# spec reported it read-only, or reported destructiveHint false. Everything
# else may be destructive:
#   - every tool while discovery is failing, for example while the server's
#     pod restarts (tool_discovered is false and tool_annotations is {});
#   - tools the server added since its last discovery;
#   - tools without hints (MCP's default for an absent destructiveHint is
#     true).
# A deny rule written as input.tool_annotations.destructiveHint == true lets
# all of those through.
#
# Hints are the server's own claims: a server can call a destructive tool
# read-only. List only servers whose code you trust; every tool of any other
# server is treated as destructive.
package kavrynt.authz

trusted_servers := {
	{"namespace": "team-a", "name": "search"},
}

ops_clients := {
	{"issuer": "https://login.example.com", "client_id": "ops-agent"},
}

default decision := {"allow": false, "reason": "this tool may be destructive; only ops clients may call it"}

known_safe if {
	input.route in trusted_servers
	input.tool_discovered
	input.tool_annotations.readOnlyHint == true
}

known_safe if {
	input.route in trusted_servers
	input.tool_discovered
	input.tool_annotations.destructiveHint == false
}

may_be_destructive if {
	input.mcp_method == "tools/call"
	not known_safe
}

decision := {"allow": true} if not may_be_destructive

decision := {"allow": true} if {
	may_be_destructive
	{"issuer": input.issuer, "client_id": input.client_id} in ops_clients
}
