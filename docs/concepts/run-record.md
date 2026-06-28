# Run record schema (the contract)

This is the data contract for what a chain run writes to `runs/<runID>/`. It is the typed-struct
spec the engine implements and that any reader (the console, the CLI, tooling) parses against. Every
record carries `schema_version` so the format can evolve without breaking readers.

> **Scope.** This is a local audit log: enough observability to see what a run did, what it changed, and
> to roll it back. It is deliberately **not** a signed or tamper-evident log (no hash chain, no
> witnessing); that verifiable-provenance direction is out of scope for this project. Per-artifact
> content hashes (`input_sha256`, `output_sha256`, and the change manifest's before/after hashes) are
> kept because rollback and diffing need them, not as a tamper-evidence claim.

## On-disk layout

```
runs/
  <runID>/                     runID = YYYYMMDD-HHMMSS-mmm
    meta.json                  identity + environment (written at start)
    step-001.json              one per node, in order
    step-002.json
    changes.json               files added/modified/deleted vs the before-snapshot
    outcome.json               verdict + metrics (written at end)
    workspace-before/          full pre-run copy of the workspace (the rollback source)
  index.json                   append-one-entry-per-run summary, for fast listing
```

`runs/` is sandboxed within the instance and gitignored. Records are pretty-printed JSON written with
typed structs (stable field order). The record holds prompts, outputs, inputs, and file contents, so
it is as sensitive as the code; it stays in-house under the retention policy below.

## The structs

```go
package runrec

const SchemaVersion = 1

// Run kinds.
const (
	KindFlow     = "flow"
	KindRollback = "rollback"
	KindSnapshot = "snapshot"
)

// Change statuses.
const (
	Added    = "added"
	Modified = "modified"
	Deleted  = "deleted"
)

// Meta is runs/<runID>/meta.json: run identity + environment, written at start.
type Meta struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	Kind          string `json:"kind"`                     // flow | rollback | snapshot
	ParentRunID   string `json:"parent_run_id,omitempty"`  // rollback target, or foreach parent
	Ratchet       string `json:"ratchet"`
	EngineVersion string `json:"engine_version"`
	ChainID       string `json:"chain_id"`
	Caller        string `json:"caller"`                   // console | mcp | cli
	Workspace     string `json:"workspace"`                // active workspace name ("" if none)
	Input         string `json:"input"`                    // capped
	InputSHA256   string `json:"input_sha256"`             // hash of the full (uncapped) input
	ModelSeats    Seats  `json:"model_seats"`
	OllamaHost    string `json:"ollama_host"`
	OSArch        string `json:"os_arch"`
	Started       string `json:"started"`                  // RFC3339
}

type Seats struct {
	Generate string `json:"generate,omitempty"`
	Dispatch string `json:"dispatch,omitempty"`
	Embed    string `json:"embed,omitempty"`
}

// Step is runs/<runID>/step-NNN.json: one node execution.
type Step struct {
	SchemaVersion int     `json:"schema_version"`
	Index         int     `json:"index"`
	Node          string  `json:"node"`
	Kind          string  `json:"kind"`
	Started       string  `json:"started"`
	DurationMS    int64   `json:"duration_ms"`
	Tokens        Tokens  `json:"tokens"`                  // generate steps
	Model         string  `json:"model,omitempty"`        // seat/model that ran this step
	Oracle        *Oracle `json:"oracle,omitempty"`       // action / foreach
	RepairIndex   int     `json:"repair_index"`           // 0 = first attempt, 1+ = repair
	Next          string  `json:"next,omitempty"`         // ai_branch decision
	Outcome       string  `json:"outcome,omitempty"`      // exit
	Prompt        string  `json:"prompt,omitempty"`       // capped 16000 (generate)
	Output        string  `json:"output,omitempty"`       // capped (16000 generate, 4000 others)
	OutputSHA256  string  `json:"output_sha256,omitempty"`// hash of the full (uncapped) output

	// Tier 2 (reserved, populated in a later phase; omitempty keeps v1 records clean):
	Retrieval *Retrieval `json:"retrieval,omitempty"` // query, top-k entries + scores, cache hit
	Bindings  []Binding  `json:"bindings,omitempty"`  // which slots, sources, sizes
	Timing    *Timing    `json:"timing,omitempty"`    // Ollama load/prompt/eval durations, tok/s
}

type Tokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

type Oracle struct {
	Tool     string `json:"tool"`
	ExitCode int    `json:"exit_code"`
	OK       bool   `json:"ok"`
}

// Change is one entry in runs/<runID>/changes.json (the rollback + provenance manifest).
type Change struct {
	Path         string `json:"path"`                     // workspace-relative
	Status       string `json:"status"`                   // added | modified | deleted
	Bytes        int64  `json:"bytes"`                    // size after (0 for deleted)
	SHA256Before string `json:"sha256_before,omitempty"`
	SHA256After  string `json:"sha256_after,omitempty"`
}

// Outcome is runs/<runID>/outcome.json: verdict + the metrics that matter.
type Outcome struct {
	SchemaVersion  int     `json:"schema_version"`
	Outcome        string  `json:"outcome"`
	Finished       string  `json:"finished"`
	DurationMS     int64   `json:"duration_ms"`
	Steps          int     `json:"steps"`
	Error          bool    `json:"error"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	AbortReason    string  `json:"abort_reason,omitempty"`   // e.g. max_tokens, tool timeout
	Tokens         Tokens  `json:"tokens"`
	RepairCount    int     `json:"repair_count"`            // total repair attempts across the run
	FirstPassSteps int     `json:"first_pass_steps"`        // generate steps that passed with 0 repairs
	GenerateSteps  int     `json:"generate_steps"`
	OraclePassRate float64 `json:"oracle_pass_rate"`        // gated steps passed / gated steps
	ChangedFiles   int     `json:"changed_files"`
	Rollbackable   bool    `json:"rollbackable"`            // snapshot present and within retention
	SnapshotPath   string  `json:"snapshot_path,omitempty"`
}

// IndexEntry is one entry appended to runs/index.json.
type IndexEntry struct {
	RunID        string `json:"run_id"`
	Time         string `json:"time"`
	Kind         string `json:"kind"`
	Chain        string `json:"chain"`
	Workspace    string `json:"workspace"`
	Outcome      string `json:"outcome"`
	DurationMS   int64  `json:"duration_ms"`
	TokensTotal  int    `json:"tokens_total"`
	ChangedFiles int    `json:"changed_files"`
	Rollbackable bool   `json:"rollbackable"`
}

// Tier 2 reserved types.
type Retrieval struct {
	Query   string       `json:"query"`
	CacheHit bool        `json:"cache_hit"`
	Hits    []RetrievalHit `json:"hits"`
}
type RetrievalHit struct {
	Entry string  `json:"entry"`
	Score float64 `json:"score"`
}
type Binding struct {
	Slot   string `json:"slot"`
	Source string `json:"source"` // $input | <node> | ref:<id> | search:<lib>
	Chars  int    `json:"chars"`
	Tokens int    `json:"tokens"`
}
type Timing struct {
	LoadMS       int64   `json:"load_ms"`
	PromptEvalMS int64   `json:"prompt_eval_ms"`
	EvalMS       int64   `json:"eval_ms"`
	TokensPerSec float64 `json:"tokens_per_sec"`
}
```

## Which step fields populate, by node kind

| Kind | Populated beyond the common fields |
| --- | --- |
| `generate` | `tokens`, `model`, `prompt`, `output`, `output_sha256`, `repair_index` |
| `action` | `oracle{tool,exit_code,ok}`, `output` |
| `foreach` | `oracle`, `output` |
| `summarizer` | `output` |
| `ai_branch` | `next` |
| `exit` | `outcome` |

Common to every step: `schema_version`, `index`, `node`, `kind`, `started`, `duration_ms`.

## Example (abbreviated)

`meta.json`
```json
{
  "schema_version": 1,
  "run_id": "20260626-101455-450",
  "kind": "flow",
  "ratchet": "go",
  "engine_version": "1.2.0",
  "chain_id": "add_file",
  "caller": "console",
  "workspace": "warehouse",
  "input": "a function that reverses a UTF-8 string",
  "input_sha256": "9f2c...",
  "model_seats": { "generate": "qwen3-coder:latest", "embed": "nomic-embed-text" },
  "ollama_host": "http://localhost:11434",
  "os_arch": "linux/amd64",
  "started": "2026-06-26T10:14:55Z"
}
```

`step-002.json` (a generate that needed one repair)
```json
{
  "schema_version": 1,
  "index": 2,
  "node": "write",
  "kind": "generate",
  "started": "2026-06-26T10:14:58Z",
  "duration_ms": 3120,
  "tokens": { "prompt": 1840, "completion": 420, "total": 2260 },
  "model": "qwen3-coder:latest",
  "repair_index": 1,
  "output_sha256": "be41..."
}
```

`outcome.json`
```json
{
  "schema_version": 1,
  "outcome": "ok",
  "finished": "2026-06-26T10:15:10Z",
  "duration_ms": 14900,
  "steps": 4,
  "error": false,
  "tokens": { "prompt": 5200, "completion": 980, "total": 6180 },
  "repair_count": 1,
  "first_pass_steps": 1,
  "generate_steps": 2,
  "oracle_pass_rate": 1.0,
  "changed_files": 2,
  "rollbackable": true,
  "snapshot_path": "runs/20260626-101455-450/workspace-before"
}
```

## Retention

Two concerns, two policies:
- **Records** (meta / step / changes / outcome / index): lightweight. Keep them. This is the
  observability history and the dataset for ratchet evaluation.
- **Snapshots** (`workspace-before/`): heavy. Keep the last **N per workspace** (default 10; a
  `ratchet.json` override is planned), prune older snapshot directories but leave their records intact.
  Beyond N a run is viewable but not rollbackable (`rollbackable: false`).

Snapshots skip a default ignore list (`node_modules`, `.git`, `target`, `dist`, `build`, `tmp`,
`vendor/bundle`), extendable via `.ratchetignore`, so a large workspace does not copy gigabytes per run.

## The metrics that matter

These are computable from the records and are the numbers that show the harness working:
- **First-pass yield** = `first_pass_steps / generate_steps`.
- **Repair count** and whether the run still reached `ok` (convergence).
- **Oracle pass rate** per run, rollupable per ratchet/model via `index.json`.
- **Tokens per run** and tokens spent on repairs vs first-pass.
- **Time to green** = `duration_ms` of a passing run.

## Cross-references

- [Observability](observability.md) - why the record exists and how rollback uses it
- [Architecture](architecture.md) - the propose-then-verify loop the record traces
- [Context Binding](context-binding.md) - what the Tier 2 `bindings` field will make auditable
