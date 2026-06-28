package search

import (
	"os"
	"path/filepath"
	"testing"
)

// Ports SelfTest.EmbedderRank: query along x; "a" identical, "c" 45deg, "b" orthogonal -> top2 = a, c.
func TestRankByVectors(t *testing.T) {
	q := []float64{1.0, 0.0}
	cands := []VecCand{
		{ID: "a", Vec: []float64{1.0, 0.0}},
		{ID: "b", Vec: []float64{0.0, 1.0}},
		{ID: "c", Vec: []float64{0.7, 0.7}},
	}
	top := RankByVectors(q, cands, 2)
	if len(top) != 2 || top[0] != "a" || top[1] != "c" {
		t.Fatalf("rank wrong: %v", top)
	}
}

func TestBm25RanksRelevantFirst(t *testing.T) {
	docs := []Doc{
		{ID: "a.md", Title: "Builder", Text: "the builder pattern constructs complex objects step by step"},
		{ID: "b.md", Title: "Adapter", Text: "the adapter pattern converts an interface to another"},
	}
	scored := Bm25Scored(docs, "builder pattern objects")
	if len(scored) == 0 || docs[scored[0].Index].ID != "a.md" {
		t.Fatalf("expected a.md ranked first, got %v", scored)
	}
}

func TestTokens(t *testing.T) {
	toks := Tokens("Hello, World_42! foo-bar")
	want := []string{"hello", "world_42", "foo", "bar"}
	if len(toks) != len(want) {
		t.Fatalf("tokens: want %v, got %v", want, toks)
	}
	for i := range want {
		if toks[i] != want[i] {
			t.Fatalf("token %d: want %q, got %q", i, want[i], toks[i])
		}
	}
}

func TestTokensStemming(t *testing.T) {
	cases := map[string]string{
		"channels": "channel", "channel": "channel", // plural collapses to singular
		"matches": "match", "indexing": "index", "policies": "policy",
		"goroutines": "goroutine", "goroutine": "goroutine", // "es" not over-stripped (no sibilant)
		"boxes": "box", "dishes": "dish", // "es" stripped after a sibilant
		"is": "is", "bus": "bus", "css": "css", // short words / <3 remainder untouched
	}
	for in, want := range cases {
		got := Tokens(in)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("Tokens(%q) = %v, want [%q]", in, got, want)
		}
	}
}

// A query in one word form must retrieve a doc written in another (the routing gap stemming closes).
func TestBm25MatchesAcrossWordForms(t *testing.T) {
	docs := []Doc{
		{ID: "fanin.md", Title: "fan-in", Text: "merge several channel into one channel using goroutines"},
		{ID: "ring.md", Title: "ring buffer", Text: "a fixed size circular buffer of elements"},
	}
	scored := Bm25Scored(docs, "merging channels") // plural query vs singular doc
	if len(scored) == 0 || docs[scored[0].Index].ID != "fanin.md" {
		t.Fatalf("expected fanin.md for 'merging channels', got %v", scored)
	}
}

// Indexer roundtrip: build a content manifest, then load it back as a path->entry map.
func TestKbManifestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "builder.md"),
		[]byte("# Builder\n\nThe builder pattern constructs complex objects incrementally.\n"), 0o644))
	sub := filepath.Join(dir, "structural")
	must(t, os.MkdirAll(sub, 0o755))
	must(t, os.WriteFile(filepath.Join(sub, "adapter.md"),
		[]byte("# Adapter\n\nConverts one interface into another expected by clients.\n"), 0o644))

	n := WriteKbManifest(dir, nil)
	if n != 2 {
		t.Fatalf("want 2 entries, got %d", n)
	}
	m := LoadManifestMap(dir)
	if len(m) != 2 {
		t.Fatalf("manifest map: want 2, got %d", len(m))
	}
	if e, ok := m["builder.md"]; !ok || e.Title != "Builder" {
		t.Fatalf("builder entry wrong: %+v", e)
	}
	if e, ok := m["structural/adapter.md"]; !ok || e.ID != "structural-adapter" {
		t.Fatalf("adapter entry id wrong: %+v", e)
	}
}

func TestBuildCorpusStripsMetaAndTitles(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "doc.md"),
		[]byte("<!--icm\n{\"id\":\"x\"}\n-->\n# Real Title\n\nbody\n"), 0o644))
	docs := BuildCorpus(dir)
	if len(docs) != 1 {
		t.Fatalf("want 1 doc, got %d", len(docs))
	}
	if docs[0].Title != "Real Title" {
		t.Fatalf("title: want 'Real Title', got %q", docs[0].Title)
	}
	if contains(docs[0].Text, "icm") {
		t.Fatalf("meta block not stripped: %q", docs[0].Text)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
