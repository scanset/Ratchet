// Package instance is a loaded ratchet: the root directory plus its config and (optional) manifest,
// with sandboxed file IO that cannot escape the instance directory. "Open a directory and land in the
// ratchet" is exactly Open. Port of src.bak/Runtime/Instance.cs.
package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scanset/Ratchet/internal/config"
	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/meta"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/pathutil"
)

// Instance is the root, config, and manifest of a loaded ratchet.
type Instance struct {
	Root     string // the workdir: the write/sandbox root (absolute, normalized)
	Config   *config.Config
	Manifest *model.Manifest // nil when the workdir has no manifest.json
}

// Open loads a ratchet from a config FILE (ratchet.json) or a DIRECTORY (find ratchet.json, then
// legacy names). The write/sandbox root is the config's workdir (default: the config file's folder).
func Open(path string) (*Instance, error) {
	full, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %v", path, err)
	}

	inst := &Instance{}
	cfgPath := ""
	baseDir := full
	st, statErr := os.Stat(full)
	switch {
	case statErr == nil && !st.IsDir():
		cfgPath = full
		baseDir = filepath.Dir(full)
	case statErr == nil && st.IsDir():
		for _, cand := range conventions.ConfigCandidates {
			p := filepath.Join(full, cand)
			if fi, e := os.Stat(p); e == nil && !fi.IsDir() {
				cfgPath = p
				break
			}
		}
	default:
		return nil, fmt.Errorf("opening %s: not a file or directory", path)
	}

	if cfgPath != "" {
		c, err := config.Load(cfgPath)
		if err != nil {
			return nil, err
		}
		inst.Config = c
	} else {
		inst.Config = config.Default(filepath.Base(strings.TrimRight(full, `\/`)))
		inst.Config.SourcePath = filepath.Join(full, conventions.ConfigFile) // resolve relative dirs against the dir
	}

	if inst.Config.Workdir != "" {
		inst.Root = pathutil.ResolveAgainst(inst.Config.SourcePath, inst.Config.Workdir)
	} else if baseDir != "" {
		inst.Root = baseDir
	} else {
		inst.Root = full
	}

	manifestPath := filepath.Join(inst.Root, conventions.ManifestFile)
	if _, e := os.Stat(manifestPath); e == nil {
		if m, err := model.LoadManifest(manifestPath); err == nil {
			inst.Manifest = m
		}
	}
	return inst, nil
}

// dirOr returns a config override (resolved against the config file) or the conventional <workdir>/<name>.
func (i *Instance) dirOr(configDir, conv string) string {
	if configDir != "" {
		return pathutil.ResolveAgainst(i.Config.SourcePath, configDir)
	}
	return filepath.Join(i.Root, conv)
}

func (i *Instance) FlowsDirAbs() string { return i.dirOr(i.Config.FlowsDir, conventions.FlowsDir) }
func (i *Instance) ToolsDirAbs() string { return i.dirOr(i.Config.ToolsDir, conventions.ToolsDir) }
func (i *Instance) SchemasDirAbs() string {
	return i.dirOr(i.Config.SchemasDir, conventions.SchemasDir)
}
func (i *Instance) SamplesDirAbs() string {
	return i.dirOr(i.Config.SamplesDir, conventions.SamplesDir)
}
func (i *Instance) WorkspacesDirAbs() string {
	return i.dirOr(i.Config.WorkspacesDir, conventions.WorkspacesDir)
}

// Knowledge composes the knowledge registry. It prefers a top-level kb/catalog.json (the high-level KB
// manifest: name/path/default/summary); if absent it falls back to this config's knowledgeBases[], so
// ratchets without a catalog work unchanged. Paths resolve against the config file. A conventional kb/
// under the workdir is added as a default if none is declared.
func (i *Instance) Knowledge() *model.KnowledgeRegistry {
	reg := &model.KnowledgeRegistry{}
	bases := i.loadKbCatalog()
	if len(bases) == 0 {
		bases = i.Config.KnowledgeBases
	}
	for _, kb := range bases {
		reg.Add(kb.Name, pathutil.ResolveAgainst(i.Config.SourcePath, kb.Path), kb.Default)
	}
	if reg.Find("kb") == nil {
		kbDir := i.dirOr("", conventions.KbDir)
		if st, err := os.Stat(kbDir); err == nil && st.IsDir() {
			reg.Add("kb", kbDir, len(reg.Defaults()) == 0)
		}
	}
	return reg
}

// loadKbCatalog reads kb/catalog.json (the high-level KB registry) into KnowledgeBase entries, or nil
// if it is absent or unreadable. The entries carry extra fields (docs/summary) the registry ignores.
func (i *Instance) loadKbCatalog() []model.KnowledgeBase {
	data, err := os.ReadFile(filepath.Join(i.dirOr("", conventions.KbDir), conventions.KbCatalogFile))
	if err != nil {
		return nil
	}
	root := jsonx.AsObject(mustParse(data))
	if root == nil {
		return nil
	}
	return model.LoadKnowledgeList(jsonx.GetArr(root, "entries"))
}

// Tools composes the tool set: config.tools[], then toolsDir/manifest.json declarations, then any bare
// toolsDir/*.ps1 script callable by name. Later sources win by name.
func (i *Instance) Tools() []config.Tool {
	byName := map[string]config.Tool{}
	order := []string{}
	put := func(t config.Tool) {
		key := strings.ToLower(t.Name)
		if _, seen := byName[key]; !seen {
			order = append(order, key)
		}
		byName[key] = t
	}
	for _, t := range i.Config.Tools {
		if t.Name != "" {
			put(t)
		}
	}

	tdir := i.ToolsDirAbs()
	man := filepath.Join(tdir, conventions.ManifestFile)
	if data, err := os.ReadFile(man); err == nil {
		if root := jsonx.AsObject(mustParse(data)); root != nil {
			for _, o := range jsonx.GetArr(root, "tools") {
				if to := jsonx.AsObject(o); to != nil {
					t := config.ToolFrom(to)
					if t.Name != "" {
						put(t)
					}
				}
			}
		}
	}
	if scripts, err := filepath.Glob(filepath.Join(tdir, "*.ps1")); err == nil {
		for _, f := range scripts {
			name := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
			if _, seen := byName[strings.ToLower(name)]; !seen {
				put(config.Tool{Name: name, Extra: map[string]any{"script": f}})
			}
		}
	}

	out := make([]config.Tool, 0, len(order))
	for _, k := range order {
		out = append(out, byName[k])
	}
	return out
}

// FindTool returns the named tool (case-insensitive), or nil.
func (i *Instance) FindTool(name string) *config.Tool {
	for _, t := range i.Tools() {
		if strings.EqualFold(t.Name, name) {
			tc := t
			return &tc
		}
	}
	return nil
}

// Resolve resolves a relative path inside the ratchet, refusing anything that escapes the root. It
// rejects absolute paths (including Windows-style on any OS) and `..` up front, then confirms the
// joined path is still under root. Works for not-yet-existing files (write).
func (i *Instance) Resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if isAbsLike(rel) {
		return "", fmt.Errorf("path '%s' must be relative to the ratchet dir", rel)
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("path '%s' may not contain '..'", rel)
		}
	}
	joined, err := filepath.Abs(filepath.Join(i.Root, rel))
	if err != nil {
		return "", fmt.Errorf("path '%s': %v", rel, err)
	}
	rootWithSep := strings.TrimRight(i.Root, `\/`) + string(os.PathSeparator)
	joinedWithSep := strings.TrimRight(joined, `\/`) + string(os.PathSeparator)
	if !strings.HasPrefix(strings.ToLower(joinedWithSep), strings.ToLower(rootWithSep)) {
		return "", fmt.Errorf("path '%s' escapes the ratchet dir", rel)
	}
	return joined, nil
}

// isAbsLike reports whether rel is an absolute path on the current OS, or Windows-style absolute
// (drive letter or leading backslash) on any OS - so a Linux build still rejects "C:\...".
func isAbsLike(rel string) bool {
	if filepath.IsAbs(rel) {
		return true
	}
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return true
	}
	if len(rel) >= 2 && rel[1] == ':' &&
		((rel[0] >= 'A' && rel[0] <= 'Z') || (rel[0] >= 'a' && rel[0] <= 'z')) {
		return true
	}
	return false
}

func (i *Instance) SchemaPath(table string) string {
	return filepath.Join(i.SchemasDirAbs(), table+".json")
}
func (i *Instance) SamplePath(table string) string {
	return filepath.Join(i.SamplesDirAbs(), table+".txt")
}
func (i *Instance) FlowPath(name string) string {
	return filepath.Join(i.FlowsDirAbs(), name+".json")
}

// ReadAt reads an absolute path (a composed read dir may sit outside the write sandbox).
func (i *Instance) ReadAt(absPath string) (string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %v", absPath, err)
	}
	return string(data), nil
}

// ReadFile reads a sandbox-relative path.
func (i *Instance) ReadFile(rel string) (string, error) {
	p, err := i.Resolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading %s: %v", p, err)
	}
	return string(data), nil
}

// WriteFile writes a sandbox-relative path, creating parent dirs.
func (i *Instance) WriteFile(rel, contents string) error {
	p, err := i.Resolve(rel)
	if err != nil {
		return err
	}
	if parent := filepath.Dir(p); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("writing %s: %v", p, err)
		}
	}
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("writing %s: %v", p, err)
	}
	return nil
}

// ReadEntry reads a KB entry by manifest id (model-facing grounding text); the routing metadata block
// is stripped so the model sees clean content.
func (i *Instance) ReadEntry(id string) (string, error) {
	if i.Manifest == nil {
		return "", fmt.Errorf("this ratchet has no manifest.json")
	}
	e := i.Manifest.GetEntry(id)
	if e == nil {
		return "", fmt.Errorf("no manifest entry '%s'", id)
	}
	text, err := i.ReadFile(e.Path)
	if err != nil {
		return "", err
	}
	return meta.StripMeta(text), nil
}

func mustParse(data []byte) any {
	v, err := jsonx.Parse(string(data))
	if err != nil {
		return nil
	}
	return v
}
