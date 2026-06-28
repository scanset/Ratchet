package runrec

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scanset/Ratchet/internal/instance"
)

func TestSha256Hex(t *testing.T) {
	h := Sha256Hex([]byte("abc"))
	if len(h) != 64 {
		t.Fatalf("sha256 hex want 64 chars, got %d", len(h))
	}
	if Sha256Hex([]byte("abc")) != h || Sha256Hex([]byte("abd")) == h {
		t.Fatal("Sha256Hex not deterministic/sensitive")
	}
}

func TestRunIDFormat(t *testing.T) {
	id := RunID(time.Date(2026, 6, 26, 10, 14, 55, 450*int(time.Millisecond), time.UTC))
	if id != "20260626-101455-450" {
		t.Fatalf("RunID = %q, want 20260626-101455-450", id)
	}
}

func TestIORoundTripAndUniqueID(t *testing.T) {
	inst, err := instance.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id := UniqueRunID(inst, now)
	m := Meta{RunID: id, Kind: KindFlow, ChainID: "c", Workspace: "proj"}
	if err := WriteMeta(inst, m); err != nil {
		t.Fatal(err)
	}
	if err := WriteStep(inst, id, Step{Index: 1, Node: "n", Kind: "generate"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteOutcome(inst, id, Outcome{Outcome: "ok", Steps: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendIndex(inst, IndexEntry{RunID: id, Workspace: "proj", Outcome: "ok", Rollbackable: true}); err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(inst)
	if err != nil || len(idx) != 1 || idx[0].RunID != id {
		t.Fatalf("ReadIndex = %+v err=%v", idx, err)
	}
	if _, err := os.Stat(filepath.Join(inst.Root, "runs", id, "meta.json")); err != nil {
		t.Fatalf("meta.json not written: %v", err)
	}
	// UniqueRunID must avoid the now-existing run dir.
	if UniqueRunID(inst, now) == id {
		t.Fatal("UniqueRunID returned a colliding id")
	}
}
