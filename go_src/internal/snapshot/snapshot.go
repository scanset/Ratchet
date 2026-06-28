// Package snapshot copies, diffs, restores, and prunes whole-workspace snapshots that back the
// engine's per-run rollback. Snapshots live under runs/<id>/workspace-before and pair with the
// run record (see internal/runrec and docs/concepts/run-record.md). Pure Go, no external deps.
package snapshot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/runrec"
)

// DefaultIgnore are directory names skipped when snapshotting/diffing/restoring, so a large
// workspace does not copy gigabytes (node_modules etc.) per run. Matched by path component.
var DefaultIgnore = map[string]bool{
	"node_modules": true,
	".git":         true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"tmp":          true,
}

// ignored reports whether a workspace-relative path should be skipped.
func ignored(rel string) bool {
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "vendor/bundle") {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if DefaultIgnore[part] {
			return true
		}
	}
	return false
}

// SnapshotDirAbs is the on-disk location of a run's before-snapshot.
func SnapshotDirAbs(inst *instance.Instance, runID string) string {
	return filepath.Join(inst.Root, conventions.RunsDir, runID, conventions.SnapshotSubdir)
}

// SnapshotRel is the sandbox-relative path recorded in the run outcome.
func SnapshotRel(runID string) string {
	return conventions.RunsDir + "/" + runID + "/" + conventions.SnapshotSubdir
}

// Exists reports whether a run's snapshot is still on disk (i.e. the run is rollbackable).
func Exists(inst *instance.Instance, runID string) bool {
	st, err := os.Stat(SnapshotDirAbs(inst, runID))
	return err == nil && st.IsDir()
}

// Snapshot copies srcAbs (a workspace) into destAbs, skipping ignored paths. A missing/empty
// source yields an empty snapshot dir (so an undo restores the workspace to empty, which is correct).
func Snapshot(srcAbs, destAbs string) error {
	if err := os.MkdirAll(destAbs, 0o755); err != nil {
		return err
	}
	st, err := os.Stat(srcAbs)
	if err != nil || !st.IsDir() {
		return nil
	}
	return copyTree(srcAbs, destAbs)
}

// Restore makes wsAbs match the snapshot at snapAbs: it removes non-ignored top-level entries of
// the workspace (preserving ignored dirs like node_modules), then copies the snapshot back over.
func Restore(snapAbs, wsAbs string) error {
	if wsAbs == "" || wsAbs == string(filepath.Separator) {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(wsAbs, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(wsAbs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if ignored(e.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(wsAbs, e.Name())); err != nil {
			return err
		}
	}
	if st, err := os.Stat(snapAbs); err != nil || !st.IsDir() {
		return nil // snapshot was empty: workspace is now cleared, which is the correct restore
	}
	return copyTree(snapAbs, wsAbs)
}

// Diff computes the change manifest between a before-snapshot and the current workspace.
func Diff(beforeAbs, afterAbs string) ([]runrec.Change, error) {
	before, err := fileMap(beforeAbs)
	if err != nil {
		return nil, err
	}
	after, err := fileMap(afterAbs)
	if err != nil {
		return nil, err
	}
	var changes []runrec.Change
	for rel, a := range after {
		if b, ok := before[rel]; ok {
			if b.hash != a.hash {
				changes = append(changes, runrec.Change{Path: rel, Status: runrec.Modified, Bytes: a.size, SHA256Before: b.hash, SHA256After: a.hash})
			}
		} else {
			changes = append(changes, runrec.Change{Path: rel, Status: runrec.Added, Bytes: a.size, SHA256After: a.hash})
		}
	}
	for rel, b := range before {
		if _, ok := after[rel]; !ok {
			changes = append(changes, runrec.Change{Path: rel, Status: runrec.Deleted, SHA256Before: b.hash})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// Prune keeps the newest keepN snapshots for a workspace and removes older ones (their records
// stay; only the heavy workspace-before dir is deleted, marking those runs not rollbackable).
func Prune(inst *instance.Instance, wsName string, keepN int) error {
	if keepN < 0 {
		keepN = 0
	}
	idx, _ := runrec.ReadIndex(inst)
	var ids []string
	for _, e := range idx {
		if e.Workspace == wsName && Exists(inst, e.RunID) {
			ids = append(ids, e.RunID)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // runIDs are timestamp-ordered
	for i, id := range ids {
		if i < keepN {
			continue
		}
		if err := os.RemoveAll(SnapshotDirAbs(inst, id)); err != nil {
			return err
		}
	}
	return nil
}

// Rollbackable returns the rollbackable runs for a workspace (snapshot still on disk), newest first.
func Rollbackable(inst *instance.Instance, wsName string) []runrec.IndexEntry {
	idx, _ := runrec.ReadIndex(inst)
	var out []runrec.IndexEntry
	for _, e := range idx {
		if e.Workspace == wsName && Exists(inst, e.RunID) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunID > out[j].RunID })
	return out
}

// RecordPoint snapshots wsAbs and writes a meta+outcome run record of the given kind (snapshot or
// rollback), returning the new run id. It is the restore-point primitive behind /snapshot and /rollback.
func RecordPoint(inst *instance.Instance, kind, wsAbs, wsName, parentID, ratchet, engineVer, caller string) (string, error) {
	now := time.Now()
	id := runrec.UniqueRunID(inst, now)
	if err := Snapshot(wsAbs, SnapshotDirAbs(inst, id)); err != nil {
		return "", err
	}
	m := runrec.Meta{
		RunID: id, Kind: kind, ParentRunID: parentID, Ratchet: ratchet, EngineVersion: engineVer,
		ChainID: kind, Caller: caller, Workspace: wsName, Started: now.Format(time.RFC3339),
	}
	if err := runrec.WriteMeta(inst, m); err != nil {
		return "", err
	}
	out := runrec.Outcome{Outcome: kind, Finished: now.Format(time.RFC3339), Rollbackable: true, SnapshotPath: SnapshotRel(id)}
	if err := runrec.WriteOutcome(inst, id, out); err != nil {
		return "", err
	}
	_ = runrec.AppendIndex(inst, runrec.IndexEntry{RunID: id, Time: m.Started, Kind: kind, Chain: kind, Workspace: wsName, Outcome: kind, Rollbackable: true})
	return id, nil
}

// RollbackTo restores wsAbs to targetID's before-snapshot. It first records the current state as a
// reversible rollback run, then restores, then writes that rollback run's change manifest and prunes.
// Returns the new (rollback) run id and the number of files the restore changed.
func RollbackTo(inst *instance.Instance, wsAbs, wsName, targetID, ratchet, engineVer, caller string, keepN int) (string, int, error) {
	if !Exists(inst, targetID) {
		return "", 0, fmt.Errorf("run %s has no snapshot", targetID)
	}
	newID, err := RecordPoint(inst, runrec.KindRollback, wsAbs, wsName, targetID, ratchet, engineVer, caller)
	if err != nil {
		return "", 0, err
	}
	if err := Restore(SnapshotDirAbs(inst, targetID), wsAbs); err != nil {
		return "", 0, err
	}
	changes, _ := Diff(SnapshotDirAbs(inst, newID), wsAbs)
	_ = runrec.WriteChanges(inst, newID, changes)
	_ = Prune(inst, wsName, keepN)
	return newID, len(changes), nil
}

// --- internals ---

type fileInfo struct {
	hash string
	size int64
}

// fileMap returns rel-path -> {hash,size} for every non-ignored file under root.
func fileMap(root string) (map[string]fileInfo, error) {
	out := map[string]fileInfo{}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return out, nil
	}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if ignored(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		h, sz, herr := hashFile(p)
		if herr != nil {
			return herr
		}
		out[filepath.ToSlash(rel)] = fileInfo{hash: h, size: sz}
		return nil
	})
	return out, err
}

func hashFile(abs string) (string, int64, error) {
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", 0, err
	}
	return runrec.Sha256Hex(b), int64(len(b)), nil
}

func copyTree(srcAbs, destAbs string) error {
	return filepath.WalkDir(srcAbs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(srcAbs, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if ignored(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destAbs, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(srcAbs, destAbs string) error {
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return err
	}
	in, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destAbs)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
