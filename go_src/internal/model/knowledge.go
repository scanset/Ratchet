// Knowledge: a knowledge base is an indexed directory reachable by search. The registry maps a name
// to a path (which may point anywhere, inside or outside the instance). The registry is read fresh on
// use (never cached). Port of src.bak/Model/Knowledge.cs.
package model

import (
	"strings"

	"github.com/scanset/Ratchet/internal/jsonx"
)

// KnowledgeBase is a named, searchable directory. Path is absolute once resolved through the registry.
type KnowledgeBase struct {
	Name    string `json:"name"`
	Path    string `json:"path"`    // absolute once resolved
	Default bool   `json:"default"` // bare /search hits the default KB(s)
}

// LoadKnowledgeList parses a knowledgeBases array (already-parsed []any), dropping entries that lack
// a name or a path. Paths are not yet resolved (the registry does that).
func LoadKnowledgeList(arr []any) []KnowledgeBase {
	var list []KnowledgeBase
	for _, o := range arr {
		ob := jsonx.AsObject(o)
		if ob == nil {
			continue
		}
		kb := KnowledgeBase{
			Name:    jsonx.GetStringOr(ob, "name", ""),
			Path:    jsonx.GetStringOr(ob, "path", ""),
			Default: jsonx.GetBool(ob, "default", false),
		}
		if kb.Name != "" && kb.Path != "" {
			list = append(list, kb)
		}
	}
	return list
}

// KnowledgeRegistry maps names to resolved knowledge bases.
type KnowledgeRegistry struct {
	Bases []KnowledgeBase
}

// Add registers (or overrides by name) a resolved KB.
func (r *KnowledgeRegistry) Add(name, absPath string, isDefault bool) {
	for i := range r.Bases {
		if strings.EqualFold(r.Bases[i].Name, name) {
			r.Bases[i].Path = absPath
			r.Bases[i].Default = isDefault
			return
		}
	}
	r.Bases = append(r.Bases, KnowledgeBase{Name: name, Path: absPath, Default: isDefault})
}

// Find returns the KB with the given name (case-insensitive), or nil.
func (r *KnowledgeRegistry) Find(name string) *KnowledgeBase {
	for i := range r.Bases {
		if strings.EqualFold(r.Bases[i].Name, name) {
			return &r.Bases[i]
		}
	}
	return nil
}

// Defaults returns the KBs flagged as default.
func (r *KnowledgeRegistry) Defaults() []KnowledgeBase {
	var o []KnowledgeBase
	for _, kb := range r.Bases {
		if kb.Default {
			o = append(o, kb)
		}
	}
	return o
}
