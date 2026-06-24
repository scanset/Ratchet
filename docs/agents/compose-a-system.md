# Compose a system from specs (agent playbook)

Goal: build a multi-file program from specs. This is the fast path; the format + the why are
[Compose from specs](../how-to/compose-from-specs.md) and [Composition](../concepts/composition.md).

## Steps

1. **Scaffold a workspace.** Run the `new_project` tool: `/do new_project <proj>` in the console, or
   `tools/new_project.ps1 <proj>` from the ratchet root.
2. **Write one `.spec` per unit** under `workspaces/<proj>/specs/`:
   ```
   name: <Unit>
   intent: <one line>
   behavior:
     - <what it does, as bullets>
   constraints: <language>; uses <other units>
   module: core       # optional
   ```
   Exactly one unit is the ENTRY (role `behavior` or `gui`). Keep names consistent across specs.
3. **Compose:** `ratchet flow <ratchet> compose --ws <proj> ""`.
4. **Read the result + transcript** in `runs/`, then build: `/do build_project <proj>` (or
   `tools/build_project.ps1 <proj>`).

## If a unit drifts (wrong names or signatures)

The multi-reference frontier: a unit that calls 2+ others may get their exact signatures wrong.
`project_api` binds the real API of the already-built units to prevent this - make sure your ratchet
implements it. See [Composition](../concepts/composition.md).

## If it builds but misbehaves

The Oracle checks that the code LINKS, not that it is correct. Review the run, then give ONE corrective
prompt: `ratchet flow <ratchet> edit_file --ws <proj> "<path> <what to fix>"`. You are the behavior oracle.

## Per-domain pieces (only if you are authoring the ratchet, not just using it)

`build_project` (the oracle), `project_api` (the existing-units API), `plan_units` (the file-path map).
The `template` ships them as stubs; `RatchetBox/dotnet4-x` and `RatchetBox/cpp` implement them.
