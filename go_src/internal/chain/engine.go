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
	"runtime"
	"strings"
	"time"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/meta"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/runrec"
	"github.com/scanset/Ratchet/internal/search"
	"github.com/scanset/Ratchet/internal/snapshot"
	"github.com/scanset/Ratchet/internal/tool"
	"github.com/scanset/Ratchet/internal/version"
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
	hardStepCap         = 100 // backstop when a chain declares no max_steps
	decideTimeout       = 60000
	maxDepth            = 8  // foreach nesting cap
	defaultSnapshotKeep = 10 // workspace snapshots retained per workspace (rollback depth)
)

// Engine runs chains for one instance, generating via gen.
type Engine struct {
	inst        *instance.Instance
	gen         Generator
	status      func(string)
	depth       int
	caller      string // console | mcp | cli (provenance, recorded in the run meta)
	parentRunID string // set on foreach sub-runs to link back to the parent run
}

// NewEngine builds a top-level engine.
func NewEngine(inst *instance.Instance, gen Generator, status func(string)) *Engine {
	return newEngine(inst, gen, status, 0)
}

func newEngine(inst *instance.Instance, gen Generator, status func(string), depth int) *Engine {
	if status == nil {
		status = func(string) {}
	}
	return &Engine{inst: inst, gen: gen, status: status, depth: depth, caller: "cli"}
}

// WithCaller tags the engine's provenance ("console", "mcp", "cli"); recorded in each run's meta.
func (e *Engine) WithCaller(c string) *Engine {
	if c != "" {
		e.caller = c
	}
	return e
}

// runContext holds the per-run state the records and rollback need.
type runContext struct {
	runID   string
	meta    runrec.Meta
	wsAbs   string
	wsName  string
	snapped bool
	startT  time.Time
	tok0    int64
	pTok0   int64
	eTok0   int64
	visits  map[string]int // generate node id -> execution count (for first-pass / repair metrics)
	gateN   int            // gated (action/foreach) executions
	gateOK  int            // gated executions that passed
	repairs int            // generate re-executions (repair_index > 0)
	stepN   int            // last step number written
}

// recordStep fills a step's index/timing fields and writes step-NNN.json.
func (e *Engine) recordStep(rc *runContext, n int, start time.Time, st runrec.Step) {
	st.Index = n
	st.Started = start.Format(time.RFC3339)
	st.DurationMS = time.Since(start).Milliseconds()
	_ = runrec.WriteStep(e.inst, rc.runID, st)
	rc.stepN = n
}

func boolExit(ok bool) int {
	if ok {
		return 0
	}
	return 1
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
	rc := &runContext{
		runID:  runrec.UniqueRunID(e.inst, now),
		wsAbs:  workspace,
		startT: now,
		tok0:   ollama.MeterTotal(),
		pTok0:  ollama.MeterPrompt(),
		eTok0:  ollama.MeterEval(),
		visits: map[string]int{},
	}
	if workspace != "" {
		rc.wsName = filepath.Base(workspace)
	}
	// Snapshot the workspace before any step writes (top-level runs only; sub-runs are covered by
	// the parent's snapshot). A snapshot failure disables rollback for this run but does not fail it.
	if e.depth == 0 && workspace != "" {
		if err := snapshot.Snapshot(rc.wsAbs, snapshot.SnapshotDirAbs(e.inst, rc.runID)); err == nil {
			rc.snapped = true
		} else {
			e.status("snapshot skipped: " + err.Error())
		}
	}
	rc.meta = runrec.Meta{
		RunID:         rc.runID,
		Kind:          runrec.KindFlow,
		ParentRunID:   e.parentRunID,
		Ratchet:       e.inst.Config.Name,
		EngineVersion: version.Version,
		ChainID:       c.ID,
		Caller:        e.caller,
		Workspace:     rc.wsName,
		Input:         cap16(input),
		InputSHA256:   runrec.Sha256Hex([]byte(input)),
		ModelSeats:    runrec.Seats{Generate: e.inst.Config.Models.Generate, Dispatch: e.inst.Config.Models.Dispatch, Embed: e.inst.Config.Models.Embed},
		OllamaHost:    e.gen.URL(),
		OSArch:        runtime.GOOS + "/" + runtime.GOARCH,
		Started:       now.Format(time.RFC3339),
	}
	_ = runrec.WriteMeta(e.inst, rc.meta)

	maxSteps := c.MaxSteps
	if maxSteps <= 0 {
		maxSteps = hardStepCap
	}
	lastOutput := ""
	step := c.Entry
	n := 0

	for step != "" {
		if n >= maxSteps {
			res.IsError = true
			res.Outcome = fmt.Sprintf("aborted: max_steps (%d)", maxSteps)
			break
		}
		if c.MaxTokens > 0 && (ollama.MeterTotal()-rc.tok0) > int64(c.MaxTokens) {
			res.IsError = true
			res.Outcome = "aborted: max_tokens"
			break
		}
		if c.MaxWallclock > 0 && time.Since(rc.startT).Seconds() > c.MaxWallclock {
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
		stepStart := time.Now()

		switch a.Kind {
		case conventions.ActionKindExit:
			res.Outcome = orElse(a.Outcome, "success")
			e.recordStep(rc, n, stepStart, runrec.Step{Node: a.ID, Kind: a.Kind, Outcome: res.Outcome})
			return e.finish(&res, rc, c, "", lastOutput)

		case conventions.ActionKindGenerate:
			slots := e.resolveSlots(a, state)
			gp := render(e.readPrompt(a), slots)
			pB, eB := ollama.MeterPrompt(), ollama.MeterEval()
			var outp string
			if a.OutputSchema != nil {
				jv, err := ollama.GenerateJSON(e.gen.URL(), e.inst.Config.Models.Generate, gp, a.OutputSchema, 0.2, decideTimeout, nil)
				if err != nil {
					res.IsError = true
					res.Outcome = "aborted: " + err.Error()
					return e.fail(&res, rc, c, lastOutput)
				}
				outp = jsonx.Serialize(jv)
			} else {
				var err error
				outp, err = e.gen.Generate(gp, 0.2)
				if err != nil {
					res.IsError = true
					res.Outcome = "aborted: " + err.Error()
					return e.fail(&res, rc, c, lastOutput)
				}
			}
			state[a.ID] = outp
			lastOutput = outp
			ri := rc.visits[a.ID]
			rc.visits[a.ID]++
			if ri > 0 {
				rc.repairs++
			}
			pD, eD := int(ollama.MeterPrompt()-pB), int(ollama.MeterEval()-eB)
			e.recordStep(rc, n, stepStart, runrec.Step{
				Node: a.ID, Kind: a.Kind, Model: e.inst.Config.Models.Generate, RepairIndex: ri,
				Tokens: runrec.Tokens{Prompt: pD, Completion: eD, Total: pD + eD},
				Prompt: cap16(gp), Output: cap16(outp), OutputSHA256: runrec.Sha256Hex([]byte(outp)),
			})
			step = a.OnSuccess

		case conventions.ActionKindAiBranch:
			slots := e.resolveSlots(a, state)
			pB, eB := ollama.MeterPrompt(), ollama.MeterEval()
			next, err := e.decide(a, render(e.readPrompt(a), slots))
			if err != nil {
				res.IsError = true
				res.Outcome = "aborted: " + err.Error()
				return e.fail(&res, rc, c, lastOutput)
			}
			state[a.ID] = next
			pD, eD := int(ollama.MeterPrompt()-pB), int(ollama.MeterEval()-eB)
			e.recordStep(rc, n, stepStart, runrec.Step{
				Node: a.ID, Kind: a.Kind, Model: e.inst.Config.DispatchModel(), Next: next,
				Tokens: runrec.Tokens{Prompt: pD, Completion: eD, Total: pD + eD},
			})
			tgt, ok := a.Transitions[next]
			if !ok {
				res.IsError = true
				res.Outcome = "aborted: '" + a.ID + "' returned unroutable '" + next + "'"
				return e.fail(&res, rc, c, lastOutput)
			}
			step = tgt

		case conventions.ActionKindAction:
			slots := e.resolveSlots(a, state)
			ok, output := e.runActionNode(a, slots)
			state[a.ID] = output
			lastOutput = output
			rc.gateN++
			if ok {
				rc.gateOK++
			}
			e.recordStep(rc, n, stepStart, runrec.Step{
				Node: a.ID, Kind: a.Kind, Output: cap4(output),
				Oracle: &runrec.Oracle{Tool: a.Tool, ExitCode: boolExit(ok), OK: ok},
			})
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
			e.recordStep(rc, n, stepStart, runrec.Step{Node: a.ID, Kind: a.Kind, Output: cap4(state[a.ID])})
			step = a.OnSuccess

		case conventions.ActionKindForEach:
			slots := e.resolveSlots(a, state)
			ok, out := e.runForeach(a, slots, c, state, rc)
			state[a.ID] = out
			lastOutput = out
			rc.gateN++
			if ok {
				rc.gateOK++
			}
			e.recordStep(rc, n, stepStart, runrec.Step{
				Node: a.ID, Kind: a.Kind, Output: cap4(out),
				Oracle: &runrec.Oracle{Tool: "foreach:" + a.Flow, ExitCode: boolExit(ok), OK: ok},
			})
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

	return e.finish(&res, rc, c, step, lastOutput)
}

// finish records the terminal state + metrics, writes outcome.json + the change manifest + the index
// entry, then prunes old snapshots. Falling off the graph (step=="" with no outcome) is an error.
func (e *Engine) finish(res *Result, rc *runContext, c *model.Chain, step, lastOutput string) Result {
	res.Steps = rc.stepN
	if step == "" && res.Outcome == "" {
		res.Outcome = "aborted: chain ended without reaching an exit node"
		res.IsError = true
	}
	if lastOutput != "" {
		res.Text = lastOutput
	} else {
		res.Text = fmt.Sprintf("[chain %s -> %s, %d step(s)]", c.ID, res.Outcome, rc.stepN)
	}

	genNodes := len(rc.visits)
	firstPass := 0
	for _, v := range rc.visits {
		if v == 1 {
			firstPass++
		}
	}
	passRate := 0.0
	if rc.gateN > 0 {
		passRate = float64(rc.gateOK) / float64(rc.gateN)
	}

	var changes []runrec.Change
	rollbackable := false
	snapRel := ""
	if rc.snapped {
		changes, _ = snapshot.Diff(snapshot.SnapshotDirAbs(e.inst, rc.runID), rc.wsAbs)
		_ = runrec.WriteChanges(e.inst, rc.runID, changes)
		rollbackable = true
		snapRel = snapshot.SnapshotRel(rc.runID)
	}

	abort := ""
	if strings.HasPrefix(res.Outcome, "aborted") {
		abort = res.Outcome
	}

	dur := time.Since(rc.startT).Milliseconds()
	o := runrec.Outcome{
		Outcome:        res.Outcome,
		Finished:       time.Now().Format(time.RFC3339),
		DurationMS:     dur,
		Steps:          rc.stepN,
		Error:          res.IsError,
		AbortReason:    abort,
		Tokens:         runrec.Tokens{Prompt: int(ollama.MeterPrompt() - rc.pTok0), Completion: int(ollama.MeterEval() - rc.eTok0), Total: int(ollama.MeterTotal() - rc.tok0)},
		RepairCount:    rc.repairs,
		FirstPassSteps: firstPass,
		GenerateSteps:  genNodes,
		OraclePassRate: passRate,
		ChangedFiles:   len(changes),
		Rollbackable:   rollbackable,
		SnapshotPath:   snapRel,
	}
	_ = runrec.WriteOutcome(e.inst, rc.runID, o)
	_ = runrec.AppendIndex(e.inst, runrec.IndexEntry{
		RunID:        rc.runID,
		Time:         rc.meta.Started,
		Kind:         rc.meta.Kind,
		Chain:        c.ID,
		Workspace:    rc.wsName,
		Outcome:      res.Outcome,
		DurationMS:   dur,
		TokensTotal:  o.Tokens.Total,
		ChangedFiles: o.ChangedFiles,
		Rollbackable: rollbackable,
	})
	if rc.snapped && rc.wsName != "" {
		_ = snapshot.Prune(e.inst, rc.wsName, defaultSnapshotKeep)
	}
	return *res
}

func (e *Engine) fail(res *Result, rc *runContext, c *model.Chain, lastOutput string) Result {
	return e.finish(res, rc, c, "", lastOutput)
}

func (e *Engine) runForeach(a model.ActionNode, slots map[string]string, c *model.Chain, state map[string]string, rc *runContext) (bool, string) {
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
			if c.MaxTokens > 0 && (ollama.MeterTotal()-rc.tok0) > int64(c.MaxTokens) {
				out.WriteString("aborted: max_tokens (foreach)\n")
				failCount++
				break
			}
			if c.MaxWallclock > 0 && time.Since(rc.startT).Seconds() > c.MaxWallclock {
				out.WriteString("aborted: max_wallclock (foreach)\n")
				failCount++
				break
			}
			itemInput := render(orElse(a.ItemInput, "{{ item }}"), map[string]string{"item": item})
			e.status("foreach " + a.ID + ": " + item)
			se := newEngine(e.inst, e.gen, e.status, e.depth+1)
			se.caller = e.caller
			se.parentRunID = rc.runID
			r := se.Run(sub, itemInput, ws)
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
	lib := render(ib.Lib, slots) // target library may be a slot (e.g. "{{kb}}"), resolved at runtime
	dir := e.libDir(lib)
	if dir == "" {
		return ""
	}
	q := render(ib.Query, slots)
	if strings.TrimSpace(q) == "" {
		return ""
	}
	cacheKey := ""
	if kb := e.inst.Knowledge().Find(lib); kb != nil {
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
