package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kavrynt/kavrynt/internal/mcpmeta"
)

// checkProtocol reports why r is not a well-formed MCP Streamable HTTP
// request, or nil (SPEC-0018). body describes the POST body as inspectRequest
// read it. GET event streams and DELETE session requests have no body to
// check (R05); bodies the Gateway cannot read are not checked.
func checkProtocol(r *http.Request, body bodyInfo) error {
	version := r.Header.Get("MCP-Protocol-Version")
	if version != "" && !mcpmeta.KnownProtocolVersion(version) {
		shown := version
		if len(shown) > 32 {
			shown = shown[:32] + "..."
		}
		return fmt.Errorf("MCP-Protocol-Version %q is not supported; supported versions: %s",
			shown, strings.Join(mcpmeta.ProtocolVersions, ", "))
	}
	if r.Method != http.MethodPost || !body.read {
		return nil
	}
	if body.batch {
		return errors.New("JSON-RPC batches are not supported; send one JSON-RPC message per request")
	}
	if !body.classified || body.method == "" {
		// Unreadable here, or a JSON-RPC response from the client, which has
		// no method to name.
		return nil
	}
	headerMethod := r.Header.Get("Mcp-Method")
	if version == "" {
		version = mcpmeta.DefaultProtocolVersion
	}
	switch {
	case headerDisagrees(r, "Mcp-Method", body.method):
		return errors.New("the Mcp-Method header does not match the request body")
	case body.method == "tools/call" && (headerDisagrees(r, "Mcp-Name", body.tool) || !body.toolOK && r.Header.Get("Mcp-Name") != ""):
		return errors.New("the Mcp-Name header does not match the request body")
	case headerMethod == "" && version >= mcpmeta.HeaderProtocolVersion:
		return fmt.Errorf("MCP-Protocol-Version %s requires the Mcp-Method header", version)
	}
	return nil
}

// headerDisagrees reports whether a value of the header differs from want. A
// repeated header is forwarded with all its values, so each must agree; an
// absent header, or a single empty one, names nothing.
func headerDisagrees(r *http.Request, name, want string) bool {
	values := r.Header.Values(name)
	if len(values) == 1 && values[0] == "" {
		return false
	}
	for _, value := range values {
		if value != want {
			return true
		}
	}
	return false
}
