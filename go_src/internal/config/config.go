// Package config loads ratchet.json: the seam that swaps models and behaviour without touching engine
// code. It carries SEPARATE model seats (a small `dispatch` seat makes the one constrained routing
// decision; the heavy `generate` seat runs behind the oracle), the declared tools, the router policy,
// the knowledge bases, and the dir overrides. Port of src.bak/Model/Config.cs.
package config

import (
	"fmt"
	"os"

	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
)

// Models holds the three model seats.
type Models struct {
	Generate string // the heavy proposer: drafts code/rows/answers; runs behind the oracle
	Dispatch string // the small seat for the one constrained dispatch/route decision ("" falls back to Generate)
	Embed    string // the embedder: narrows candidates, never decides or generates
}

// Tool is a tool the instance exposes (over MCP, or to the local dispatcher). Kind selects the engine
// behaviour; Name/Description are how a caller sees it. Extra carries any per-tool fields.
type Tool struct {
	Name        string
	Kind        string
	Description string
	Extra       map[string]any
}

// ToolFrom builds a Tool from a parsed object, keeping unknown fields in Extra.
func ToolFrom(obj map[string]any) Tool {
	t := Tool{
		Name:        jsonx.GetStringOr(obj, "name", ""),
		Kind:        jsonx.GetStringOr(obj, "kind", ""),
		Description: jsonx.GetStringOr(obj, "description", ""),
		Extra:       map[string]any{},
	}
	for k, v := range obj {
		if k != "name" && k != "kind" && k != "description" {
			t.Extra[k] = v
		}
	}
	return t
}

// Command returns the explicit `command` argv if the tool declares one, else nil. The argv is run
// as-is (the ratchet author chose the interpreter), so it is already cross-platform.
func (t Tool) Command() []string {
	if c, ok := t.Extra["command"]; ok {
		var list []string
		for _, o := range jsonx.AsArr(c) {
			if o != nil {
				list = append(list, fmt.Sprintf("%v", o))
			}
		}
		if len(list) > 0 {
			return list
		}
	}
	return nil
}

// Script returns the `script` convenience field (a path dispatched to an interpreter by extension and
// host OS in internal/tool), or "".
func (t Tool) Script() string { return jsonx.GetStringOr(t.Extra, "script", "") }

// HasExec reports whether the tool declares something runnable (a command or a script).
func (t Tool) HasExec() bool { return t.Command() != nil || t.Script() != "" }

// StdinArg returns the argument name whose value is piped to the tool's stdin instead of argv ("" if
// none).
func (t Tool) StdinArg() string { return jsonx.GetStringOr(t.Extra, "stdin", "") }

// TimeoutMs returns the per-tool timeout in ms (config `timeout` is in seconds); default 60s.
func (t Tool) TimeoutMs() int {
	if v, ok := jsonx.GetNumber(t.Extra, "timeout"); ok {
		return int(v * 1000)
	}
	return 60000
}

// InputSchema returns the instance-authored JSON schema for the tool's arguments, or nil.
func (t Tool) InputSchema() any {
	if s, ok := t.Extra["inputSchema"]; ok {
		return s
	}
	return nil
}

// EnvVars returns optional environment overrides for the tool process.
func (t Tool) EnvVars() map[string]any { return jsonx.GetObject(t.Extra, "env") }

// Router is the conversational-router behaviour for the terminal console. "confirm" (default)
// proposes the inferred flow and asks; "on" auto-runs a high-confidence match; "off" disables routing.
type Router struct {
	Autorun string // confirm | on | off
}

// Enabled reports whether routing is on at all.
func (r Router) Enabled() bool { return r.Autorun == "confirm" || r.Autorun == "on" }

// AutoRunHigh reports whether a high-confidence match auto-runs.
func (r Router) AutoRunHigh() bool { return r.Autorun == "on" }

// Config is the loaded ratchet.json plus the launch wiring.
type Config struct {
	Name      string
	Domain    string
	Models    Models
	Router    Router
	OllamaURL string
	Tools     []Tool
	Oracle    any // opaque oracle config

	// launch wiring
	SourcePath     string // the config file this loaded from; relative dir refs resolve against its folder
	Workdir        string // the write/sandbox root (default: the config file's folder)
	FlowsDir       string // dir overrides; "" = the conventional <workdir>/<name>
	ToolsDir       string
	SchemasDir     string
	SamplesDir     string
	WorkspacesDir  string // projects container; may point anywhere (a write location)
	KnowledgeBases []model.KnowledgeBase
	Requirements   any // opaque preflight checks the host's doctor validates
}

// Load reads and parses a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", path, err)
	}
	parsed, err := jsonx.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %v", path, err)
	}
	root := jsonx.AsObject(parsed)
	if root == nil {
		return nil, fmt.Errorf("parsing %s: not a JSON object", path)
	}

	c := &Config{
		SourcePath: path,
		Name:       jsonx.GetStringOr(root, "name", ""),
		Domain:     jsonx.GetStringOr(root, "domain", ""),
		Router:     Router{Autorun: "confirm"},
		OllamaURL:  "http://localhost:11434",
	}
	c.Models = resolveModels(root)
	if ro := jsonx.GetObject(root, "router"); ro != nil {
		c.Router.Autorun = jsonx.GetStringOr(ro, "autorun", c.Router.Autorun)
	}
	c.OllamaURL = jsonx.GetStringOr(root, "ollama_url", c.OllamaURL)
	for _, t := range jsonx.GetArr(root, "tools") {
		if to := jsonx.AsObject(t); to != nil {
			c.Tools = append(c.Tools, ToolFrom(to))
		}
	}
	c.Workdir = jsonx.GetStringOr(root, "workdir", "")
	c.FlowsDir = jsonx.GetStringOr(root, "flowsDir", "")
	c.ToolsDir = jsonx.GetStringOr(root, "toolsDir", "")
	c.SchemasDir = jsonx.GetStringOr(root, "schemasDir", "")
	c.SamplesDir = jsonx.GetStringOr(root, "samplesDir", "")
	c.WorkspacesDir = jsonx.GetStringOr(root, "workspacesDir", "")
	c.KnowledgeBases = model.LoadKnowledgeList(jsonx.GetArr(root, "knowledgeBases"))
	if v, ok := root["oracle"]; ok {
		c.Oracle = v
	}
	if v, ok := root["requirements"]; ok {
		c.Requirements = v
	}
	return c, nil
}

// Default returns a config for a directory with no ratchet.json (keeps the host forgiving).
func Default(name string) *Config {
	n := name
	if n == "" {
		n = "(unnamed)"
	}
	return &Config{
		Name:      n,
		Models:    Models{Generate: "qwen3-coder:latest"},
		Router:    Router{Autorun: "confirm"},
		OllamaURL: "http://localhost:11434",
	}
}

// resolveModels resolves the model seats with a COMPAT SHIM: prefer nested models.{...}, but fall
// back to the flat model / embed_model / dispatch_model fields the Python ICMs use.
func resolveModels(root map[string]any) Models {
	m := Models{Generate: "qwen3-coder:latest"}
	mo := jsonx.GetObject(root, "models")

	gen, ok := nestedOrFlat(mo, "generate", root, "model")
	if ok && gen != "" {
		m.Generate = gen
	}
	dis, _ := nestedOrFlat(mo, "dispatch", root, "dispatch_model")
	m.Dispatch = dis
	emb, _ := nestedOrFlat(mo, "embed", root, "embed_model")
	m.Embed = emb
	return m
}

// nestedOrFlat returns the nested models.<nestedKey> if present, else the flat root.<flatKey>.
func nestedOrFlat(mo map[string]any, nestedKey string, root map[string]any, flatKey string) (string, bool) {
	if mo != nil {
		if v, ok := jsonx.GetString(mo, nestedKey); ok {
			return v, true
		}
	}
	if v, ok := jsonx.GetString(root, flatKey); ok {
		return v, true
	}
	return "", false
}

// DispatchModel returns the dispatch seat, falling back to the generate seat when unset.
func (c *Config) DispatchModel() string {
	if c.Models.Dispatch == "" {
		return c.Models.Generate
	}
	return c.Models.Dispatch
}
