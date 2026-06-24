# Edit a flow (agent playbook)

Goal: add a new chain or change an existing one. This is the fast path; the format contract is
[Author flows](../how-to/author-flows.md).

## A chain is a folder

```
flows/<chain>/
  chain.json                    # { id, version, summary, entry, inputs?, budgets, nodes[] }
  actions/<node>/action.json    # { id, kind, inputs[], edges }
  actions/<node>/prompt.md      # for generate / ai_branch nodes
```

`nodes[]` must list every node id. `entry` is the first node. Edges are `on_success` / `on_failure`
(action) or `transitions` (ai_branch).

## Node kinds (one line each)

- `action` - run a tool; route on its exit code (`on_success` / `on_failure`).
- `generate` - free text from `prompt.md`; add `output_schema` to force JSON.
- `ai_branch` - prompt -> enum decision -> `transitions`.
- `summarizer` - deterministic merge of inputs.
- `foreach` - run a sub-chain per line in a list slot (the fan-out; depth- and budget-guarded).
- `exit` - terminal `outcome`.

## To ADD a chain

1. Copy the nearest working chain (e.g. `template/flows/add_file`) to `flows/<new>/`.
2. Set `id` / `summary` / `entry`, list every node in `nodes[]`, set `budgets`.
3. Edit each node's `action.json` (kind, `inputs`, edges) and its `prompt.md`.
4. Bind every `{{ slot }}` a prompt uses in that node's `inputs` - a node sees ONLY its declared inputs.
5. `ratchet validate-flow <ratchet> <new>` -> fix until clean.
6. Run it: `ratchet flow <ratchet> <new> [--ws <p>] "<input>"`; read `runs/<id>/`.

## To CHANGE a chain

1. `ratchet validate-flow <ratchet> <name>` first (know the current state).
2. Edit the node or prompt. To repair twice, add a `fix2` / `recheck2` pair and point
   `recheck.on_failure` at `fix2` (add both to `nodes[]`).
3. `validate-flow` again; run; read the transcript in `runs/`.

## Gotchas

- An unbound `{{ x }}` renders empty - bind it in `inputs`.
- Chains are UNROLLED (no loop-back edges); add explicit repair pairs to retry.
- A `foreach` names a sub-flow - it must exist and pass `validate-flow` on its own.
