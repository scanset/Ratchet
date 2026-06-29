# Compose a system from specs

Composition builds a whole multi-file program from a folder of `.spec` files. You describe each piece in a
short spec; the model plans the build order, then writes each unit one at a time, checking it against the
real code already built. It is the multi-file version of a single flow: same propose-then-verify, run unit
by unit.

This is a ratchet-authoring pattern - it ships in the `template` ratchet. The engine only provides the
generic `foreach` step; everything else is flows and tools inside the ratchet.

## What you write: a `.spec` file

A spec is a STRUCTURED PROMPT, not a parsed format. One file per unit, under `<workspace>/specs/`. Use
plain fields:

```
name: TaskStore
intent: an in-memory store of tasks
behavior:
  - Add(task) assigns the next id starting at 1 and returns it
  - All() returns every task
  - Complete(id) marks that task done
constraints: <your language>; uses Task
module: core        # optional: which folder under src/ this unit goes in
```

The model reads all the specs (in any order, even with slightly inconsistent names) and works out the
plan. Keep `name`, `intent`, and `behavior` sharp - they are the prompt.

## Write specs that hold

The spec is where you make the decisions the model cannot. A vague spec does not get "filled in sensibly" -
the model guesses, and on a test-driven flow its guess disagrees with the test's guess, so the gate never
closes. What makes a spec hold:

- **State the invariant, not just the prose.** "compress runs" is a wish; "a run of byte `b` length `n`
  (1..255) encodes to the two bytes `n,b`; `Decode(Encode(s)) == s` for all `s`" is a contract the Oracle
  can check. Behaviour lines become assertions almost verbatim - write them as the property you want proven.
- **Disambiguate anything with two valid readings.** If a behaviour could be implemented two correct ways
  (which encoding? which rounding? what order?), pin one. Ambiguity is the single most common cause of a
  stuck gate, and it is *your* gap to close, not a model weakness.
- **Name the edge cases explicitly.** Zero, negative, empty, overflow, "key absent". An unstated edge is an
  unspecified behaviour; say what happens (and whether it errors or is silently handled - "never panics").
- **Pin the mechanism only when it is part of the contract.** Say "safe for concurrent use (no data race
  under `-race`)" when concurrency is required; leave `mutex` vs `atomic` to the model when it is not.

This is the same instinct as binding the real API: don't ask the model to remember or invent the contract -
state it. See [Methodologies](../concepts/methodologies.md) for why an ambiguous spec masquerades as a model
failure, and which residual a stalled step actually is.

## How to run it

```
ratchet flow <ratchet-dir> compose --ws <project> ""
```

`--ws <project>` is the workspace; its specs live in `<project>/specs/*.spec`. Scaffold the workspace
first (e.g. `new_project <project>`), write the specs, then compose.

## What happens (the pipeline)

| Step | What it does |
|---|---|
| `read_specs` | reads every `.spec` in the folder |
| `plan` | the model infers the units in dependency order + the shared contracts (schema-forced JSON) |
| `plan_units` | turns the plan into a worklist: one line per unit, `<path> <spec>` |
| `foreach add_unit` | builds each unit in order (see below) |
| `build_project` | builds the whole thing at the end |

Each `add_unit` run: read the unit's spec, read the project so far, get the API of the units already built
(`project_api`), generate the file, build the WHOLE project (the Oracle), repair up to twice, register it.
Because units are built in dependency order, each one is checked against real, compiled code - not a guess.

## The unit model

- The ENTRY unit (role `behavior` or `gui`) becomes the program's main/entry file and wires the others
  together.
- Every other unit is a component file under `src/`, in its `module` folder (default `core`).
- One file per unit by default. For a header+source language (C++), a unit is a declaration + a definition
  emitted together; see `RatchetBox/Windows/cpp`.

## What you implement per domain

The pipeline shape is generic; three pieces are language-specific (the `template` ships them as stubs):

- **`build_project`** - the Oracle: build the whole project, exit 0 = built.
- **`project_api`** - emit the public API of the units already built, so a new unit calls them exactly.
  This is what keeps multi-unit code consistent - see [Composition](../concepts/composition.md).
- **`plan_units`** - map a unit to its file path (set your source extension and entry file).

Working references that implement all three: `RatchetBox/Windows/dotnet4-x` (C#) and `RatchetBox/Windows/cpp` (C++).

## See it work

Write three specs - a data type, a store that uses it, and an entry that uses both - run `compose`, and
read the result plus the run transcript under `runs/`. The `dotnet4-x` and `cpp` ratchets' `Tests/`
folders hold full compose transcripts: the prompts sent and the code the model returned.

For why composition is reliable and where it has limits, see [Composition](../concepts/composition.md).
