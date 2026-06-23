# Building a ratchet

A ratchet is data Ratchet loads - the host code never changes per domain. It is one
`ratchet.json` config plus four buckets. Copy `examples\template` (a complete, self-documented skeleton
with a working example chain) and replace its contents.

This page is the contract overview. For depth, see the technical guides:
[authoring-flows.md](authoring-flows.md) (action chains), [authoring-tools.md](authoring-tools.md)
(tools), and [context-binding.md](context-binding.md) (how each step gets its scoped context).

```
my-ratchet/
  ratchet.json        identity, model seats, ollama_url, router, knowledgeBases, optional dir overrides
  kb/             indexed reference content (organize by subdir); ratchet index builds kb/manifest.json
  flows/          action chains: <chain>/chain.json + actions/<node>/{action.json, prompt.md}
  tools/          scripts the host runs; manifest.json declares how to invoke each
  schemas/ + samples/   the TSV oracle: <table>.json column schema + <table>.txt reference rows
  workspaces/     project workspaces (one subdir per project; the active one is the session focus)
```

`.index/` (search cache) and `runs/` (chain run-state) are generated at runtime - gitignore them.

## ratchet.json

The launch config. Open with `ratchet <dir>` (finds `ratchet.json`) or `ratchet <path-to-ratchet.json>`.

```json
{
  "name": "my-ratchet",
  "domain": "one line: what this ratchet is for",
  "models": { "generate": "qwen3-coder:latest", "dispatch": "qwen3-coder:latest", "embed": "nomic-embed-text" },
  "ollama_url": "http://localhost:11434",
  "router": { "autorun": "confirm" },
  "knowledgeBases": [ { "name": "kb", "path": "kb", "default": true } ]
}
```

- **`models`** - named seats. `generate` writes text; `dispatch` is the small classify/route call
  (omit to reuse `generate`); `embed` powers search + narrows routing candidates (optional). Flat
  `model` / `embed_model` are accepted as fallbacks.
- **`router.autorun`** - `confirm` (default; propose + `y/n`), `on` (auto-run high-confidence), or
  `off`. Governs `/route`.
- **`knowledgeBases`** - one or more searchable libraries, each a name + a path (which may point
  anywhere on disk) + an optional `default`. A conventional `kb/` is picked up automatically.
- **`workdir`** and dir overrides (`flowsDir`, `toolsDir`, `schemasDir`, `samplesDir`,
  `workspacesDir`) - optional. `workdir` is the write/sandbox root (default: the config's folder);
  each bucket defaults under it, or set a field to point elsewhere (e.g. a shared base). `workspacesDir`
  is a write location and may live outside the sandbox.

A directory with no `ratchet.json` still opens with sensible defaults plus its `kb/`.

### requirements (preflight)

Declare the tools the ratchet needs and validate them with `ratchet doctor <dir>`. Each entry is a
`name` plus exactly one generic check the host knows how to run; optional `required` (default `true`)
and `hint`. `doctor` prints `[ok]`/`[warn]`/`[MISS]` and exits non-zero if a required check fails.

```json
"requirements": [
  { "name": "Ollama",  "http": "http://localhost:11434/api/tags", "hint": "start Ollama" },
  { "name": "model",   "model": "qwen3-coder:latest", "hint": "ollama pull qwen3-coder" },
  { "name": "git",     "exe": "git", "required": false },
  { "name": "SDK",     "file": "C:\\path\\that\\must\\exist" },
  { "name": "KB kb",   "kb": "kb" },
  { "name": "toolchain", "tool": "doctor_cl" }
]
```

Check types: **`exe`** (on PATH), **`file`** (path exists), **`env`** (var set), **`http`** (GET 2xx),
**`model`** (Ollama at `ollama_url` has it), **`kb`** (that knowledgeBase's manifest exists and matches
its file count), and **`tool`** (run a declared tool; exit 0 = pass). Use `tool` for domain-specific
probes the generic checks can't express (e.g. "is the compiler reachable through its env script").

## kb/ (indexed content)

One topic per markdown file, organized by subdir (`kb/reference/`, `kb/patterns/`, ...). Run
`ratchet index kb` to build `kb/manifest.json`, the routing index - title, summary, and keywords are
extracted from each file's content **deterministically** (no LLM): the title is the first heading, the
summary the first prose line, keywords the top terms. `README.md` folder guides are skipped. Chains
ground on the kb via `search`/`ref` input bindings; `/search kb <query>` answers from it.

**Grounding mechanics.** Retrieval is **BM25 by default** (the floor, and the graceful fallback when
no `embed` seat is set or Ollama is down); when an embedder is available it re-ranks the BM25
shortlist, with vectors cached per-ratchet in `.index/`. Semantic re-rank is most worth it at
"pick the right X" gates (e.g. "decouple object creation" → Factory), which keyword search misses;
BM25 carries the hot paths.

**An honest tradeoff.** ICM's bet is that curated structure compensates for a weak model - a
hand-written `summary`/`keywords` gave the 30B model precise routing. Generating the manifest from
content trades some of that curated precision for plain retrieval quality. Mitigate it the way the
reference ratchets do: keep each kb **small and focused**, leave the `embed` seat on for curated
libraries, and write a sharp first heading + first line per file (that is what routing sees).

## tools/

Scripts the host runs. Declare each in `tools/manifest.json`; a bare `tools/*.ps1` with no entry is
still callable by name (zero-arg).

```json
{
  "tools": [
    {
      "name": "csc_check",
      "description": "Compile a C# file with the in-box csc and report OK or the diagnostics.",
      "command": ["powershell","-NoProfile","-ExecutionPolicy","Bypass","-File","tools/csc_check.ps1"],
      "inputSchema": { "type": "object", "properties": { "code": { "type": "string" } }, "required": ["code"] },
      "stdin": "code",
      "timeout": 60
    }
  ]
}
```

The host runs the command with the **ratchet root as the working directory** (so `tools/...` resolves),
substitutes `{arg}` placeholders into the argv from the call's arguments, optionally pipes one argument
to stdin instead (`"stdin": "argname"` - use for large payloads like source), enforces `timeout`
(seconds), and captures stdout / stderr / exit code. The command is an argv array (no shell), so
there is no shell-injection surface. The author writes the command; the caller only fills declared
arguments. (A stdin payload arrives with a leading UTF-8 BOM; a stdin-reading tool should strip it.)

## schemas/ + samples/ (the TSV oracle)

`schemas/<table>.json` declares a table's columns (type / required / min / max / enum, and `ref`
columns that must point into another table); `samples/<table>.txt` is the tab-separated data (first
line is the header). `ratchet validate <dir> <table>` runs the oracle: column count (the classic
"a tab got added or dropped" corruption), types, ranges, enum membership, and cross-table references.
The same check gates the `/propose` path with bounded repair.

## flows/ (action chains)

A chain is the orchestrator - the model proposes inside nodes but never decides what runs next. Each
chain is a directory:

```
flows/<chain>/
  chain.json          { id, version, summary, entry, inputs?, budgets, nodes[] }
  actions/<node>/
    action.json       { id, kind, inputs[], ...edges }
    prompt.md         the model's instructions ({{ slot }} templating) - for generate / ai_branch
```

`chain.json` declares the entry node, the node list, optional `inputs` (named slots split from the
run's input), and `budgets` (`max_steps`, `max_total_tokens`, `max_wallclock_seconds`). Run state is
written to `runs/<id>/` (`meta.json`, one `step-NNN.json` per node, `outcome.json`).

### Node kinds

- **`action`** - run a `tools/` tool, then route on its exit code: `on_success` / `on_failure`. The
  deterministic oracle step.
- **`generate`** - free text via the generate seat, from a `prompt.md`. The ICM proposer.
- **`ai_branch`** - fill slots, prompt the model for an enum decision, and route via `transitions`.
- **`summarizer`** - a deterministic transform of prior outputs.
- **`exit`** - a terminal `outcome` (e.g. `success`, or an `aborted: ...` reason).

### Context binding (input slots)

**Context Binding** is how a node gets its context: it sees **only** its declared `inputs`, bound into
named slots - never a cumulative tape, prior prompts, or engine state. That isolation is a core
reliability mechanism (small, clean, known context per call); put reference/retrieval grounding on the
few nodes that need it, not everywhere. Each binding names a source and an `as` slot:

- **`from`** - a prior node's output, or a run seed: `$input` (the whole input), `$workspace` (the
  active workspace path), or a chain-declared `inputs` slot.
- **`ref`** - a fixed kb entry, always injected (use for constant constraints).
- **`search`** - a templated kb query (`{ "search": "kb", "query": "{{ task }}", "k": 2, "as": "refs" }`),
  ranked by BM25 (+ embeddings if available). Use for task-relevant grounding.

Cap any injected slot with `max_chars`. Templating uses `{{ slot }}` in `prompt.md` and in an action's
`body`.

### The bounded repair loop

Repair is expressed as **unrolled** nodes (no cycles), so a chain always lints clean and terminates:
`generate → check → done | fix → recheck → done | fail`. The `check` action's `on_failure` points at a
`fix` generate node (which receives the oracle's errors and the previous attempt); to repair twice,
add a second `fix2`/`recheck2` pair and point `recheck.on_failure` at `fix2`. See
`examples\dotnet\flows\csharp` (single-file generate-compile-repair) and `examples\dotnet\flows\edit_file`
(a workspace-bound project chain that repairs twice).

Lint chains with `ratchet validate-flow <dir>` (all) or `ratchet validate-flow <dir> <name>` (one): it checks
node kinds, required fields, unknown tool references, unreachable nodes, and that chain + tool names
don't collide.

## A note on workspaces

`workspaces/` holds project workspaces - a per-project sandbox the project chains operate on. The
active workspace (set with `/ws switch`) is injected into chains as `$workspace` and into chat as the
session focus. The C# reference ratchet scaffolds a project there with `new_project`, builds it up
with the `add_file` / `edit_file` chains (whole-project compile oracle + repair), and writes a
SAC-safe launcher with `make_launcher`. See [console.md](console.md#the-project-lifecycle).
