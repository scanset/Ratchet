# ROBOTS.md - orientation for AI agents

You are an AI agent (any model) working on or with **Ratchet**. This is your quick reference. Humans:
see [README.md](README.md). Deep dives in [docs/](docs/): `architecture`, `ratchets`, `console`,
`mcp`, plus the technical guides `context-binding`, `authoring-flows`, `authoring-tools`, and the
`gemini-hack`.

## The 5 Ws

- **What.** A Windows-native host that runs a small **local** model as a *constrained proposer*: the
  model proposes into a fixed chain of steps, a deterministic **Oracle** (a compiler, a parser, a
  table validator) accepts or rejects each step, and the chain advances only on a pass. The host is a
  domain-agnostic harness; all domain logic lives in the *ratchets* it loads.
- **Why.** A weak local model is unreliable as a decider or in an open tool loop. Structure plus the
  Oracle make it reliable: it only ever proposes into a narrow, scoped, checked slot.
- **Who.** A human operator drives from the console (or a frontier model drives over MCP). The local
  model never picks actions; it fills slots. You, the agent reading this, are usually editing the host
  (`src/`) or authoring a ratchet (cloned from RatchetBox).
- **Where.** **Windows only, for now.** It builds with the in-box .NET Framework `csc.exe` (no
  SDK/NuGet/MSBuild), runs PowerShell tools, and uses SAC-safe launchers - all Windows-specific. Talks
  to a local [Ollama](https://ollama.com). Ratchets live in the companion RatchetBox repo, or anywhere on disk.
- **When.** Use Ratchet for bounded, *verifiable* generation - code that must compile, a row that must
  validate, an edit that must still build. Not for open-ended agentic roaming.

Two ideas are Ratchet's own; know them by name:
- **The Oracle** - deterministic verify-then-advance with bounded repair. Oracle-pass means "won't
  break," not "is correct."
- **Context Binding** - each chain node sees only its declared, scoped inputs (a prior output, a fixed
  `ref`, or a `search` hit), never a cumulative tape. Isolation is the biggest reliability lever.

(Lineage: structure-as-architecture is from ICM; RAG is a technique; the action-chain + Context
Binding model is Ratchet's. Don't call Ratchet "just ICM.")

## Vocabulary

- **Ratchet** - the engine (`ratchet.exe`). It runs ratchets.
- **a ratchet** - a self-contained unit: a directory with `ratchet.json` plus `flows/`, `tools/`,
  `kb/`. You point the engine at one: `ratchet <dir>`.
- **flows** - action chains (the LLM-native `make`): model proposes into fixed slots, Oracle verifies.
- **tools** - deterministic scripts/oracles a flow invokes (or you, via `/do`).
- **kb** - indexed knowledge retrieved per step (Context Binding).
- **workspaces** - the projects a ratchet builds; the active one is `$workspace`.

## The prime directive: do not blur the host/ratchet boundary

The binary in `src/` is a **generic harness** - never put domain logic or a specific chain/tool/kb
name in it.

- **Host (harness):** the chain engine (`Runtime/ChainEngine.cs`), dispatcher (`Dispatcher.cs`), the
  oracle *mechanism* (`Oracle.cs` TSV validator), search/embed (`KbIndex`/`Search`/`Embedder`), MCP
  (`Server/Mcp.cs`), and the generic verbs (`/search`, `/route`, `/flow`, `/do`, `/ws`, `/propose`).
- **Ratchet (domain):** every concrete chain, tool/script, knowledge base, and table schema. C# /
  PowerShell / compile / launch specifics live ONLY here.

To add a capability, add a **chain or tool in a ratchet** and invoke it generically - do not add a
host command. New chains/tools need no rebuild (discovered at runtime); only engine changes do.

## Build & verify (after any src change)

```
powershell -ExecutionPolicy Bypass -File build.ps1          # console only (default): ratchet.exe
powershell -ExecutionPolicy Bypass -File build.ps1 -Gui     # also the legacy ratchet-gui.exe
.\ratchet.cmd selftest                                       # deterministic core, model-free
.\ratchet.cmd validate-flow ..\RatchetBox\dotnet4-x          # lint a ratchet's chains (clone RatchetBox alongside)
.\ratchet.cmd doctor ..\RatchetBox\dotnet4-x                 # preflight a ratchet's declared toolchain
```

Only chat / generation / search need a running Ollama. Model-free smokes: `project-smoke.ps1` (the
project tool chain) and `mcp-smoke.ps1` (the MCP handshake + a tool call) - both pass green.

## Hard constraints (these bite)

- **C# 5 only** (the in-box compiler is pre-Roslyn): NO string interpolation (`$"..."`), NO `?.`, NO
  expression-bodied members, NO tuples. A ratchet's prompts forbid `$"..."` because the local model
  reaches for it and the compiler rejects it.
- **Smart App Control blocks the unsigned `.exe`.** Use `.\ratchet.cmd ...`, never the bare exe. Built
  app exes are also unsigned - the `make_launcher` tool writes a `.cmd` that loads them in-memory. Do
  not disable SAC.
- **One flat `namespace Icm`** in `src/`; folders are organizational. **PowerShell 5.1:** a native
  exe's stderr is wrapped as a NativeCommandError (cosmetic); stdin payloads carry a UTF-8 BOM.

## Repo layout

```
src/Conventions.cs   file/dir names + tool/action-kind constants
src/Model/           pure data: Config, Manifest, TableSchema, Chain, FlowInfo, Results
src/Runtime/         engine: Instance, Oracle, Tsv, ToolRunner, Ollama, Dispatcher, ChainEngine,
                     ChainLint, KbIndex, Indexer, Search, Embedder
src/Server/Mcp.cs    MCP server (tools/list + tools/call)
src/Cli/             console exe: Program, ConsoleChat, SelfTest
src/Gui/             legacy WinForms exe (built only with -Gui)
docs/                deep dives: architecture, ratchets, console, mcp, context-binding,
                     authoring-flows, authoring-tools, gemini-hack
```

Ratchet ships no bundled ratchets - ready-made ones (the C# `dotnet4-x`, `cpp`, and the `template`
skeleton) live in the companion repo **[RatchetBox](https://github.com/CurtisSlone/RatchetBox)**. Clone
it alongside this repo and point `ratchet` at a ratchet there.

---

# Authoring a ratchet

A ratchet is `ratchet.json` + four buckets (`kb/`, `flows/`, `tools/`, `schemas/`+`samples/`) +
`workspaces/`. Copy `template` to start. Full contract: [docs/ratchets.md](docs/ratchets.md).

## Knowledge bases: create, index, add

**Create** - one topic per markdown file under `kb/<subdir>/`. The **first heading** becomes the
routing title; the **first prose line** becomes the summary; keywords are the top terms. Make those two
lines sharp - they are what routing sees. `README.md` folder guides are skipped.

**Index** - build the routing index from content (deterministic, no model):

```
.\ratchet.cmd index <ratchet-dir>\kb      # writes kb/manifest.json
```

Re-run after adding or editing entries.

**Add another library** - register it in `ratchet.json` (a name + a path that may point anywhere, plus
an optional default). It is read fresh on each search.

```json
"knowledgeBases": [
  { "name": "kb",  "path": "kb", "default": true },
  { "name": "docs", "path": "C:\\path\\to\\external-docs" }
]
```

**Use** - `/search [name|path] <query>` answers from a library; a chain grounds on one via a `search`
or `ref` input binding (below). Retrieval is BM25 by default; the embedder re-ranks when an `embed`
seat is set (cached in `.index/`).

## Flows: create an action chain

A chain is a directory; the chain is the orchestrator (the model proposes inside nodes, never picks
the next step).

```
flows/<chain>/
  chain.json                       # { id, version, summary, entry, inputs?, budgets, nodes[] }
  actions/<node>/action.json       # { id, kind, inputs[], ...edges }
  actions/<node>/prompt.md         # model instructions ({{ slot }}) - for generate / ai_branch
```

**Node kinds:** `action` (run a tool, route on exit code via `on_success`/`on_failure`), `generate`
(free text from `prompt.md`), `ai_branch` (slots -> prompt -> enum decision -> `transitions`),
`summarizer` (deterministic transform), `exit` (terminal `outcome`).

**Context Binding** - a node sees ONLY its declared `inputs`, each into a named `as` slot, capped with
`max_chars`. Sources: `from` (a prior node, or `$input` / `$workspace` / a chain-declared `inputs`
slot), `ref` (a fixed kb entry, always present), `search` (a templated kb query). Deep dive:
[docs/context-binding.md](docs/context-binding.md).

The canonical shape is **generate -> check -> repair** (unrolled, so it lints clean and terminates):

```jsonc
// chain.json
{ "id": "draft", "version": "1.0.0", "entry": "draft.generate",
  "summary": "Generate, verify with the oracle, repair once.",
  "budgets": { "max_steps": 10, "max_total_tokens": 12000, "max_wallclock_seconds": 600 },
  "nodes": ["draft.generate","draft.check","draft.fix","draft.recheck","draft.done","draft.fail"] }

// actions/generate/action.json
{ "id": "draft.generate", "kind": "generate", "prompt": "./prompt.md",
  "inputs": [ { "from": "$input", "path": ".", "as": "task" },
              { "search": "kb", "query": "{{ task }}", "k": 2, "as": "refs", "max_chars": 2000 } ],
  "on_success": "draft.check", "on_failure": "draft.fail" }

// actions/check/action.json   (the oracle)
{ "id": "draft.check", "kind": "action", "tool": "example_check",
  "inputs": [ { "from": "draft.generate", "path": ".", "as": "code" } ],
  "body": { "code": "{{ code }}" },
  "on_success": "draft.done", "on_failure": "draft.fix" }
```

`fix` is a `generate` node fed the oracle's errors + the previous attempt; `recheck` re-runs the tool.
To repair twice, add a `fix2`/`recheck2` pair and point `recheck.on_failure` at `fix2`. **Lint**:
`.\ratchet.cmd validate-flow <dir> [name]` (checks node kinds, fields, unknown tools, reachability,
name clashes). Working examples: `template/flows/draft`,
`dotnet4-x/flows/{csharp,add_file,edit_file}`. **Deep dive:**
[docs/authoring-flows.md](docs/authoring-flows.md).

## Tools: create a script the host runs

Drop a script in `tools/` and declare it in `tools/manifest.json`. A bare `tools/*.ps1` with no entry
is callable by name (zero-arg).

```json
{ "tools": [
  { "name": "csc_check",
    "description": "Compile a C# file with the in-box csc; report OK or diagnostics.",
    "command": ["powershell","-NoProfile","-ExecutionPolicy","Bypass","-File","tools/csc_check.ps1"],
    "inputSchema": { "type":"object", "properties": { "code": { "type":"string" } }, "required":["code"] },
    "stdin": "code", "timeout": 60 }
] }
```

Contract: the host runs the command with the **ratchet root as cwd**, substitutes `{arg}` into the
argv from the call's arguments (argv array, so no shell-injection), pipes one arg to stdin instead when
`"stdin"` names it (use for large payloads like source - it arrives with a BOM, strip it), enforces
`timeout` seconds, and treats the **exit code as the oracle verdict** (0 = on_success). The author
writes the command; the caller only fills declared arguments. Call directly with `/do <name> [arg]`,
or from an `action` node's `tool`. **Deep dive:** [docs/authoring-tools.md](docs/authoring-tools.md).

## Workspaces: create the work area

`workspaces/` holds project workspaces; the **active** one is the session focus, injected into chains
as `$workspace`.

```
/ws create <name>      # make workspaces/<name> (+ project.json) and switch to it
/ws switch <name>      # activate an existing one
```

For a buildable project, a domain tool scaffolds the structure - in the C# ratchet,
`/do new_project <name> [console|winforms]` writes `workspaces/<name>/` with `src/`, `response.rsp`,
`build.ps1`, `project.json`. Then project chains (`add_file`, `edit_file`) operate on the active
workspace: generate -> stage -> build the whole project (oracle) -> repair -> record. Relocate the
container with `workspacesDir` in `ratchet.json` (it may live outside the sandbox).

---

# Driving Ratchet (quick reference)

## From the console (`ratchet <dir>`)

Plain text = ungrounded chat. Commands: `/search [src] <q>` (grounded answer), `/route <request>`
(model picks a chain; you confirm), `/flow <name> <input>` (run a chain), `/do <tool [arg] | command>`
(run a declared tool, or a shell command you paste), `/propose <desc>` (oracle-gated row),
`/ws switch|create`, `/flows`, `/note`/`/notes`, `/help`. One-shot, non-interactive:
`ratchet flow <dir> <name> [input...]`, `ratchet open <dir>`, `ratchet gen <dir> <prompt>`.

## Over MCP (a frontier model drives)

```
.\ratchet.cmd mcp <dir>     # stdio JSON-RPC: tools/list advertises the ratchet's tools; tools/call runs them
```

Point MCP clients at `run-cli.ps1` (not the bare exe - SAC). Same engine the console uses: the
frontier model browses + calls while the local model fills the oracle-checked slots; the ratchet's
declared tools are the blast radius. Connection recipes (Claude Desktop / Claude Code) are in
[docs/mcp.md](docs/mcp.md). (Pending: KB-browse built-ins `catalog`/`read_entry` still read a retired
root manifest; calling declared tools works today.)

## Let a frontier model write your prompts (the Gemini hack)

No MCP setup needed: point a browser frontier model (Gemini in Chrome, ChatGPT, Claude) at this repo's
docs + a ratchet's `kb`/`flows`/`tools` manifests and have it author ready-to-paste console prompts -
frontier-quality planning, local verified execution, by copy-paste. Full recipe (and the prompt to
give it): [docs/gemini-hack.md](docs/gemini-hack.md).

**What a frontier model reads to write exact commands + prompts** (don't invent commands not in these):

- **`dotnet4-x/Tests/WINFORMS_TEST_LOG.md`** - real console traces: exact inputs/outputs, proof
  that Ratchet keeps no chat tape (state lives on disk in `project.json` + the source tree + `runs/`,
  so commands are discrete), and the concrete **current** command syntax. (`COMPLEX_TEST_LOG.md` beside
  it is a *pre-rework* transcript - it shows the retired `/new`//`add`//`build`; use the verbs below.)
- **`a ratchet/SYSTEM.md`** (indexed via `kb/manifest.json`) - the hard limits: pre-Roslyn
  **C# 5** (no string interpolation, expression-bodied members, `?.`, tuples). Tailor `add_file`/
  `edit_file` prompts to avoid C# 6+ so the `csc` oracle doesn't trip.
- **`a ratchet/STRUCTURE.md`** - the layout: `tools/` (automation) + `flows/` (chains).
- **`flows/*/chain.json` + `tools/manifest.json`** - the chains and tools that actually exist.

Current "start a new app" command set (not the retired `/new`//`add`//`build`): `/do new_project <name>
[console|winforms]` -> `/ws switch <name>` -> `/flow add_file <path> <desc>` -> `/flow edit_file <path>
<desc>` -> `/do build_project <name>` -> `/do make_launcher <name>`.

## See real input/output: the Tests/ dir

`dotnet4-x/Tests/` holds transcripts of building real projects with Ratchet:
- **`COMPLEX_TEST_LOG.md`** - multi-file C# projects (interface + driver + model + entry).
- **`WINFORMS_TEST_LOG.md`** - runnable WinForms apps, increasing complexity, incl. a failure-and-fix.

Each records the exact commands sent, the code the local model generated, the build/oracle results, and
per-turn local-model token counts. It is the fastest way to see what driving Ratchet actually looks
like - read it before authoring or demoing.

---

## Troubleshooting (real failures + fixes)

- **`.exe` blocked / silently nothing.** SAC blocking the unsigned exe - use `.\ratchet.cmd ...`.
- **`contacting Ollama ... (timeout?)`.** Ollama not running / models missing: `ollama pull
  qwen3-coder` + `ollama pull nomic-embed-text`; confirm `ollama list`; override `OLLAMA_URL`. Verify
  the model-free core first with `selftest`.
- **Generated code won't compile (modern C#).** Expected - the oracle catches it and the repair loop
  feeds the error back. Hand-edits must stay in the C# 5 subset.
- **Edited `src/` but no change.** Rebuild (`build.ps1`); the committed `ratchet.exe` is prebuilt.
- **`csc.exe not found`.** Install/repair .NET Framework 4.x.
- **MCP client sees no tools / garbled handshake.** Launch `.\ratchet.cmd mcp <dir>`; keep **stdout**
  for protocol only (logs to stderr).

## Style

Plain and grounded: no emoji, no em dashes, no hype. C# 5 only. Verify, don't assert - compile-/run-
verify and show evidence; "compiles" is not "behavior-correct."
