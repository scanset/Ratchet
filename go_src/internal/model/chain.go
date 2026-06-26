// Chain / ActionNode - the data model for an action-chain flow (filesystem-as-graph):
// flows/<chain>/chain.json (the graph) + actions/<a>/{action.json, prompt.md} (the nodes). This is
// the loader + types; the run engine and linter live in internal/chain. Port of src.bak/Model/Chain.cs.
package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/jsonx"
)

// Validator is a response predicate on an `action` node (the side-effect's success check).
type Validator struct {
	Path      string
	Predicate string
}

// ParseValidator builds a Validator from a parsed object.
func ParseValidator(o map[string]any) Validator {
	return Validator{
		Path:      jsonx.GetStringOr(o, "path", ""),
		Predicate: jsonx.GetStringOr(o, "predicate", ""),
	}
}

// InputBinding is one slot bound into a node's prompt/body. Source is exactly one of from/ref/search.
type InputBinding struct {
	As       string
	Source   string // "from" | "ref" | "search"
	From     string // from: prior-node id
	Path     string // from: jq path
	Lib      string // ref/search: knowledge library name
	ID       string // ref: entry id
	RefPath  string // ref: entry path
	Query    string // search: templated query
	K        int    // search: top-k
	MaxChars int    // injected-slot cap (0 = none)
}

// ParseInputBinding builds an InputBinding from a parsed object.
func ParseInputBinding(o map[string]any) InputBinding {
	b := InputBinding{As: jsonx.GetStringOr(o, "as", ""), K: 3}
	if mc, ok := jsonx.GetNumber(o, "max_chars"); ok {
		b.MaxChars = int(mc)
	}
	if f, ok := jsonx.GetString(o, "from"); ok {
		b.Source = "from"
		b.From = f
		b.Path = jsonx.GetStringOr(o, "path", ".")
	} else if r, ok := jsonx.GetString(o, "ref"); ok {
		b.Source = "ref"
		b.Lib = r
		b.ID = jsonx.GetStringOr(o, "id", "")
		b.RefPath = jsonx.GetStringOr(o, "path", "")
	} else if s, ok := jsonx.GetString(o, "search"); ok {
		b.Source = "search"
		b.Lib = s
		b.Query = jsonx.GetStringOr(o, "query", "")
		if k, ok := jsonx.GetNumber(o, "k"); ok {
			b.K = int(k)
		}
	}
	return b
}

// ActionNode is one node in a chain.
type ActionNode struct {
	ID     string
	Kind   string // action | generate | ai_branch | summarizer | foreach | exit
	Dir    string // the action's folder (abs); resolves prompt.md
	Inputs []InputBinding
	// action
	Tool      string
	Endpoint  string
	Body      map[string]any
	Validate  []Validator
	OnSuccess string
	OnFailure string
	// generate / ai_branch
	Prompt       string         // ./prompt.md (read from Dir at runtime)
	PromptText   string         // inline prompt body; used instead of reading Prompt (in-memory chains / tests)
	OutputSchema map[string]any //
	Transitions  map[string]string
	// exit
	Outcome string
	// foreach
	Over      string
	Flow      string
	ItemInput string
	Extra     map[string]any
}

// ParseActionNode builds an ActionNode from a parsed object.
func ParseActionNode(o map[string]any) ActionNode {
	a := ActionNode{
		ID:           jsonx.GetStringOr(o, "id", ""),
		Kind:         jsonx.GetStringOr(o, "kind", ""),
		Tool:         jsonx.GetStringOr(o, "tool", ""),
		Endpoint:     jsonx.GetStringOr(o, "endpoint", ""),
		Body:         jsonx.GetObject(o, "body"),
		OnSuccess:    jsonx.GetStringOr(o, "on_success", ""),
		OnFailure:    jsonx.GetStringOr(o, "on_failure", ""),
		Prompt:       jsonx.GetStringOr(o, "prompt", ""),
		Over:         jsonx.GetStringOr(o, "over", ""),
		Flow:         jsonx.GetStringOr(o, "flow", ""),
		ItemInput:    jsonx.GetStringOr(o, "input", ""),
		OutputSchema: jsonx.GetObject(o, "output_schema"),
		Outcome:      jsonx.GetStringOr(o, "outcome", ""),
		Transitions:  map[string]string{},
		Extra:        map[string]any{},
	}
	for _, i := range jsonx.GetArr(o, "inputs") {
		if io := jsonx.AsObject(i); io != nil {
			a.Inputs = append(a.Inputs, ParseInputBinding(io))
		}
	}
	for _, v := range jsonx.GetArr(o, "validate") {
		if vo := jsonx.AsObject(v); vo != nil {
			a.Validate = append(a.Validate, ParseValidator(vo))
		}
	}
	if tr := jsonx.GetObject(o, "transitions"); tr != nil {
		for k, v := range tr {
			if v != nil {
				a.Transitions[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	for k, v := range o {
		a.Extra[k] = v // keep extras (summarizer from/produce, model, ...)
	}
	return a
}

// LoadActionNode reads and parses an action.json, recording its folder in Dir.
func LoadActionNode(path string) (ActionNode, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ActionNode{}, fmt.Errorf("reading action %s: %v", path, err)
	}
	parsed, err := jsonx.Parse(string(data))
	if err != nil {
		return ActionNode{}, fmt.Errorf("parsing action %s: %v", path, err)
	}
	o := jsonx.AsObject(parsed)
	if o == nil {
		return ActionNode{}, fmt.Errorf("parsing action %s: not a JSON object", path)
	}
	a := ParseActionNode(o)
	abs, _ := filepath.Abs(path)
	a.Dir = filepath.Dir(abs)
	return a, nil
}

// Edges returns the next-node ids this node can transition to.
func (a ActionNode) Edges() []string {
	var e []string
	if a.Kind == conventions.ActionKindAiBranch {
		for _, v := range a.Transitions {
			e = append(e, v)
		}
	} else {
		if a.OnSuccess != "" {
			e = append(e, a.OnSuccess)
		}
		if a.OnFailure != "" {
			e = append(e, a.OnFailure)
		}
	}
	return e
}

// Chain is the loaded graph: budgets, declared node ids, and the action nodes keyed by id.
type Chain struct {
	ID           string
	Version      string
	Entry        string
	Summary      string   // routing text for /route
	Inputs       []string // named slots $input is split into (head/tail)
	MaxSteps     int
	MaxTokens    int
	MaxWallclock float64
	NodeIds      []string
	Actions      map[string]ActionNode
	Dir          string
}

// LoadChain reads flows/<chain>/chain.json and the actions/*/action.json nodes under chainDir.
func LoadChain(chainDir string) (*Chain, error) {
	c := &Chain{Actions: map[string]ActionNode{}}
	c.Dir, _ = filepath.Abs(chainDir)
	cj := filepath.Join(c.Dir, "chain.json")
	data, err := os.ReadFile(cj)
	if err != nil {
		return nil, fmt.Errorf("reading chain %s: %v", cj, err)
	}
	parsed, err := jsonx.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing chain %s: %v", cj, err)
	}
	o := jsonx.AsObject(parsed)
	if o == nil {
		return nil, fmt.Errorf("parsing chain %s: not a JSON object", cj)
	}

	c.ID = jsonx.GetStringOr(o, "id", filepath.Base(strings.TrimRight(c.Dir, `\/`)))
	c.Version = jsonx.GetStringOr(o, "version", "")
	c.Entry = jsonx.GetStringOr(o, "entry", "")
	c.Summary = jsonx.GetStringOr(o, "summary", jsonx.GetStringOr(o, "whenToUse", ""))
	for _, i := range jsonx.GetArr(o, "inputs") {
		if i != nil {
			c.Inputs = append(c.Inputs, fmt.Sprintf("%v", i))
		}
	}
	if b := jsonx.GetObject(o, "budgets"); b != nil {
		if ms, ok := jsonx.GetNumber(b, "max_steps"); ok {
			c.MaxSteps = int(ms)
		}
		if mt, ok := jsonx.GetNumber(b, "max_total_tokens"); ok {
			c.MaxTokens = int(mt)
		}
		if mw, ok := jsonx.GetNumber(b, "max_wallclock_seconds"); ok {
			c.MaxWallclock = mw
		}
	}
	for _, n := range jsonx.GetArr(o, "nodes") {
		if n != nil {
			c.NodeIds = append(c.NodeIds, fmt.Sprintf("%v", n))
		}
	}

	adir := filepath.Join(c.Dir, "actions")
	if st, err := os.Stat(adir); err == nil && st.IsDir() {
		var files []string
		_ = filepath.Walk(adir, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && info.Name() == "action.json" {
				files = append(files, p)
			}
			return nil
		})
		sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i]) < strings.ToLower(files[j]) })
		for _, af := range files {
			if a, err := LoadActionNode(af); err == nil && a.ID != "" {
				c.Actions[a.ID] = a
			}
		}
	}
	return c, nil
}

// IsChainDir reports whether dir is an action-chain (has chain.json).
func IsChainDir(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "chain.json"))
	return err == nil
}
