// Unit tests for the console's formatting and connection snippets. Run with
// `make console-test` (node --test, no npm dependencies).
import { test } from "node:test";
import assert from "node:assert/strict";

import format from "../../internal/gateway/console/static/format.js";

const { connectSnippets } = format;

const url = "https://gateway.example.com/mcp/team-sre.incidents";

// The MCP Python SDK 2.x renamed streamablehttp_client to
// streamable_http_client, which yields two streams, not three (issue #79).
test("the Python snippet uses the MCP Python SDK 2.x client", () => {
  const python = connectSnippets("incidents", url)["Python agent"];
  assert.match(python, /^# pip install "mcp>=2"$/m);
  assert.match(python, /^from mcp\.client\.streamable_http import streamable_http_client$/m);
  assert.match(python, /^    async with streamable_http_client\("https:\/\/gateway\.example\.com\/mcp\/team-sre\.incidents"\) as \(read, write\):$/m);
  assert.doesNotMatch(python, /streamablehttp_client/);
});

test("every snippet names the route URL", () => {
  const snippets = connectSnippets("incidents", url);
  assert.deepEqual(Object.keys(snippets), ["MCP client", "Python agent", "curl"]);
  for (const [name, snippet] of Object.entries(snippets)) {
    assert.ok(snippet.includes(url), name + " does not name the URL");
  }
  assert.deepEqual(JSON.parse(snippets["MCP client"]), { mcpServers: { incidents: { type: "http", url } } });
});

// Verifies SPEC-0028-R08: every recorded hint is shown, in a fixed order,
// and absent hints are not.
test("tool hints are labelled in a fixed order", () => {
  assert.deepEqual(format.toolHints({ openWorldHint: false, destructiveHint: true, readOnlyHint: false, idempotentHint: true }),
    ["not read-only", "destructive", "idempotent", "closed world"]);
  assert.deepEqual(format.toolHints({ readOnlyHint: true, openWorldHint: true }), ["read-only", "open world"]);
  assert.deepEqual(format.toolHints({}), []);
  assert.deepEqual(format.toolHints(undefined), []);
  assert.deepEqual(format.toolHints({ readOnlyHint: "yes", dangerous: true }), []);
});
