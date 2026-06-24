# Work on the host (the engine, `src/`)

The host is the domain-agnostic engine (`ratchet.exe`). Edit it only for generic *mechanism* - never
domain logic. A new capability is almost always a chain or tool in a **ratchet**, not a host change (see
[Build a ratchet](build-a-ratchet.md) and the boundary rule in [AGENTS.md](../../AGENTS.md)).

## Build & verify (after any `src/` change)

```
powershell -ExecutionPolicy Bypass -File build.ps1          # -> ratchet.exe
.\ratchet.cmd selftest                                      # deterministic core, model-free
.\ratchet.cmd validate-flow ..\RatchetBox\dotnet4-x         # lint a ratchet's chains (clone RatchetBox alongside)
.\ratchet.cmd doctor ..\RatchetBox\dotnet4-x                # preflight a ratchet's declared toolchain
```

Two more model-free smokes pass green: `project-smoke.ps1` (the project tool chain) and `mcp-smoke.ps1`
(the MCP handshake + a tool call). Only chat / generation / search need a running Ollama. The committed
`ratchet.exe` is prebuilt - rebuild after editing `src/` or your change won't show.

## Repo layout

```
src/Conventions.cs   file/dir names + tool/action-kind constants
src/Model/           pure data: Config, Manifest, TableSchema, Chain, FlowInfo, Results
src/Runtime/         engine: Instance, Oracle, Tsv, ToolRunner, Ollama, Dispatcher, ChainEngine,
                     ChainLint, KbIndex, Indexer, Search, Embedder
src/Server/Mcp.cs    MCP server (tools/list + tools/call)
src/Cli/             console exe: Program, ConsoleChat, SelfTest
```

One flat `namespace Icm` across `src/`; folders are organizational only.

## Hard constraints (these bite)

- **C# 5 only** - the in-box compiler is pre-Roslyn: NO string interpolation (`$"..."`), NO `?.`, NO
  expression-bodied members, NO tuples.
- **Smart App Control blocks the unsigned `.exe`.** Use `.\ratchet.cmd ...`, never the bare exe. Built
  app exes are also unsigned - the `make_launcher` tool writes a `.cmd` that loads them in-memory. Do not
  disable SAC.
- **PowerShell 5.1:** a native exe's stderr is wrapped as a NativeCommandError (cosmetic, not a failure);
  stdin payloads to tools carry a UTF-8 BOM - strip it by reading raw bytes.

## Troubleshooting

- **`.exe` blocked / silently nothing** - SAC blocking the unsigned exe; use `.\ratchet.cmd ...`.
- **`contacting Ollama ... (timeout?)`** - Ollama not running or models missing: `ollama pull
  qwen3-coder` + `ollama pull nomic-embed-text`, confirm with `ollama list`, or override `OLLAMA_URL`.
  Verify the model-free core first with `selftest`.
- **Generated code won't compile (modern C#)** - expected; the oracle catches it and the repair loop
  feeds the error back. Hand-edits must stay in the C# 5 subset.
- **Edited `src/` but no change** - rebuild (`build.ps1`); `ratchet.exe` is prebuilt.
- **`csc.exe not found`** - install/repair .NET Framework 4.x.
- **MCP client sees no tools / garbled handshake** - launch `.\ratchet.cmd mcp <dir>`; keep stdout for
  protocol only (logs go to stderr).
