# Authoring flows (action chains)

A flow is an **action chain**: a directory of nodes the engine walks from an entry to an exit, one
step at a time. It is prompt chaining made filesystem-native and deterministic - the chain is the
orchestrator, the model proposes inside nodes, and the model never decides what runs next. This is the
technical how-to; for the context model see [context-binding.md](context-binding.md), for tools see
[authoring-tools.md](authoring-tools.md).

## Two tiers

- **Dispatch** (which chain): a request is matched to ONE chain. `/route` does this (embedder narrows
  the catalog -> the model picks from a closed enum -> a gate + confirm). Or you name it: `/flow <name>`.
- **Chain** (within a chain): the engine loops from `entry`, reading one `action.json` per step,
  following `on_success`/`on_failure`/`transitions` edges until an `exit`. The model is called only at
  `generate` and `ai_branch` nodes, each a single constrained proposal.

## Directory shape

```
flows/<chain>/
  chain.json                       # the chain header
  actions/<node>/action.json       # one per node
  actions/<node>/prompt.md         # model instructions ({{ slot }}) - for generate / ai_branch only
```

### chain.json

```jsonc
{
  "id": "edit_file",
  "version": "1.1.0",
  "summary": "Apply a change to a file in the active workspace; rebuild and repair up to twice.",
  "entry": "edit_file.read",
  "inputs": ["path", "request"],          // optional: named slots split from the run input
  "budgets": { "max_steps": 14, "max_total_tokens": 22000, "max_wallclock_seconds": 1200 },
  "nodes": ["edit_file.read","edit_file.generate","edit_file.build","edit_file.fix",
            "edit_file.rebuild","edit_file.fix2","edit_file.rebuild2","edit_file.log",
            "edit_file.done","edit_file.fail"]
}
```

- **`entry`** - the first node id. **`nodes`** - every node id (must match the `actions/<node>/` dirs).
- **`summary`** - the match surface for `/route` and the `/flows` listing.
- **`inputs`** - optional named slots. The run input (`$input`) is split head/tail across them: with
  `["path", "request"]`, `/flow edit_file src/Ui/MainForm.cs add a Clear button` binds
  `path = "src/Ui/MainForm.cs"` and `request = "add a Clear button"`. Nodes bind these by name
  (`{ "from": "path", ... }`). Without `inputs`, a node binds the whole input via `{ "from": "$input" }`.
- **`budgets`** - hard caps: `max_steps` (loop ceiling), `max_total_tokens` (summed across model
  calls), `max_wallclock_seconds`. Hitting one aborts with an outcome.

## Node kinds

### `action` - run a tool, route on its exit code
The deterministic step (the Oracle, or any side-effecting script). `tool` names a declared tool;
`inputs` bind its arguments into slots; `body` templates the tool's argument object from those slots;
the tool's **exit code** routes the edge (`0` -> `on_success`, non-zero -> `on_failure`).

```jsonc
{ "id": "edit_file.build", "kind": "action", "tool": "stage_and_build",
  "inputs": [ { "from": "$workspace", "path": ".", "as": "proj" },
              { "from": "path",       "path": ".", "as": "path" },
              { "from": "edit_file.generate", "path": ".", "as": "code" } ],
  "body": { "proj": "{{ proj }}", "path": "{{ path }}", "code": "{{ code }}" },
  "on_success": "edit_file.log", "on_failure": "edit_file.fix" }
```

### `generate` - text (or structured JSON) via the generate seat
The proposer. Renders `prompt.md` from its bound slots; its output becomes this node's value (which a
later node binds via `from`).

```jsonc
{ "id": "edit_file.generate", "kind": "generate", "prompt": "./prompt.md",
  "inputs": [ { "from": "path", "path": ".", "as": "path" },
              { "from": "request", "path": ".", "as": "request" },
              { "from": "edit_file.read", "path": ".", "as": "current", "max_chars": 8000 },
              { "search": "kb", "query": "{{ request }}", "k": 2, "as": "refs", "max_chars": 3000 } ],
  "on_success": "edit_file.build", "on_failure": "edit_file.fail" }
```

By default the output is free text. Add an **`output_schema`** (a JSON Schema object) and the node
returns structured JSON validated against it; downstream bindings then pull individual fields with
`path` (a JSON pointer - see [context-binding.md](context-binding.md)). This is how a "plan" node
decides what to retrieve: it emits one query field per knowledge base, and the next node routes each
into its own `search`. An empty field renders an empty query, which the engine skips - so only the
sources the model chose are consulted, with no wasted context.

```jsonc
{ "id": "cpp.plan", "kind": "generate", "prompt": "./prompt.md",
  "inputs": [ { "from": "$input", "path": ".", "as": "task" } ],
  "output_schema": { "type": "object",
    "properties": { "cppref_q": { "type": "string" }, "win32_q": { "type": "string" } },
    "required": ["cppref_q", "win32_q"] },
  "on_success": "cpp.generate", "on_failure": "cpp.generate" }
// then in cpp.generate:  { "from": "cpp.plan", "path": "cppref_q", "as": "cppref_q" }
//                        { "search": "cppref", "query": "{{ cppref_q }}", "k": 3, "as": "refs" }
```

### `ai_branch` - a model-chosen edge (closed enum)
Fill slots -> render `prompt.md` -> the model returns one value from a fixed enum -> `transitions` maps
it to the next node. The model is grammar-constrained to the declared edges; it cannot name a node that
isn't there. This is the only place the model influences control flow, and only within author-declared
options.

```jsonc
{ "id": "triage", "kind": "ai_branch", "prompt": "./prompt.md",
  "inputs": [ { "from": "$input", "path": ".", "as": "task" } ],
  "transitions": { "code": "write_code", "answer": "answer_kb", "none": "triage_fail" } }
```

### `summarizer` - deterministic transform
Fold prior outputs into a named slot without a model call (e.g. concatenate, trim). Use when a later
node needs a derived value, not a fresh generation.

### `exit` - terminate
```jsonc
{ "id": "edit_file.done", "kind": "exit", "outcome": "success" }
{ "id": "edit_file.fail", "kind": "exit", "outcome": "aborted: did not build after two repairs" }
```

## The bounded repair loop (the core pattern)

Repair is expressed as **unrolled** nodes - no cycles - so the chain always lints clean and provably
terminates. The shape:

```
generate -> check -> (pass) done
                  -> (fail) fix -> recheck -> (pass) done
                                           -> (fail) fail
```

`check` is an `action` whose `on_failure` points at `fix` (a `generate` node bound to the oracle's
errors + the previous attempt); `recheck` re-runs the same tool. **To repair twice**, add a
`fix2`/`recheck2` pair and point `recheck.on_failure` at `fix2` (see `examples/dotnet/flows/edit_file`,
which repairs twice; `examples/dotnet/flows/csharp` repairs once). Keep budgets in step with the depth.

## A complete minimal chain

`examples/template/flows/draft` is the canonical generate->check->repair, copy-ready:

```jsonc
// chain.json
{ "id": "draft", "version": "1.0.0", "entry": "draft.generate",
  "summary": "Generate an artifact grounded in kb, verify with the oracle, repair once.",
  "budgets": { "max_steps": 10, "max_total_tokens": 12000, "max_wallclock_seconds": 600 },
  "nodes": ["draft.generate","draft.check","draft.fix","draft.recheck","draft.done","draft.fail"] }
```

`actions/generate/` (generate, grounded), `actions/check/` (action -> `example_check` tool),
`actions/fix/` (generate, bound to `draft.check` errors + `draft.generate` prev), `actions/recheck/`
(action), `actions/done|fail/` (exit). Read the dir for the full set.

## Run, lint, inspect

```
.\ratchet.cmd flow <dir> <name> [--ws <workspace>] [input...]   # run a chain (non-interactive; --ws sets $workspace)
.\ratchet.cmd validate-flow <dir> [name]     # lint all chains, or one by name
.\ratchet.cmd flows <dir>                     # list a dir's chains (the /route catalog)
```

`validate-flow` checks: valid node kinds, required fields present, every edge target is a declared
node, every `from` is a reachable predecessor, unknown tool references, and that chain + tool names do
not collide (flat routing needs unique names).

**Run state** is written to `runs/<id>/`: `meta.json` (chain, input, budgets), one `step-NNN.json` per
node (id, kind, ok, output - including the oracle's diagnostics on a failed build), and `outcome.json`
(`{ outcome, steps, error }`). This is your trace when a run misbehaves; `runs/` is gitignored.

## Gotchas

- **A node sees only its declared `inputs`** - if a prompt references `{{ x }}`, bind `x`. There is no
  ambient context. (This is the point - see [context-binding.md](context-binding.md).)
- **Cap growing slots** with `max_chars`; the token budget counts the ceiling.
- **Unroll repair, don't loop** - the engine has no cycle primitive by design (lintability +
  termination). Add explicit `fix2`/`recheck2` to go deeper.
- **C# 5 in prompts** - if a `generate` node emits C#, its prompt must forbid `$"..."` and the other
  C# 6+ constructs, or the compile oracle will reject and burn a repair (see the dotnet ratchet's
  prompts).
