// Package meta reads and strips a routable file's leading metadata block:
//
//	<!--icm
//	{ "id": "...", "title": "...", "doc_type": "reference", "summary": "...", "keywords": [...] }
//	-->
//
// Grounding reads strip the block (StripMeta) so the model sees clean content; `ratchet reindex`
// reads it (ExtractMeta) to regenerate manifest.json. Pulled out of src.bak/Runtime/Indexer.cs into
// its own leaf package so both internal/instance and internal/search can use it without an import
// cycle.
package meta

import (
	"strings"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/jsonx"
)

// ExtractMeta returns the metadata object inside a file's <!--icm ... --> block, or nil if
// absent/invalid.
func ExtractMeta(text string) map[string]any {
	if text == "" {
		return nil
	}
	a := strings.Index(text, conventions.MetaOpen)
	if a < 0 {
		return nil
	}
	start := a + len(conventions.MetaOpen)
	b := strings.Index(text[start:], conventions.MetaClose)
	if b < 0 {
		return nil
	}
	jsonStr := strings.TrimSpace(text[start : start+b])
	parsed, err := jsonx.Parse(jsonStr)
	if err != nil {
		return nil
	}
	return jsonx.AsObject(parsed)
}

// StripMeta returns the file content with a leading metadata block removed (for grounding).
func StripMeta(text string) string {
	if text == "" {
		return text
	}
	a := strings.Index(text, conventions.MetaOpen)
	if a < 0 {
		return text
	}
	rel := strings.Index(text[a:], conventions.MetaClose)
	if rel < 0 {
		return text
	}
	b := a + rel
	out := text[:a] + text[b+len(conventions.MetaClose):]
	return strings.TrimLeft(out, "\r\n \t")
}
