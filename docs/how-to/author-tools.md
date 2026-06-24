# Authoring tools

A tool is a script (or external program) the host runs deterministically. Tools are where a ratchet
does real work: compile, parse, scaffold, build, validate, launch. The model never invents a tool's
command - it only fills declared arguments. This is the technical how-to; tools are invoked by
`action` nodes ([authoring-flows.md](author-flows.md)) or directly with `/do`.

## Where tools live

```
tools/
  manifest.json        declares each tool: command, inputSchema, stdin, timeout
  <name>.ps1           the script(s)
```

A tool declared in `manifest.json` is invoked by its `name`. A bare `tools/<name>.ps1` with **no**
manifest entry is still callable by name as a zero-argument convention script.

## The declaration

```json
{
  "tools": [
    {
      "name": "csc_check",
      "description": "Compile a C# file with the in-box csc at langversion 5; report OK or diagnostics.",
      "command": ["powershell","-NoProfile","-ExecutionPolicy","Bypass","-File","tools/csc_check.ps1"],
      "inputSchema": { "type": "object", "properties": { "code": { "type": "string" } }, "required": ["code"] },
      "stdin": "code",
      "timeout": 60
    }
  ]
}
```

- **`name`** - unique within the ratchet (the namespace check enforces it; chain + tool names share
  one flat namespace).
- **`description`** - what a caller (a frontier model over MCP, or you) sees.
- **`command`** - the argv array to run. **Not a shell string** - so argument values present no
  shell-injection surface. A `{placeholder}` token is substituted from the call's arguments.
- **`inputSchema`** - JSON Schema for the arguments. Used to validate calls and advertised over MCP.
- **`stdin`** - optional: the name of one argument to pipe to standard input instead of argv. Use for
  **large payloads** (source code, file contents) that would be unwieldy or limited as a command-line
  argument.
- **`timeout`** - seconds; the host kills the process and reports a timeout past it.

## The runtime contract

When the host runs a tool it:

1. sets the **working directory to the ratchet root** (so `tools/...` and relative paths resolve);
2. substitutes each `{arg}` in the `command` argv from the call's arguments;
3. if `stdin` is set, pipes that argument to standard input instead of argv;
4. enforces `timeout`, and captures **stdout, stderr, and the exit code**;
5. routes on the **exit code**: `0` = success (`on_success` from an `action` node), non-zero = failure
   (`on_failure`). The captured output is what a failure binds into a repair node.

So the rule for writing a tool: **do the work, print any diagnostics, and exit 0 on success or
non-zero on failure.** The exit code is the oracle verdict; stdout/stderr is the feedback the model
gets on a retry.

```powershell
# tools/csc_check.ps1 (shape) - read code on stdin, compile, exit with the verdict
$code = [Console]::In.ReadToEnd()
$code = $code -replace "^\xEF\xBB\xBF", ""     # strip the UTF-8 BOM stdin payloads carry
# ... write $code to a temp file, run csc, print diagnostics ...
if ($LASTEXITCODE -ne 0) { exit 1 }            # non-zero -> on_failure (the repair gets the diagnostics)
"OK"; exit 0
```

> **The stdin BOM.** A payload piped to stdin arrives with a leading UTF-8 BOM. A stdin-reading tool
> must strip it (as above), or the first token (e.g. `using`) is corrupted.

## Invoking a tool

- **From a chain** - an `action` node names it in `tool` and templates its `body` from bound slots:

  ```jsonc
  { "id": "x.check", "kind": "action", "tool": "csc_check",
    "inputs": [ { "from": "x.generate", "path": ".", "as": "code" } ],
    "body": { "code": "{{ code }}" },
    "on_success": "x.done", "on_failure": "x.fix" }
  ```

- **From the console** - `/do <name> [arg]` runs a declared tool by name (the remaining text maps to
  its argument / stdin). `/do <anything else>` runs as a pasted shell command instead.
- **Over MCP** - `tools/list` advertises every declared tool with its `inputSchema`; `tools/call`
  runs it through this same contract. (See [mcp.md](drive-over-mcp.md).)

## Patterns from the C# reference ratchet

`RatchetBox/dotnet4-x/tools/` shows the common shapes:

- **Oracles** (`csc_check`, `ps_parse`, `csc_winforms`) - read source on stdin, exit with the
  compile/parse verdict. These are what `action` nodes call.
- **Project tools** (`new_project`, `stage_and_build`, `build_project`, `read_file`, `read_project`,
  `register_file`) - operate on a workspace path passed as an argument; `stage_and_build` is the
  whole-project compile oracle the `add_file`/`edit_file` chains use.
- **Launcher** (`make_launcher` + `run_app`) - `make_launcher` writes a SAC-safe `.cmd` that loads a
  built (unsigned) exe in-memory via `run_app`. Launching is domain-specific, so it is a tool, invoked
  with `/do make_launcher <name>` - not a host command.

## Security

A tool runs with **your privileges** - that is the trust boundary, and why you only open ratchets you
trust ([SECURITY.md](../../SECURITY.md)). Context Binding controls *what data flows into* a tool call
(arguments come from declared, origin-tagged slots, not free model output); it does not sandbox the
tool itself. Keep `command` an argv array, validate inputs with `inputSchema`, and never build a tool
that `Invoke-Expression`s an argument.
