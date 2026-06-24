# Edit a tool (agent playbook)

Goal: add a script the host runs, or change one. This is the fast path; the format contract is
[Author tools](../how-to/author-tools.md).

## Two steps

1. Drop the script at `tools/<name>.ps1`.
2. Declare it in `tools/manifest.json`:

```json
{ "name": "csc_check",
  "description": "Compile a C# file with csc; OK or diagnostics.",
  "command": ["powershell","-NoProfile","-ExecutionPolicy","Bypass","-File","tools/csc_check.ps1"],
  "inputSchema": { "type":"object", "properties": { "code": { "type":"string" } }, "required":["code"] },
  "stdin": "code", "timeout": 60 }
```

## The contract

- The host runs `command` with the RATCHET ROOT as the working directory.
- `{arg}` in `command` is filled from the call's arguments (argv, no shell - no injection).
- If `"stdin"` names an input, that one is piped to stdin instead (use for large payloads like source).
  It arrives with a UTF-8 BOM - strip it by reading raw bytes.
- The EXIT CODE is the oracle verdict: 0 = `on_success`, non-zero = `on_failure`.
- A bare `tools/*.ps1` with no manifest entry is still callable by name (zero-arg).

## Verify

```
ratchet doctor <ratchet>                  # if the tool backs a `requirements` check
ratchet flow <ratchet> <chain> ...        # exercise a chain whose action node calls it
```

Or call it directly in the console: `/do <name> [arg]`. Or run `tools/<name>.ps1` from the ratchet root.

## Gotchas (PowerShell 5.1)

- Do NOT pipe a native exe's stderr through `2>&1` under `ErrorActionPreference=Stop` - it wraps stderr
  as a terminating error.
- `>` writes UTF-16; when another tool will read the file, capture the output and
  `Set-Content -Encoding ascii`.
- A `[type]`-constrained param silently coerces later same-named assignments - rename if you reassign.
