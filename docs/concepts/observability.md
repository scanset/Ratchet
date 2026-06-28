# Observability: the run record

Every action-chain run writes a complete, structured record of itself to disk. This is Ratchet's
observability layer: not a log you have to parse, but a per-step transcript of what each step saw, what
the model proposed, what the oracle decided, and how the run ended. Where an open agent loop leaves you
with a tangled conversation, a chain run leaves you with a clean, inspectable record of every decision.

## Where it lives

Each run creates a fresh, timestamped directory under the instance:

```
runs/
  20260626-101455-450/        one run, id = YYYYMMDD-HHMMSS-mmm
    meta.json                 what started it (identity + environment)
    step-001.json             one file per step, in execution order
    step-002.json
    changes.json              files the run added / modified / deleted
    outcome.json              how it ended (verdict + metrics)
    workspace-before/         pre-run copy of the workspace (the rollback source)
  index.json                  one summary entry per run, for fast listing
```

`runs/` is per-instance and gitignored (it is run state, not source). Each run is its own timestamped
directory, so records accumulate; the lightweight JSON records are kept, while the heavy
`workspace-before/` snapshots are bounded (the last N per workspace are retained for rollback, older
ones pruned). Everything is written through the same sandboxed IO as the rest of the engine, so it
cannot escape the instance directory. The exact fields are the contract in [Run record](run-record.md).

## What gets recorded

**`meta.json`** captures the run's inputs: the chain id, the active workspace, the raw input, and the
start time.

**`step-NNN.json`** captures one step, and what it records depends on the node kind:

| Node kind | Records |
| --- | --- |
| `generate` | `node`, `kind`, the rendered `prompt`, and the model `output` (both capped at 16000 chars) |
| `ai_branch` | `node`, `kind`, and `next` (the enum value the model chose) |
| `action` | `node`, `kind`, `ok` (the oracle verdict), and the tool `output` (capped at 4000) |
| `summarizer` | `node`, `kind`, and the merged `output` (capped at 4000) |
| `foreach` | `node`, `kind`, `ok`, and the per-item `output` (capped at 4000) |
| `exit` | `node`, `kind`, and the `outcome` |

**`outcome.json`** captures the verdict for the whole run: the final `outcome`, the `steps` count, and
whether it ended in `error`.

## Why this is observability, not just logging

Three properties make the run record useful where an agent transcript is not:

- **The prompt and the output are recorded together.** For every `generate` step you can see the exact
  rendered prompt, the bound context the model actually received, alongside what it produced. That makes
  [Context Binding](context-binding.md) visible: you can confirm a step saw only its declared inputs,
  and you can see precisely what grounding was injected. A run reads back as a transcript of one
  constrained model call after another.
- **The oracle verdict is recorded per step.** An `action` step records `ok`, so you see not just what
  the tool returned but whether the deterministic gate accepted it. When a run fails, the record shows
  which step the oracle rejected and what the tool said, so a failure is a coordinate, not a mystery.
- **The decisions are explicit.** An `ai_branch` records the enum value the model picked. The model
  never picks freely, so every branch in the run is a recorded choice from a fixed set, not an opaque
  jump.

Put together, you can answer the questions observability is supposed to answer: what did this step see,
what did the model do with it, did the check pass, and why did the run end. You can do it after the
fact, from files on disk, without re-running anything.

## Why it matters beyond debugging

The run record is the same artifact in two roles. For a developer it is observability: the fastest way
to see what a weak model did inside a chain and where it went wrong. For a regulated buyer it is the
beginning of an **audit trail**: a structured, per-step record of how an artifact was produced and what
gated it. This is the concrete form of "verify, do not trust", the chain is not only checked as it runs,
it leaves behind a record you can verify against afterward.

## Reversible, not just readable

Because each run snapshots the workspace before it touches anything and records exactly what it changed
(`changes.json`), a run is **reversible**. `/rollback` restores the active workspace to a run's
pre-state, and the rollback is itself recorded as a reversible run (so you can undo the undo). Rollback
and observability are the same artifact: the record carries both the before-state and the change
manifest. `/runs` lists the log; `/snapshot` takes a manual restore point.

## Honest scope

This is a **local audit log**: enough to see what a run did, what it changed, and to undo it. It is
deliberately not signed, hash-chained, or tamper-evident. It tells you what happened on this machine, not
that the record has not been altered. The per-artifact content hashes it does keep (`input_sha256`,
`output_sha256`, and the change manifest's before/after hashes) are there because rollback and diffing
need them, not as a tamper-evidence claim. Two other limits: large prompts and outputs are truncated to
the caps above, and rollback depth is bounded (older runs stay viewable but only the recent N per
workspace are rollbackable).

## Cross-references

- [Run record](run-record.md) - the exact field-level schema (the data contract)
- [Architecture](architecture.md) - the propose-then-verify control flow the record traces
- [Context Binding](context-binding.md) - what each step is allowed to see, made visible in the record
- [Author flows](../how-to/author-flows.md) - the node kinds whose output shapes the record
