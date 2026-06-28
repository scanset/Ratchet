package chain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/model"
)

// A search binding's target library may be a slot ("{{kb}}"), resolved at runtime against the KB
// registry - so the same binding retrieves from whichever KB the caller selected. (Catalog-driven
// routing depends on this; before the fix, ib.Lib was read literally and "{{kb}}" resolved to nothing.)
func TestResolveSearchDynamicLib(t *testing.T) {
	root := t.TempDir()
	for _, kb := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "kb", kb)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		doc := "# " + kb + " topic\n\nthis is the " + kb + " knowledge about widgets and gadgets\n"
		if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := `{"name":"t","knowledgeBases":[{"name":"alpha","path":"kb/alpha"},{"name":"beta","path":"kb/beta"}]}`
	if err := os.WriteFile(filepath.Join(root, conventions.ConfigFile), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := instance.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(inst, fakeGen{}, nil)

	ib := model.InputBinding{As: "refs", Source: "search", Lib: "{{kb}}", Query: "widgets", K: 1}

	gotA := e.resolveSearch(ib, map[string]string{"kb": "alpha"})
	if !strings.Contains(gotA, "alpha") || strings.Contains(gotA, "beta") {
		t.Fatalf("slot kb=alpha should retrieve the alpha doc, got: %q", gotA)
	}
	gotB := e.resolveSearch(ib, map[string]string{"kb": "beta"})
	if !strings.Contains(gotB, "beta") || strings.Contains(gotB, "alpha") {
		t.Fatalf("slot kb=beta should retrieve the beta doc, got: %q", gotB)
	}

	// An unknown slot value resolves to no library -> empty (graceful, not an error).
	if got := e.resolveSearch(ib, map[string]string{"kb": "missing"}); got != "" {
		t.Fatalf("unknown lib should yield empty grounding, got: %q", got)
	}

	// Regression: a literal (non-templated) lib still works exactly as before.
	lit := model.InputBinding{As: "refs", Source: "search", Lib: "alpha", Query: "widgets", K: 1}
	if got := e.resolveSearch(lit, nil); !strings.Contains(got, "alpha") {
		t.Fatalf("literal lib regressed, got: %q", got)
	}
}
