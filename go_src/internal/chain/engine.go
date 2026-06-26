// ChainEngine runs an action chain (filesystem-as-graph). The loop reads one node per step, resolves
// its declared inputs into slots (from a prior node, a fixed ref, or a search injection), runs the
// node, and follows the edge - under the chain's budgets, writing run state to runs/<id>/. The model
// proposes (a decision edge, or generated text); the host executes. A node sees ONLY its declared
// inputs (no cumulative tape). Port of src.bak/Runtime/ChainEngine.cs.
package chain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/meta"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/search"
	"github.com/scanset/Ratchet/internal/tool"
)

// Generator is what the engine needs from the dispatcher: the Ollama URL and a streaming-aware
// freeform generate. Defining it here (and having Dispatcher satisfy it) breaks the dispatch<->chain
// import cycle the flat C# namespace did not have.
type Generator interface {
	URL() string
	Generate(prompt string, temperature float64) (string, error)
}

// Result is the outcome of running a chain.
type Result struct {
	Outcome string
	Text    string
	Steps   int
	IsError bool
}

const (
	hardStepCap   = 100 // backstop when a chain declares no max_steps
	decideTimeout = 60000
	maxDepth      = 8 // foreach nesting cap
)

// Engine runs chains for one instance, generating via gen.
type Engine struct {
	inst   *instance.Instance
	gen    Generator
	status func(string)
	depth  int
}

// NewEngine builds a top-level engine.
func NewEngine(inst *instance.Instance, gen Generator, status func(string)) *Engine {
	return newEngine(inst, gen, status, 0)
}

func newEngine(inst *instance.Instance, gen Generator, status func(string), depth int) *Engine {
	if status == nil {
		status = func(string) {}
	}
	return &Engine{inst: inst, gen: gen, status: status, depth: depth}
}

// Run executes chain c with the given input and active workspace.
func (e *Engine) Run(c *model.Chain, input, workspace string) Result {
	var res Result
	if e.depth > maxDepth {
		res.IsError = true
		res.Outcome = fmt.Sprintf("aborted: max recursion depth (%d)", maxDepth)
		return res
	}
	state := map[string]string{}
	state["$input"] = input
	state["$workspace"] = workspace
	splitInputs(c.Inputs, state["$input"], state)

	now := time.Now()
	runID := now.Format("20060102-150405") + fmt.Sprintf("-%03d", now.Nanosecond()/1e6)
	e.writeState(runID, "meta.json", jsonx.Obj("chain", c.ID, "workspace", state["$workspace"], "input", state["$input"], "started", now.Format("2006-01-02T15:04:05")))

	maxSteps := c.MaxSteps
	if maxSteps <= 0 {
		maxSteps = hardStepCap
	}
	tok0 := ollama.MeterTotal()
	startT := time.Now()
	lastOutput := ""
	step := c.Entry
	n := 0

	for step != "" {
		if n >= maxSteps {
			res.IsError = true
			res.Outcome = fmt.Sprintf("aborted: max_steps (%d)", maxSteps)
			break
		}
		if c.MaxTokens > 0 && (ollama.MeterTotal()-tok0) > int64(c.MaxTokens) {
			res.IsError = true
			res.Outcome = "aborted: max_tokens"
			break
		}
		if c.MaxWallclock > 0 && time.Since(startT).Seconds() > c.MaxWallclock {
			res.IsError = true
			res.Outcome = "aborted: max_wallclock"
			break
		}

		a, ok := c.Actions[step]
		if !ok {
			res.IsError = true
			res.Outcome = "aborted: missing node '" + step + "'"
			break
		}
		n++
		e.status(fmt.Sprintf("step %d: %s (%s)", n, a.ID, a.Kind))
		stepFile := fmt.Sprintf("step-%03d.json", n)

		switch a.Kind {
		case conventions.ActionKindExit:
			res.Outcome = orElse(a.Outcome, "success")
			e.writeState(runID, stepFile, jsonx.Obj("node", a.ID, "kind", a.Kind, "outcome", res.Outcome))
			step = ""
			// reached an exit: stop the loop with a recorded outcome
			e.finish(&res, c, step, n, lastOutput, runID)
			return res

		case conventions.ActionKindGenerate:
			slots := e.resolveSlots(a, state)
			gp := render(e.readPrompt(a), slots)
			var outp string
			if a.OutputSchema != nil {
				jv, err := ollama.GenerateJSON(e.gen.URL(), e.inst.Config.Models.Generate, gp, a.OutputSchema, 0.2, decideTimeout, nil)
				if err != nil {
					res.IsError = true
					res.Outcome = "aborted: " + err.Error()
					return e.fail(&res, c, n, lastOutput, runID)
				}
				outp = jsonx.Serialize(jv)
			} else {
				var err error
				outp, err = e.gen.Generate(gp, 0.2)
				if err != nil {
					res.IsError = true
					res.Outcome = "aborted: " + err.Error()
					return e.fail(&res, c, n, lastOutput, runID)
				}
			}
			state[a.ID] = outp
			lastOutput = outp
			e.writeState(runID, stepFile, jsonx.Obj("node", a.ID, "kind", a.Kind, "prompt", cap16(gp), "output", cap16(outp)))
			step = a.OnSuccess

		case conventions.ActionKindAiBranch:
			slots := e.resolveSlots(a, state)
			next, err := e.decide(a, render(e.readPrompt(a), slots))
			if err != nil {
				res.IsError = true
				res.Outcome = "aborted: " + err.Error()
				return e.fail(&res, c, n, lastOutput, runID)
			}
			state[a.ID] = next
			e.writeState(runID, stepFile, jsonx.Obj("node", a.ID, "kind", a.Kind, "next", next))
			tgt, ok := a.Transitions[next]
			if !ok {
				res.IsError = true
				res.Outcome = "aborted: '" + a.ID + "' returned unroutable '" + next + "'"
				return e.fail(&res, c, n, lastOutput, runID)
			}
			step = tgt

		case conventions.ActionKindAction:
			slots := e.resolveSlots(a, state)
			ok, output := e.runActionNode(a, slots)
			state[a.ID] = output
			lastOutput = output
			e.writeState(runID, stepFile, jsonx.Obj("node", a.ID, "kind", a.Kind, "ok", ok, "output", cap4(output)))
			if ok {
				step = a.OnSuccess
			} else {
				step = a.OnFailure
			}

		case conventions.ActionKindSummarizer:
			slots := e.resolveSlots(a, state)
			var sb strings.Builder
			for _, k := range orderedSlotKeys(a) {
				if v, ok := slots[k]; ok {
					sb.WriteString(k + ": " + v + "\n")
				}
			}
			state[a.ID] = strings.TrimRight(sb.String(), "\n")
			e.writeState(runID, stepFile, jsonx.Obj("node", a.ID, "kind", a.Kind, "output", cap4(state[a.ID])))
			step = a.OnSuccess

		case conventions.ActionKindForEach:
			slots := e.resolveSlots(a, state)
			ok, out := e.runForeach(a, slots, c, state, tok0, startT)
			state[a.ID] = out
			lastOutput = out
			e.writeState(runID, stepFile, jsonx.Obj("node", a.ID, "kind", a.Kind, "ok", ok, "output", cap4(out)))
			if ok {
				step = a.OnSuccess
			} else {
				step = a.OnFailure
			}

		default:
			res.IsError = true
			res.Outcome = "aborted: unknown kind '" + a.Kind + "'"
			step = ""
		}
	}

	return e.finish(&res, c, step, n, lastOutput, runID)
}

// finish records the terminal state and fills res.Text/Steps. Falling off the graph is an error.
func (e *Engine) finish(res *Result, c *model.Chain, step string, n int, lastOutput, runID string) Result {
	res.Steps = n
	if step == "" && res.Outcome == "" {
		res.Outcome = "aborted: chain ended without reaching an exit node"
		res.IsError = true
	}
	if lastOutput != "" {
		res.Text = lastOutput
	} else {
		res.Text = fmt.Sprintf("[chain %s -> %s, %d step(s)]", c.ID, res.Outcome, n)
	}
	e.writeState(runID, "outcome.json", jsonx.Obj("outcome", res.Outcome, "steps", res.Steps, "error", res.IsError))
	return *res
}

func (e *Engine) fail(res *Result, c *model.Chain, n int, lastOutput, runID string) Result {
	return e.finish(res, c, "", n, lastOutput, runID)
}

func (e *Engine) runForeach(a model.ActionNode, slots map[string]string, c *model.Chain, state map[string]string, tok0 int64, startT time.Time) (bool, string) {
	listText := ""
	if a.Over != "" {
		if lv, ok := slots[a.Over]; ok {
			listText = lv
		}
	}
	rawItems := strings.Split(strings.ReplaceAll(listText, "\r\n", "\n"), "\n")
	subDir := filepath.Join(e.inst.FlowsDirAbs(), a.Flow)
	ws := state["$workspace"]
	failCount := 0
	var out strings.Builder

	sub, err := model.LoadChain(subDir)
	if !model.IsChainDir(subDir) || err != nil {
		out.WriteString("no sub-flow '" + a.Flow + "'")
		failCount++
	} else {
		for _, rawItem := range rawItems {
			item := strings.TrimSpace(rawItem)
			if item == "" {
				continue
			}
			if c.MaxTokens > 0 && (ollama.MeterTotal()-tok0) > int64(c.MaxTokens) {
				out.WriteString("aborted: max_tokens (foreach)\n")
				failCount++
				break
			}
			if c.MaxWallclock > 0 && time.Since(startT).Seconds() > c.MaxWallclock {
				out.WriteString("aborted: max_wallclock (foreach)\n")
				failCount++
				break
			}
			itemInput := render(orElse(a.ItemInput, "{{ item }}"), map[string]string{"item": item})
			e.status("foreach " + a.ID + ": " + item)
			r := newEngine(e.inst, e.gen, e.status, e.depth+1).Run(sub, itemInput, ws)
			if r.IsError || strings.HasPrefix(r.Outcome, "aborted") {
				failCount++
			}
			out.WriteString(item + " -> " + r.Outcome + "\n")
		}
	}
	return failCount == 0, strings.TrimRight(out.String(), "\n")
}

// --- slot resolution (declared-inputs-only context) ---

func (e *Engine) resolveSlots(a model.ActionNode, state map[string]string) map[string]string {
	slots := map[string]string{}
	for _, ib := range a.Inputs {
		if ib.As == "" {
			continue
		}
		val := ""
		switch ib.Source {
		case "from":
			val = applyPath(state[ib.From], ib.Path)
		case "ref":
			val = e.resolveRef(ib)
		case "search":
			val = e.resolveSearch(ib, slots)
		}
		if ib.MaxChars > 0 && len(val) > ib.MaxChars {
			val = val[:ib.MaxChars]
		}
		slots[ib.As] = val
	}
	return slots
}

func applyPath(raw, path string) string {
	if path == "" || path == "." {
		return raw
	}
	root, err := jsonx.Parse(raw)
	if err != nil {
		return ""
	}
	ptr := path
	if !strings.HasPrefix(ptr, "/") {
		ptr = "/" + ptr
	}
	node := jsonx.Pointer(root, ptr)
	if node == nil {
		return ""
	}
	if s, ok := node.(string); ok {
		return s
	}
	return jsonx.Serialize(node)
}

func (e *Engine) resolveRef(ib model.InputBinding) string {
	dir := e.libDir(ib.Lib)
	if dir == "" {
		return ""
	}
	rel := ib.RefPath
	if rel == "" && ib.ID != "" {
		for _, ent := range search.LoadManifestMap(dir) {
			if strings.EqualFold(ent.ID, ib.ID) {
				rel = ent.Path
				break
			}
		}
	}
	return readDocOrEmpty(dir, rel)
}

func (e *Engine) resolveSearch(ib model.InputBinding, slots map[string]string) string {
	dir := e.libDir(ib.Lib)
	if dir == "" {
		return ""
	}
	q := render(ib.Query, slots)
	if strings.TrimSpace(q) == "" {
		return ""
	}
	cacheKey := ""
	if kb := e.inst.Knowledge().Find(ib.Lib); kb != nil {
		cacheKey = kb.Name
	}
	k := ib.K
	if k <= 0 {
		k = 3
	}
	ranked := search.Rank(e.indexDir(), cacheKey, dir, q, k)
	var sb strings.Builder
	for _, rel := range ranked {
		doc := readDocOrEmpty(dir, rel)
		if doc != "" {
			sb.WriteString(doc + "\n\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (e *Engine) libDir(lib string) string {
	if lib == "" {
		return ""
	}
	if kb := e.inst.Knowledge().Find(lib); kb != nil {
		return kb.Path
	}
	if p, err := e.inst.Resolve(lib); err == nil {
		if isDir(p) {
			return p
		}
	}
	return ""
}

func (e *Engine) indexDir() string {
	return filepath.Join(e.inst.Root, conventions.IndexDir)
}

func readDocOrEmpty(dir, rel string) string {
	if rel == "" {
		return ""
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := readFile(p)
	if err != nil {
		return ""
	}
	return meta.StripMeta(data)
}

// --- node execution ---

func (e *Engine) decide(a model.ActionNode, prompt string) (string, error) {
	var schema any
	if a.OutputSchema != nil {
		schema = a.OutputSchema
	} else {
		schema = jsonx.Schema(jsonx.Obj("next", jsonx.StrProp()), "next")
	}
	v, err := ollama.GenerateJSON(e.gen.URL(), e.inst.Config.DispatchModel(), prompt, schema, 0.1, decideTimeout, nil)
	if err != nil {
		return "", err
	}
	return jsonx.GetStringOr(v, "next", ""), nil
}

func (e *Engine) runActionNode(a model.ActionNode, slots map[string]string) (bool, string) {
	t := e.inst.FindTool(a.Tool)
	if t == nil {
		return false, "[no such tool: " + a.Tool + "]"
	}
	args := map[string]any{}
	for k, v := range a.Body {
		args[k] = render(fmt.Sprintf("%v", v), slots)
	}
	rr := tool.Run(e.inst, *t, args)
	output := rr.Output
	if rr.Error != "" {
		output = rr.Error
	}
	ok := rr.Ok && rr.Error == "" && validate(a, output)
	return ok, output
}

func validate(a model.ActionNode, output string) bool {
	for _, v := range a.Validate {
		pass := true
		switch v.Predicate {
		case "is_non_empty":
			pass = strings.TrimSpace(output) != ""
		case "is_empty":
			pass = strings.TrimSpace(output) == ""
		case "exists":
			pass = true
		case "is_array":
			parsed := asParsed(output)
			_, a1 := parsed.([]any)
			pass = a1
		case "is_object":
			pass = jsonx.AsObject(asParsed(output)) != nil
		default:
			pass = true
		}
		if !pass {
			return false
		}
	}
	return true
}

func asParsed(s string) any {
	v, err := jsonx.Parse(s)
	if err != nil {
		return nil
	}
	return v
}

// --- helpers ---

func (e *Engine) readPrompt(a model.ActionNode) string {
	if a.PromptText != "" {
		return a.PromptText
	}
	if a.Prompt == "" || a.Dir == "" {
		return ""
	}
	rel := strings.ReplaceAll(strings.ReplaceAll(a.Prompt, "./", ""), `.\`, "")
	data, err := readFile(filepath.Join(a.Dir, rel))
	if err != nil {
		return ""
	}
	return data
}

func render(template string, slots map[string]string) string {
	if template == "" {
		return ""
	}
	return SlotRe.ReplaceAllStringFunc(template, func(m string) string {
		sub := SlotRe.FindStringSubmatch(m)
		if v, ok := slots[sub[1]]; ok {
			return v
		}
		return ""
	})
}

// orderedSlotKeys returns the slot names in the node's declared input order (summarizer concatenation).
func orderedSlotKeys(a model.ActionNode) []string {
	var keys []string
	for _, ib := range a.Inputs {
		if ib.As != "" {
			keys = append(keys, ib.As)
		}
	}
	return keys
}

// splitInputs splits $input into the chain's declared named slots: each leading name takes one
// whitespace token; the LAST name captures the remainder.
func splitInputs(names []string, input string, state map[string]string) {
	if len(names) == 0 {
		return
	}
	remaining := strings.TrimSpace(input)
	lead := len(names) - 1
	for i := 0; i < lead; i++ {
		sp := strings.IndexFunc(remaining, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' })
		if sp < 0 {
			state[names[i]] = remaining
			remaining = ""
		} else {
			state[names[i]] = remaining[:sp]
			remaining = strings.TrimLeft(remaining[sp:], " \t\n\r")
		}
	}
	state[names[len(names)-1]] = remaining
}

func (e *Engine) writeState(runID, file string, obj map[string]any) {
	_ = e.inst.WriteFile(conventions.RunsDir+"/"+runID+"/"+file, jsonx.SerializePretty(obj))
}

func orElse(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func cap16(s string) string { return capStr(s, 16000) }
func cap4(s string) string  { return capStr(s, 4000) }

func capStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + " ..."
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
