# Composition

Composition turns a folder of specs into a working multi-file system. This page explains why it works and
where its limits are. For the steps, see [Compose from specs](../how-to/compose-from-specs.md).

## The idea

Build the units in DEPENDENCY ORDER, and generate each one against the code already built - not against a
guess of what the other units look like. A data type is built first; the store that uses it is built next,
seeing the type's real API; the entry that wires them is built last, seeing both. Each step is
Oracle-checked (it must build) before the next one starts.

## The multi-reference frontier (the main limit)

The reliability of a generated unit scales INVERSELY with how many OTHER units it must call at once:

- **0 to 1** other units referenced: reliable - it composes first try.
- **2 or more**: the drift zone - the model gets the others' exact signatures wrong (a constructor's
  argument count, a method's name) even when it can see the project.

This is not about the entry unit specifically; any unit that references several others can drift.

## How Ratchet closes it

- **Bind the real API.** Before generating a unit, `project_api` extracts the public surface (types,
  constructors, method signatures) of the units already built and puts it in front of the model as the
  authoritative list: use these names and signatures verbatim; if the spec says something different, the
  built code wins. This removes most signature drift.
- **Interfaces are the lever.** An interface collapses many concrete contracts into one. A unit that
  depends on an interface holds a single, stable contract instead of N - so it stays in the reliable zone.

## The Oracle gates contracts, not behavior

The build checks that the code LINKS - right names, right signatures. It does NOT check that the code does
the right thing. So a composed system can build cleanly and still misbehave (for example, wiring the wrong
values together). That gap is closed by a human in the loop: review the result, then give one corrective
prompt (an `edit_file` on the unit that is wrong). The compiler is the contract oracle; the author is the
behavior oracle.

## What composes easily

- Pure data types and single-reference components compose first try.
- Concurrency lives inside a component (a thread-safe class), so it composes like any other unit.

The hard cases are the multi-reference units above - which is exactly what `project_api` and interfaces
address.

Lineage note: composition is built on the engine's generic `foreach` step; everything else - the spec
convention, `project_api`, the plan - lives in the ratchet. See [Architecture](architecture.md) for the
host/ratchet split.
