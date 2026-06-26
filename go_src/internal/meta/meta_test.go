package meta

import (
	"strings"
	"testing"

	"github.com/scanset/Ratchet/internal/jsonx"
)

// Ports SelfTest.MetaBlock.
func TestExtractAndStrip(t *testing.T) {
	doc := "<!--icm\n{ \"id\": \"x\", \"keywords\": [\"a\", \"b\"] }\n-->\n# Title\n\nbody text"
	m := ExtractMeta(doc)
	if m == nil {
		t.Fatal("ExtractMeta returned nil for a valid block")
	}
	if id, _ := jsonx.GetString(m, "id"); id != "x" {
		t.Fatalf("meta id: want x, got %q", id)
	}
	if len(jsonx.GetArr(m, "keywords")) != 2 {
		t.Fatalf("keywords: want 2, got %d", len(jsonx.GetArr(m, "keywords")))
	}
	stripped := StripMeta(doc)
	if strings.Contains(stripped, "icm") || !strings.HasPrefix(stripped, "# Title") {
		t.Fatalf("strip wrong: %q", stripped)
	}
	// no block: ExtractMeta is nil, StripMeta is identity
	if ExtractMeta("# plain\ntext") != nil {
		t.Fatal("ExtractMeta should be nil with no block")
	}
	if StripMeta("# plain") != "# plain" {
		t.Fatal("StripMeta should be identity with no block")
	}
}
