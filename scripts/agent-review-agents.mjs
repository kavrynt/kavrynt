// Builds the --agents JSON for the agent-review workflow from .claude/agents/*.md.
//
// Claude Code runs with --bare in CI so that hooks and MCP servers from the
// checkout never run while the API key is in the environment. --bare skips
// .claude/agents/, so the definitions are passed explicitly. Only the prompt
// text is taken from the files; tools are fixed to read-only file tools, so a
// pull request cannot widen them by editing a definition.
//
// The prompt is read from <ref> (the pull request's base commit), so a pull
// request cannot rewrite the agent that reviews it. A definition the ref does
// not have yet is read from the working tree.
//
// Usage: node scripts/agent-review-agents.mjs <ref> <out.json> <agent-name>...
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";

const [ref, out, ...names] = process.argv.slice(2);
if (!ref || !out || names.length === 0) {
  console.error("usage: agent-review-agents.mjs <ref> <out.json> <agent-name>...");
  process.exit(2);
}

function definition(name) {
  const path = `.claude/agents/${name}.md`;
  try {
    return execFileSync("git", ["show", `${ref}:${path}`], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
  } catch {
    console.error(`${name}: not in ${ref}, using the working tree`);
    return readFileSync(path, "utf8");
  }
}

const agents = {};
for (const name of names) {
  if (!/^[a-z0-9-]+$/.test(name)) {
    console.error(`invalid agent name: ${name}`);
    process.exit(2);
  }
  const text = definition(name);
  const m = text.match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/);
  if (!m) {
    console.error(`${name}: no front matter`);
    process.exit(1);
  }
  const description = m[1].match(/^description:\s*(.+)$/m)?.[1]?.trim();
  if (!description) {
    console.error(`${name}: no description`);
    process.exit(1);
  }
  agents[name] = { description, prompt: m[2].trim(), tools: ["Read", "Grep", "Glob"] };
}
writeFileSync(out, JSON.stringify(agents));
