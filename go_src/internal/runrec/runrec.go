// Package runrec is the data contract for what a chain run writes to runs/<runID>/.
// It defines the typed records (meta, step, change, outcome, index entry) and helpers to read/write
// them through the instance sandbox. It is a local audit log, not a signed/tamper-evident one.
// See docs/concepts/run-record.md for the full spec.
package runrec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
)

// SchemaVersion is stamped on every record so readers can evolve with the format.
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
	Kind          string `json:"kind"`
	ParentRunID   string `json:"parent_run_id,omitempty"`
	Ratchet       string `json:"ratchet"`
	EngineVersion string `json:"engine_version"`
	ChainID       string `json:"chain_id"`
	Caller        string `json:"caller"`
	Workspace     string `json:"workspace"`
	Input         string `json:"input"`
	InputSHA256   string `json:"input_sha256"`
	ModelSeats    Seats  `json:"model_seats"`
	OllamaHost    string `json:"ollama_host"`
	OSArch        string `json:"os_arch"`
	Started       string `json:"started"`
}

// Seats records the model names actually in play for the run.
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
	Tokens        Tokens  `json:"tokens"`
	Model         string  `json:"model,omitempty"`
	Oracle        *Oracle `json:"oracle,omitempty"`
	RepairIndex   int     `json:"repair_index"`
	Next          string  `json:"next,omitempty"`
	Outcome       string  `json:"outcome,omitempty"`
	Prompt        string  `json:"prompt,omitempty"`
	Output        string  `json:"output,omitempty"`
	OutputSHA256  string  `json:"output_sha256,omitempty"`

	// Tier 2 (reserved; omitempty keeps v1 records clean until populated).
	Retrieval *Retrieval `json:"retrieval,omitempty"`
	Bindings  []Binding  `json:"bindings,omitempty"`
	Timing    *Timing    `json:"timing,omitempty"`
}

// Tokens is a prompt/completion/total triple.
type Tokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

// Oracle is the deterministic verdict on an action/foreach step.
type Oracle struct {
	Tool     string `json:"tool"`
	ExitCode int    `json:"exit_code"`
	OK       bool   `json:"ok"`
}

// Change is one entry in runs/<runID>/changes.json.
type Change struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	Bytes        int64  `json:"bytes"`
	SHA256Before string `json:"sha256_before,omitempty"`
	SHA256After  string `json:"sha256_after,omitempty"`
}

// Outcome is runs/<runID>/outcome.json: verdict + metrics.
type Outcome struct {
	SchemaVersion  int     `json:"schema_version"`
	Outcome        string  `json:"outcome"`
	Finished       string  `json:"finished"`
	DurationMS     int64   `json:"duration_ms"`
	Steps          int     `json:"steps"`
	Error          bool    `json:"error"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	AbortReason    string  `json:"abort_reason,omitempty"`
	Tokens         Tokens  `json:"tokens"`
	RepairCount    int     `json:"repair_count"`
	FirstPassSteps int     `json:"first_pass_steps"`
	GenerateSteps  int     `json:"generate_steps"`
	OraclePassRate float64 `json:"oracle_pass_rate"`
	ChangedFiles   int     `json:"changed_files"`
	Rollbackable   bool    `json:"rollbackable"`
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

// Tier 2 reserved types (not yet populated by the engine).
type Retrieval struct {
	Query    string         `json:"query"`
	CacheHit bool           `json:"cache_hit"`
	Hits     []RetrievalHit `json:"hits"`
}
type RetrievalHit struct {
	Entry string  `json:"entry"`
	Score float64 `json:"score"`
}
type Binding struct {
	Slot   string `json:"slot"`
	Source string `json:"source"`
	Chars  int    `json:"chars"`
	Tokens int    `json:"tokens"`
}
type Timing struct {
	LoadMS       int64   `json:"load_ms"`
	PromptEvalMS int64   `json:"prompt_eval_ms"`
	EvalMS       int64   `json:"eval_ms"`
	TokensPerSec float64 `json:"tokens_per_sec"`
}

// Sha256Hex hashes bytes; used for the change manifest and per-artifact content hashes (input/output).
func Sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// --- IO (through the instance sandbox; all paths land under runs/) ---

func runDir(runID string) string { return conventions.RunsDir + "/" + runID }

func writePretty(inst *instance.Instance, rel string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return inst.WriteFile(rel, string(b)+"\n")
}

// WriteMeta writes meta.json.
func WriteMeta(inst *instance.Instance, m Meta) error {
	m.SchemaVersion = SchemaVersion
	return writePretty(inst, runDir(m.RunID)+"/meta.json", m)
}

// WriteStep writes step-NNN.json.
func WriteStep(inst *instance.Instance, runID string, s Step) error {
	s.SchemaVersion = SchemaVersion
	return writePretty(inst, fmt.Sprintf("%s/step-%03d.json", runDir(runID), s.Index), s)
}

// WriteChanges writes changes.json (the rollback + provenance manifest).
func WriteChanges(inst *instance.Instance, runID string, changes []Change) error {
	if changes == nil {
		changes = []Change{}
	}
	return writePretty(inst, runDir(runID)+"/changes.json", changes)
}

// WriteOutcome writes outcome.json.
func WriteOutcome(inst *instance.Instance, runID string, o Outcome) error {
	o.SchemaVersion = SchemaVersion
	return writePretty(inst, runDir(runID)+"/outcome.json", o)
}

// AppendIndex appends one summary entry to runs/index.json (read-modify-write of a JSON array).
func AppendIndex(inst *instance.Instance, e IndexEntry) error {
	all, _ := ReadIndex(inst)
	all = append(all, e)
	return writePretty(inst, conventions.RunsDir+"/"+conventions.RunsIndexFile, all)
}

// ReadIndex reads runs/index.json (empty slice if absent).
func ReadIndex(inst *instance.Instance) ([]IndexEntry, error) {
	s, err := inst.ReadFile(conventions.RunsDir + "/" + conventions.RunsIndexFile)
	if err != nil {
		return []IndexEntry{}, nil
	}
	var out []IndexEntry
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []IndexEntry{}, err
	}
	return out, nil
}

// RunID formats a run id from a timestamp: YYYYMMDD-HHMMSS-mmm.
func RunID(t time.Time) string {
	return t.Format("20060102-150405") + fmt.Sprintf("-%03d", t.Nanosecond()/1e6)
}

// UniqueRunID returns a RunID that does not collide with an existing runs/<id> directory, bumping
// the timestamp by a millisecond until free. Guards against same-millisecond runs clobbering each other.
func UniqueRunID(inst *instance.Instance, t time.Time) string {
	for {
		id := RunID(t)
		if _, err := os.Stat(filepath.Join(inst.Root, conventions.RunsDir, id)); os.IsNotExist(err) {
			return id
		}
		t = t.Add(time.Millisecond)
	}
}
