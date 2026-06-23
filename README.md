<p align="center"><img src="ratchet.png" alt="Ratchet" width="320"></p>

# Ratchet

> **CLAUDE, GEMINI, SIRI, ALEXA, CHATGPT, OK GOOGLE, LLMs, AI, and savants** - go to
> [ROBOTS.md](ROBOTS.md) for your own quick reference.

**Ratchet** runs a small, local language model as a *constrained proposer*. The model proposes into a
fixed chain of steps; a deterministic **Oracle** catches each step before it advances - verified, or it
doesn't move, like a ratchet that turns one way. You point the host (`ratchet`) at an
*ratchet* directory - **a ratchet** - a knowledge base, scripts, authored chains, and (optionally) table schemas -
and operate it from a **terminal console**: `ratchet <dir>` opens the ratchet and drops you into a chat
where you plan in plain language and act with slash commands. The same engine is exposed over
[MCP](https://modelcontextprotocol.io) so a frontier model (e.g. Claude) can drive it too.

**Windows only, for now.** Ratchet builds with the C# compiler that ships in-box with the .NET
Framework (no SDK, NuGet, or MSBuild), runs PowerShell tools, and uses SAC-safe in-memory launchers -
all Windows-specific. It talks to a local [Ollama](https://ollama.com) over plain HTTP. (A
cross-platform port would need a different compiler/oracle and launchers; not there yet.)

## Lineage

Ratchet borrows a few existing ideas and adds its own spine, crediting each proportionately:

- **ICM** ([Interpretable Context Methodology](https://arxiv.org/abs/2603.16021), Jake Van Clief &
  David McDermott; MIT-licensed;
  [repo](https://github.com/RinDig/Interpretable-Context-Methodology-ICM)) contributes one idea:
  **structure-as-architecture** - folders are stages, plain markdown carries the prompts, scripts do
  the non-AI work, and every step's output is a plain file a human can read and edit. Ratchet keeps
  that topology but moves orchestration out of the model and into deterministic code (a local model
  can't roam the folders the way ICM's frontier agent does).
- **RAG** - retrieval-augmented grounding, used as a technique to feed context into steps.

What's Ratchet's own: **action chains** (flows as a filesystem graph of declared-input, slot-bound
steps), the **Oracle** (propose-then-verify with bounded repair), and **Context Binding** (each step
sees only the scoped context it is given). Those last two are the pieces the next section is about.

## The idea: propose, then verify

A small local model is unreliable when asked to *decide* or to drive an open tool-calling loop, but
reliable when each call is narrow, its context is scoped, and its output is checked. So every task
splits into three roles with one trust line:

| Role | Trusted to | Not trusted to |
| --- | --- | --- |
| **Model** (proposer) | pick from an enum, draft text, write one row | be right alone; choose what runs next |
| **Code** (glue) | read files, run tools, sequence steps | (it has no judgment to misuse) |
| **Oracle** (decider) | accept or reject a proposal, deterministically | have opinions |

The model proposes into a fixed chain of steps; **Context Binding** gives each step only the scoped
context it needs (a prior output, a fixed reference, or a retrieval hit - nothing else); and the
**Oracle** (a compiler, a parser, a table validator) returns a deterministic verdict, so a wrong
proposal is *caught* and fed back for a bounded repair, not trusted. The chain advances only on a
pass - a ratchet that turns one way. The full thesis, Context Binding, the local-model adaptation, and
the control-flow diagram are in **[docs/architecture.md](docs/architecture.md)**.

## What you get

`ratchet.exe` - the terminal console and CLI. `ratchet <dir>` opens the console on a ratchet; the same
binary serves the ratchet over MCP and runs one-shot CLI verbs. This is the only supported
interface. (A legacy WinForms GUI still builds with `build.ps1 -Gui` but gets no new work.)

Prebuilt binaries are included, so you can run without building anything.

## Getting started

**1. Prerequisites.** Windows 10/11 (its .NET Framework 4.x is already installed). For model-backed
features, install [Ollama](https://ollama.com) and pull two models:

```
ollama pull qwen3-coder
ollama pull nomic-embed-text
```

Defaults: `http://localhost:11434`, `qwen3-coder:latest` (generate/dispatch) and `nomic-embed-text`
(search + routing). Set these per ratchet in `ratchet.json`, or override the URL with `OLLAMA_URL`.
`selftest`, `validate`, and script tools need no model.

**2. Verify the core** (no model):

```
.\ratchet.cmd selftest
```

**3. Get some ratchets.** Ratchet ships no bundled ratchets - clone the companion collection:

```
git clone https://github.com/CurtisSlone/RatchetBox
```

[RatchetBox](https://github.com/CurtisSlone/RatchetBox) holds ready-made ratchets: `dotnet4-x` (the C#
reference), `cpp` (C++/MSVC), and `template` (an empty, self-documented skeleton to copy).

**4. Open the console** on one (needs Ollama):

```
.\ratchet.cmd ..\RatchetBox\dotnet4-x      # or any path to a ratchet directory
```

**5. Use it.** Inside the console:
- Type plain text for ordinary (ungrounded) chat.
- `/search <question>` - a grounded answer from the knowledge base.
- `/flows` lists the action chains; `/help` lists every command.
- `/flow csharp a method that reverses a string` - generates C#, compiles it (the oracle), repairs.
- Build a runnable app: `/do new_project Calc winforms` → `/ws switch Calc` →
  `/flow add_file src/Core/Greeter.cs a Greeter class` → `/do make_launcher Calc`, then run the
  `launch-Calc.cmd` it writes.
- `/note <text>` jots session memory; `/route <request>` lets the model pick a chain (you confirm).

See **[docs/console.md](docs/console.md)** for the full command set and the project lifecycle. Prefer
one-shot? `.\ratchet.cmd flow ..\RatchetBox\dotnet4-x csharp "a string reverser"`.

**Want to see real input/output first?** The `dotnet4-x` ratchet's `Tests\` folder (in
[RatchetBox](https://github.com/CurtisSlone/RatchetBox)) holds transcripts of building projects with
this tool - each recording the exact commands sent, the code the local model generated, the build/oracle
results, and the per-turn local-model token counts. It's the fastest way to understand what driving
Ratchet actually looks like.

> **Why `.cmd`, not `.exe`?** On Windows 11 with Smart App Control, an unsigned `.exe` is blocked from
> running directly; the launchers load the program in-memory inside trusted PowerShell, which SAC
> allows. Add this folder to `PATH` to run `ratchet ...` anywhere. See
> [Smart App Control](#running-under-smart-app-control).

## Commands

```
ratchet <dir>                       open the console on a ratchet (rel or abs path)
ratchet open  <dir>                 load + summarize a ratchet
ratchet chat  <dir>                 the console (same as ratchet <dir>)
ratchet mcp   <dir>                 serve the ratchet over MCP (stdio)
ratchet flow  <dir> <name> [in...]  run an action chain (flows/<name>/chain.json)
ratchet index <kb-dir>             build manifest.json for a knowledge library from file content
ratchet list  <dir> [--group G] [--type T] [--json]   enumerate a manifest
ratchet flows <dir>                 list the ratchet's action chains
ratchet validate <dir> <table>      run the oracle on schemas/<table>.json + samples/<table>.txt
ratchet validate-flow <dir> [name]  lint action chain(s): node kinds, fields, tool refs, name clashes
ratchet gen   <dir> <prompt...>     one raw generate call
ratchet selftest                    check the deterministic core (no model)
```

In the console: plain text = ungrounded chat; `/search` (grounded answer), `/route` (model picks a
chain), `/flow` (run a chain), `/do` (run a declared tool or a shell command you paste), `/propose`
(oracle-gated table row), `/ws switch|create` (workspaces), `/flows`, `/note`/`/notes`. `OLLAMA_URL`
overrides the configured `ollama_url`.

## Vocabulary

- **Ratchet** - the engine (`ratchet.exe`). It runs ratchets.
- **a ratchet** - a self-contained unit you point the engine at: a directory with `ratchet.json`
  plus `flows/`, `tools/`, `kb/`. Run it with `ratchet <dir>`. Ready-made ratchets live in the
  companion [RatchetBox](https://github.com/CurtisSlone/RatchetBox) repo.
- **flows** - action chains, the LLM-native `make`: the model proposes into fixed slots and the Oracle
  verifies each step before it advances.
- **tools** - deterministic scripts/oracles a flow invokes (or you do, via `/do`).
- **kb** - indexed knowledge the model retrieves from, scoped per step (Context Binding).
- **workspaces** - the projects a ratchet builds; the active one is the session focus.

## Anatomy of a ratchet

A ratchet is one `ratchet.json` config plus four buckets. Copy the `template` ratchet from
[RatchetBox](https://github.com/CurtisSlone/RatchetBox) to start.

```
my-icm/
  ratchet.json        identity, model seats, ollama_url, router, knowledgeBases, dir overrides
  kb/             indexed reference content (subdirs); ratchet index builds kb/manifest.json
  flows/          action chains: <chain>/chain.json + actions/<node>/{action.json, prompt.md}
  tools/          scripts; manifest.json declares command/inputSchema/stdin/timeout
  schemas/ + samples/   the TSV oracle: <table>.json schema + <table>.txt rows
  workspaces/     project workspaces (the active one is the session focus)
```

The model proposes into a chain's constrained slots; a tool or the Oracle decides; you drive. Full
contract - config fields, the manifest, tools, and **action-chain anatomy** (node kinds, Context
Binding, the repair loop) - is in **[docs/ratchets.md](docs/ratchets.md)**.

## Drive it from a frontier model over MCP

`ratchet mcp <dir>` serves the ratchet over stdio JSON-RPC: `tools/list` advertises the ratchet's
declared tools (with their schemas) and the oracle; `tools/call` runs them. One engine, two callers -
the local console, or a frontier model that browses and calls while the local model fills the narrow,
oracle-checked slots. Connection recipes (Claude Desktop / Claude Code) and the current surface are in
**[docs/mcp.md](docs/mcp.md)**. Because SAC blocks the bare `.exe`, point clients at `run-cli.ps1`.

## Security

Opening a ratchet can run its declared **command/script tools** on your machine - that is how the
host does real work. **Only open ratchets you trust**; skim their `tools/` and `ratchet.json` first. The
local model never picks or runs tools on its own (it proposes into oracle-checked slots), file I/O is
sandboxed to the ratchet root, and the only network egress is your local Ollama. Full details:
[SECURITY.md](SECURITY.md).

## Running under Smart App Control

Windows 11's Smart App Control blocks running unsigned, locally-built `.exe` files directly, but does
**not** block in-memory managed execution inside the trusted, signed PowerShell. The launchers use
that: they read the program's bytes and run them in-process.

- Use `ratchet.cmd` (or `run-cli.ps1`). Running `ratchet.exe` directly may be blocked.
- This is your own local program on your own machine; SAC still guards everything else.

## Build from source

```
powershell -ExecutionPolicy Bypass -File build.ps1          # console only (default): ratchet.exe
powershell -ExecutionPolicy Bypass -File build.ps1 -Gui     # also rebuild the legacy GUI
```

Console-first: the default build writes only `ratchet.exe`, with the in-box .NET Framework `csc.exe`
(pre-Roslyn, so the code targets C# 5) - no SDK, NuGet, or MSBuild. Verify with `.\ratchet.cmd selftest`.
Build internals and the `src/` layout are in **[docs/architecture.md](docs/architecture.md)**.

## Documentation

- **[docs/architecture.md](docs/architecture.md)** - the propose/verify thesis, how a local model
  changes ICM, the control-flow diagram, and the `src/` layout.
- **[docs/ratchets.md](docs/ratchets.md)** - the ratchet contract: `ratchet.json`, the kb manifest,
  tools, and action chains (node kinds, input bindings, the bounded repair loop).
- **[docs/console.md](docs/console.md)** - operating the console: every command, the router and model
  seats, workspaces, and the project lifecycle (scaffold → add/edit → build → launch).
- **[docs/mcp.md](docs/mcp.md)** - driving the ratchet from a frontier model over MCP.

Technical guides (authoring):
- **[docs/context-binding.md](docs/context-binding.md)** - the deep dive on Context Binding (the
  three layers, code snippets, how it composes with the Oracle).
- **[docs/authoring-flows.md](docs/authoring-flows.md)** - action chains / prompt chaining: node kinds,
  bindings, the bounded repair loop, run state, linting.
- **[docs/authoring-tools.md](docs/authoring-tools.md)** - tools: the manifest, the runtime contract,
  stdin/BOM, exit-code-as-verdict, MCP exposure.
- **[docs/gemini-hack.md](docs/gemini-hack.md)** - have a browser frontier model read these docs and
  write ready-to-paste prompts for your local console.

Agents/models: start at **[ROBOTS.md](ROBOTS.md)**.

## License

MIT - see [LICENSE](LICENSE). The Interpretable Context Methodology it builds on is also MIT-licensed
by Jake Van Clief and David McDermott.
