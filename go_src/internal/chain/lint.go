// Package chain runs and lints action chains (filesystem-as-graph). ChainLint is the author-time
// validator (this file); ChainEngine (added once its runtime deps land) executes a chain. Port of
// src.bak/Runtime/{ChainLint,ChainEngine}.cs.
//
// ChainLint catches what would otherwise be a runtime failure with a loose model inside an unbounded
// graph: unknown node kinds, chain.json nodes vs the action.json files on disk, every edge target is
// a declared node, ai_branch transitions == output_schema.next.enum, every inputs.from is a reachable
// predecessor, prompts exist and fit a rough token budget, and the silent empty-slot seam (a {{ slot }}
// a prompt or search query names must be bound, and a search's slots must be bound above it).
package chain

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
)

// SlotRe matches a {{ slot }} reference in a prompt/template. Shared by ChainLint and ChainEngine.
var SlotRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_\-]+)\s*\}\}`)

const (
	charsPerToken   = 4
	promptBodyLimit = 600 // tokens, per context-budget heuristic
)

// Lint validates an in-memory chain against the declared tool names. Returns the problem messages
// (empty when clean).
func Lint(c *model.Chain, toolNames []string) []string {
	var p []string
	if c == nil {
		return []string{"chain is null"}
	}
	if c.Entry == "" {
		p = append(p, "chain has no 'entry'")
	}

	onDisk := map[string]bool{}
	for id := range c.Actions {
		onDisk[id] = true
	}
	declared := map[string]bool{}
	for _, id := range c.NodeIds {
		declared[id] = true
	}
	for _, m := range sortedKeys(declared) {
		if !onDisk[m] {
			p = append(p, "declared node '"+m+"' has no action.json on disk")
		}
	}
	for _, x := range sortedKeys(onDisk) {
		if !declared[x] {
			p = append(p, "action '"+x+"' not declared in chain.json nodes")
		}
	}
	if c.Entry != "" && !onDisk[c.Entry] {
		p = append(p, "entry '"+c.Entry+"' has no action.json")
	}

	for _, id := range sortedActionKeys(c.Actions) {
		a := c.Actions[id]
		w := "node '" + a.ID + "'"
		if a.Kind == "" {
			p = append(p, w+": missing 'kind'")
			continue
		}
		if !containsStr(conventions.ActionKindAll, a.Kind) {
			p = append(p, w+": unknown kind '"+a.Kind+"'")
			continue
		}

		for _, tgt := range a.Edges() {
			if !onDisk[tgt] {
				p = append(p, w+": edge -> '"+tgt+"' is not a declared node")
			}
		}

		for _, ib := range a.Inputs {
			if ib.As == "" {
				p = append(p, w+": an input binding has no 'as'")
			}
			if ib.Source == "ref" && ib.Lib == "" {
				p = append(p, w+": ref binding has no library")
			}
			if ib.Source == "search" && ib.Lib == "" {
				p = append(p, w+": search binding has no library")
			}
			if ib.Source == "" {
				p = append(p, w+": input '"+ib.As+"' has no source (from/ref/search)")
			}
		}
		checkSearchRefs(a, w, &p)

		switch a.Kind {
		case conventions.ActionKindAction:
			if a.Tool == "" && a.Endpoint == "" {
				p = append(p, w+": action needs a 'tool' (or 'endpoint')")
			} else if a.Tool != "" && toolNames != nil && !containsStr(toolNames, a.Tool) {
				p = append(p, w+": references unknown tool '"+a.Tool+"'")
			}
			if a.OnSuccess == "" {
				p = append(p, w+": action needs 'on_success'")
			}
		case conventions.ActionKindGenerate:
			if a.Prompt == "" && a.PromptText == "" {
				p = append(p, w+": generate needs 'prompt'")
			}
			if a.OnSuccess == "" {
				p = append(p, w+": generate needs 'on_success'")
			}
			checkPrompt(a, w, &p)
		case conventions.ActionKindAiBranch:
			if a.Prompt == "" && a.PromptText == "" {
				p = append(p, w+": ai_branch needs 'prompt'")
			}
			if len(a.Transitions) < 2 {
				p = append(p, w+": ai_branch needs at least 2 transitions")
			}
			keys := keySet(a.Transitions)
			enumVals := nextEnum(a.OutputSchema)
			if !setEq(keys, enumVals) {
				p = append(p, w+": transitions keys {"+joinSet(keys)+"} != output_schema.next.enum {"+joinSet(enumVals)+"}")
			}
			checkPrompt(a, w, &p)
		case conventions.ActionKindForEach:
			if a.Flow == "" {
				p = append(p, w+": foreach needs 'flow' (the sub-chain to run per item)")
			}
			if a.Over == "" {
				p = append(p, w+": foreach needs 'over' (the slot holding the newline list)")
			}
			if a.OnSuccess == "" {
				p = append(p, w+": foreach needs 'on_success'")
			}
			if a.OnFailure == "" {
				p = append(p, w+": foreach needs 'on_failure'")
			}
		case conventions.ActionKindExit:
			if a.Outcome == "" {
				p = append(p, w+": exit needs 'outcome'")
			}
		}
	}

	// inputs.from must be a reachable predecessor (BFS from entry)
	order := bfs(c)
	for _, id := range sortedActionKeys(c.Actions) {
		a := c.Actions[id]
		co, haveCo := order[a.ID]
		for _, ib := range a.Inputs {
			if ib.Source != "from" || ib.From == "" {
				continue
			}
			// reserved run seeds ($input/$workspace) and chain-declared inputs are always available
			if ib.From == "$input" || ib.From == "$workspace" || containsStr(c.Inputs, ib.From) {
				continue
			}
			so, ok := order[ib.From]
			if !ok {
				p = append(p, "node '"+a.ID+"': inputs.from '"+ib.From+"' is not reachable from entry")
			} else if haveCo && so >= co {
				p = append(p, "node '"+a.ID+"': inputs.from '"+ib.From+"' is not a predecessor")
			}
		}
	}
	return p
}

// checkPrompt: token budget + the slot-reference contract: every {{ slot }} a prompt names must be a
// declared input binding, else Render substitutes "" and the model silently sees an empty slot.
func checkPrompt(a model.ActionNode, w string, p *[]string) {
	body := a.PromptText
	if body == "" {
		if a.Prompt == "" || a.Dir == "" {
			return // missing prompt already reported / no file to read
		}
		rel := strings.ReplaceAll(strings.ReplaceAll(a.Prompt, "./", ""), `.\`, "")
		path := filepath.Join(a.Dir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				*p = append(*p, w+": prompt file '"+a.Prompt+"' not found")
			}
			return
		}
		body = string(data)
	}
	tokens := (len(body) + charsPerToken - 1) / charsPerToken
	if tokens > promptBodyLimit {
		*p = append(*p, w+": prompt body "+strconv.Itoa(tokens)+" tokens > limit "+strconv.Itoa(promptBodyLimit))
	}

	bound := map[string]bool{}
	for _, ib := range a.Inputs {
		if ib.As != "" {
			bound[ib.As] = true
		}
	}
	for _, m := range SlotRe.FindAllStringSubmatch(body, -1) {
		slot := m[1]
		if !bound[slot] {
			*p = append(*p, w+": prompt references {{ "+slot+" }} but no input binds it (add an input with as: \""+slot+"\")")
		}
	}
}

// checkSearchRefs: a search query is rendered over slots resolved SO FAR (inputs walked top-to-bottom),
// so every slot a query names must be bound by an EARLIER input, else the query renders empty.
func checkSearchRefs(a model.ActionNode, w string, p *[]string) {
	seen := map[string]bool{}
	for _, ib := range a.Inputs {
		if ib.Source == "search" && ib.Query != "" {
			for _, m := range SlotRe.FindAllStringSubmatch(ib.Query, -1) {
				slot := m[1]
				if !seen[slot] {
					*p = append(*p, w+": search '"+ib.As+"' query references {{ "+slot+" }} but no earlier input binds it (a search sees only slots resolved above it)")
				}
			}
		}
		if ib.As != "" {
			seen[ib.As] = true
		}
	}
}

func bfs(c *model.Chain) map[string]int {
	order := map[string]int{}
	if c.Entry == "" {
		return order
	}
	if _, ok := c.Actions[c.Entry]; !ok {
		return order
	}
	q := []string{c.Entry}
	order[c.Entry] = 0
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		a, ok := c.Actions[cur]
		if !ok {
			continue
		}
		for _, n := range a.Edges() {
			if n != "" {
				if _, seen := order[n]; !seen {
					order[n] = order[cur] + 1
					q = append(q, n)
				}
			}
		}
	}
	return order
}

func nextEnum(schema map[string]any) map[string]bool {
	s := map[string]bool{}
	if schema == nil {
		return s
	}
	props := jsonx.GetObject(schema, "properties")
	if props == nil {
		return s
	}
	next := jsonx.GetObject(props, "next")
	if next == nil {
		return s
	}
	for _, e := range jsonx.GetArr(next, "enum") {
		if e != nil {
			s[toStr(e)] = true
		}
	}
	return s
}

// --- small helpers ---

func keySet(m map[string]string) map[string]bool {
	s := map[string]bool{}
	for k := range m {
		s[k] = true
	}
	return s
}

func setEq(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for x := range a {
		if !b[x] {
			return false
		}
	}
	return true
}

func joinSet(s map[string]bool) string {
	l := make([]string, 0, len(s))
	for x := range s {
		l = append(l, x)
	}
	sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i]) < strings.ToLower(l[j]) })
	return strings.Join(l, ",")
}

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	l := make([]string, 0, len(m))
	for k := range m {
		l = append(l, k)
	}
	sort.Strings(l)
	return l
}

func sortedActionKeys(m map[string]model.ActionNode) []string {
	l := make([]string, 0, len(m))
	for k := range m {
		l = append(l, k)
	}
	sort.Strings(l)
	return l
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
