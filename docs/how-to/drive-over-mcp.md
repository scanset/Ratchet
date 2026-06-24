# Driving a ratchet over MCP

`ratchet mcp <dir>` serves a ratchet over stdio JSON-RPC
([MCP](https://modelcontextprotocol.io)). This is the same engine the local console uses, exposed so a
capable orchestrator (Claude, or any MCP client) can take the operator seat: it browses and calls
capabilities while the local model keeps filling the narrow, Oracle-checked slots. One engine, two
callers.

> **Two ways a frontier model drives.** Over MCP (this page), the frontier model is the orchestrator: it
> calls the ratchet's **tools** (compile, scaffold, build, validate) and sequences them itself. It does
> NOT run the ratchet's **flows** (`add_file`, `compose`) - those are the local model's chains, invoked
> from the console. To have a frontier model run the flows instead, use the no-setup console hand-off:
> [Frontier prompting](../agents/frontier-prompting.md). Same idea, different seat.

## The surface

- **`tools/list`** advertises the ratchet's **declared tools** (from `tools/manifest.json`), each with
  its authored `inputSchema`.
- **`tools/call`** runs a tool: command/script tools go through the same `ToolRunner` the chains use
  (ratchet root as working directory, `{arg}` substitution, optional stdin, timeout, captured
  stdout/stderr/exit code); a tool whose `kind` is `validate` or `propose` runs the oracle path.
- Unknown tools return a JSON-RPC error (`-32602`); `ping` and the `initialize` handshake behave as a
  client expects.

A frontier orchestrator does the loose inference ("what's the workflow for X?") and calls the matching
tool; the host executes it deterministically and the oracle still gates structured output.

> **Pending:** the KB-browse built-ins (`catalog` / `read_entry`) currently read a root
> `manifest.json`, which the new per-library `kb/manifest.json` model no longer produces, so they are
> not advertised for current ratchets. Repointing them to the kb manifest is planned. Running an
> ratchet's declared tools over MCP works today; browsing the kb over MCP does not yet. Authoring and
> registering a brand-new chain from the frontier model is also roadmap.

## Connecting a client

Smart App Control blocks the bare `.exe`, so point the client at the in-memory launcher
`run-cli.ps1`, not `ratchet.exe`. Use absolute paths.

**Claude Desktop** (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "ratchet": {
      "command": "powershell",
      "args": ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File",
               "C:\\path\\to\\ratchet\\run-cli.ps1", "mcp",
               "C:\\path\\to\\a-ratchet"]
    }
  }
}
```

**Claude Code:**

```
claude mcp add ratchet -- powershell -NoProfile -ExecutionPolicy Bypass -File C:\path\to\ratchet\run-cli.ps1 mcp C:\path\to\a-ratchet
```

## Verifying the server

`powershell -NoProfile -File mcp-smoke.ps1 <ratchet-dir>` drives the handshake over stdio and asserts
the responses, model-free (initialize, tools/list, a real `csc_check` tool call, ping, and the
unknown-tool error). The same trust rule as the console applies: a client calling `tools/call` runs the ratchet's
declared scripts on your machine, so only serve ratchets you trust.
