# Context Binding

The discipline that decides **what each step in a chain is allowed to see**. Alongside the Oracle, it
is the reason a weak local model becomes reliable. This is the deep dive; for the chain mechanics see
[authoring-flows.md](../how-to/author-flows.md), for the overview see [architecture.md](architecture.md).

## Why "binding"

In systems and networking, *binding* means a hard, low-level link between an identifier and an object
- data-binding, late-binding, socket-binding. The word is chosen deliberately: context here is **not a
loose, free-flowing conversation**. It is a strict, programmatic link between a data asset and an
execution slot. A chain node does not "have a conversation history"; it has a set of **bound slots**,
each filled from one explicit source, capped in size, and nothing else.

The contrast that matters:

| | Chat / agent loop | Ratchet |
| --- | --- | --- |
| Context shape | a growing **tape** (all prior turns, tool output, system prompts) | a fixed set of **bound slots** |
| Who decides what's in context | accumulation - whatever happened so far | the chain author, per node |
| Failure mode | the model drowns in or is poisoned by accumulated text | each call sees a small, known, scoped input |

Context Binding turns context from a chaotic, non-deterministic tape into an explicitly bound contract.

## The contract (the mechanism)

A node declares `inputs[]`. Each entry is a binding: a **source**, a **destination slot** (`as`), and
an optional size cap (`max_chars`). The engine resolves the bindings **in declared order** into a slot
dictionary, then renders `{{ slot }}` references in the node's `prompt.md` (for `generate`/`ai_branch`)
or in an `action`'s `body` (for tool arguments). The node sees the slot dictionary and nothing else -
no prior prompts, no other nodes' outputs, no engine state.

A source is exactly one of:

```jsonc
{ "from": "<node-id | $input | $workspace | chain-input-slot>", "path": ".", "as": "slot", "max_chars": 4000 }
{ "ref":  "<library>", "id": "<entry-id>",            "as": "slot", "max_chars": 2000 }   // a fixed entry, always present
{ "search": "<library | {{ slot }}>", "query": "{{ task }}", "k": 2, "as": "refs", "max_chars": 2000 } // a templated retrieval
```

- **`from`** binds a prior node's recorded output (or a run seed: `$input`, `$workspace`, or a slot the
  chain declared in its own `inputs`). `path` selects within the value: `.` (or empty) is the whole
  output; any other value is a **JSON pointer** into it (a bare field name like `cppref_q` is treated as
  `/cppref_q`; **array indices work too**, e.g. `selections/0/kb`). So a `generate` node with an
  `output_schema` can emit several fields - or an array of objects - and downstream bindings pull each one:
  a "plan" node emits one query per knowledge base (or a `selections[]` array of `{kb, query}`) and the
  next node routes each into its own `search`. Non-JSON output or a missing field yields an empty slot.
- **`ref`** binds a fixed knowledge entry - always injected, for constant constraints (e.g. "stay in
  C# 5").
- **`search`** binds the top-`k` hits of a query against a knowledge library. Both the **library and the
  query** are rendered over the slots resolved so far, so each may reference an already-resolved slot: a
  literal library (`"search": "kb"`) or a runtime target (`"search": "{{ kb }}"`) chosen by a plan node.

Example - a repair node binds *only* the three things it needs, each from a named origin:

```jsonc
// flows/edit_file/actions/fix/action.json
{ "id": "edit_file.fix", "kind": "generate", "prompt": "./prompt.md",
  "inputs": [
    { "from": "path",               "path": ".", "as": "path" },     // a chain-declared input slot
    { "from": "request",            "path": ".", "as": "request" },  // another
    { "from": "edit_file.generate", "path": ".", "as": "prev",   "max_chars": 6000 },  // the prior attempt
    { "from": "edit_file.build",    "path": ".", "as": "errors", "max_chars": 2000 }   // the oracle's complaint
  ],
  "on_success": "edit_file.rebuild", "on_failure": "edit_file.fail" }
```

The matching `prompt.md` references those slots and only those:

```md
Your edited `{{ path }}` failed to build. Return the corrected COMPLETE file, fixing exactly what the
compiler reported while still applying the change.

## Change requested
{{ request }}

## Compiler errors
{{ errors }}

## Your previous version
{{ prev }}
```

The model never sees the generate node's prompt, the build command, the chain definition, or any other
node's output. It sees four strings. That is the entire context for the call.

## Three layers (how to formalize it)

The phrase decomposes cleanly across three layers - useful when explaining or defending it:

### 1. Static Context Binding
The deterministic process where the orchestrator (C# / .NET / PowerShell) extracts a precise snippet -
a BM25/embedding hit, a file read, a prior node's output - and physically anchors it to a named
template variable before the model is called. The extraction is code, not the model; the anchor is a
`{{ slot }}`, not a free-form instruction. "Static" because the binding set for a node is fixed on disk
and lint-checked, not assembled at runtime by the model.

### 2. Slot-Bound Isolation
The boundary that keeps the model from seeing or bleeding data across bound variables. Each node's
prompt is **micro-segmented**: it contains exactly its slots, each capped by `max_chars`, with no
cumulative tape. One node's confusion or verbosity cannot leak into the next, because the next node
re-binds from declared sources rather than inheriting a transcript. This is the single biggest
reliability gain for a local model.

### 3. Origin-Bound Verification
Every slot is tied to an explicit origin - `from` a known node/seed, `ref` a fixed entry, or `search`
a named library. A privileged `action` (the "deputy" - e.g. a build or write tool) receives its
arguments only through these origin-tagged bindings rendered into its `body`; the model fills
*generate* slots, never tool arguments directly. So data cannot drift into a privileged call from an
unbound or model-chosen origin - it narrows the **confused-deputy** surface: the deputy acts on
declared inputs, not on whatever the model decided to emit.

> Honest scope: layer 3 is a reliability/containment property, not a full security boundary. The
> ultimate trust boundary is still "the ratchet's tools run with your privileges" (see
> [SECURITY.md](../../SECURITY.md)). Context Binding constrains *what flows into* a tool call; it does not
> sandbox the tool itself.

## How it composes with the Oracle

Context Binding and the Oracle are the two halves of the trust line:

- **Context Binding** controls the *input* - the model proposes from a small, scoped, origin-tagged
  context.
- **The Oracle** controls the *output* - a deterministic check accepts or rejects the proposal, and on
  failure the errors are bound back into a repair node (themselves a slot, capped).

Together they make each model call a **bounded, type-safe-ish contract**: known inputs in, a checkable
artifact out, no hidden state on either side.

## Practical guidance

- **Bind the minimum.** Give a node exactly the slots its prompt names. More context is not better; it
  is noise and tokens.
- **Cap everything that can grow.** Put `max_chars` on prior-node outputs, file reads, and search hits.
  The per-node budget counts the ceiling.
- **Place grounding at gates, not everywhere.** A `ref`/`search` binding belongs on the node that
  needs to decide "the right X" or write idiomatically - not on every node. Most nodes bind only
  `from` a prior step.
- **Order matters.** Bindings resolve top-to-bottom, so a `search` can template on a slot bound above
  it.
- **Don't confuse it with "prompt injection."** That term means the *attack* (untrusted input
  hijacking a model). Context Binding is the opposite: a containment mechanism that controls exactly
  what reaches each step.
