// Package dispatch is the operator console's command layer, reused across the console REPL, the MCP
// server, and the flow engine. Plain text is UNGROUNDED chat (the model never picks an action from
// it). Acting is always an explicit slash command - /search, /route, /flow, /do, /propose, /ws. The
// model proposes into constrained slots; a deterministic gate/oracle decides; the operator drives.
// Port of src.bak/Runtime/Dispatcher.cs.
//
// A Dispatcher is single-threaded per use - one in-flight operation at a time. It satisfies
// chain.Generator (URL + Generate), which is how the chain engine generates without importing this
// package.
package dispatch

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/scanset/Ratchet/internal/chain"
	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/search"
)

const (
	dispatchTimeoutMs   = 60000
	genTimeoutMs        = 300000
	maxHistory          = 6
	maxProposeRepairs   = 4
	maxProblemsShown    = 40
	routeCandidateK     = 8
	routeManyCandidateK = 12
	doCommandTimeoutMs  = 120000
)

// Dispatcher routes operator input for one instance.
type Dispatcher struct {
	inst             *instance.Instance
	url              string
	status           func(string)
	history          []string
	cancel           *ollama.Cancel
	pendingFlowID    string
	pendingArgs      string
	pendingRedirect  string
	streamedThisTurn bool
	activeWorkspace  string

	// OnToken, when set (the console sets it), streams freeform generation tokens. Nil = non-streaming.
	OnToken func(string)
}

// New builds a dispatcher.
func New(inst *instance.Instance, url string, status func(string)) *Dispatcher {
	if status == nil {
		status = func(string) {}
	}
	return &Dispatcher{inst: inst, url: url, status: status}
}

// URL satisfies chain.Generator.
func (d *Dispatcher) URL() string { return d.url }

// Inst exposes the instance (for the MCP server and CLI).
func (d *Dispatcher) Inst() *instance.Instance { return d.inst }

// CancelCurrent aborts the in-flight operation's model call (best effort, safe from another thread).
func (d *Dispatcher) CancelCurrent() {
	if c := d.cancel; c != nil {
		c.Abort()
	}
}

func (d *Dispatcher) indexDir() string { return filepath.Join(d.inst.Root, conventions.IndexDir) }

// Turn runs one operator turn and returns a TurnResult (never panics for model/oracle failures).
func (d *Dispatcher) Turn(line string) model.TurnResult {
	r := model.TurnResult{}
	line = strings.TrimSpace(line)
	r.Standalone = line
	if line == "" {
		return r
	}
	d.cancel = ollama.NewCancel()
	d.streamedThisTurn = false

	// Resolve a pending router confirmation (a plain y/n answer to "Run the X flow?").
	if d.pendingFlowID != "" && line[0] != '/' {
		if isAffirmative(line) {
			id, args, rd := d.pendingFlowID, d.pendingArgs, d.pendingRedirect
			d.pendingFlowID, d.pendingArgs, d.pendingRedirect = "", "", ""
			r.Intent = "flow:" + id
			d.status("router: running '" + id + "'")
			d.runNamedFlow(id, args, &r)
			d.applyRedirect(&r, rd, "flow:"+id, args)
			return d.done(&r, line)
		}
		if isNegative(line) {
			d.pendingFlowID, d.pendingArgs, d.pendingRedirect = "", "", ""
			r.Intent = "chat"
			r.Text = "Cancelled."
			return d.done(&r, line)
		}
		d.pendingFlowID, d.pendingArgs, d.pendingRedirect = "", "", ""
	}

	if line[0] == '/' {
		d.runSlash(line, &r)
		if r.Intent != "clear" {
			return d.done(&r, line)
		}
		return r
	}

	// Plain text is ungrounded chat. A trailing "> path" still saves the reply.
	clean, redirect := parseRedirect(line)
	r.Intent = "chat"
	r.Text = d.doChat(clean)
	d.applyRedirect(&r, redirect, "chat", clean)
	return d.done(&r, line)
}

func (d *Dispatcher) done(r *model.TurnResult, line string) model.TurnResult {
	r.Streamed = d.streamedThisTurn
	d.remember("you: " + line)
	if r.IsError {
		d.remember("icm: " + r.Text)
	} else {
		d.remember("icm: " + truncate(r.Text, 400))
	}
	return *r
}

// Generate satisfies chain.Generator: streaming-aware freeform generation.
func (d *Dispatcher) Generate(prompt string, temperature float64) (string, error) {
	d.cancel = ollama.NewCancel()
	return d.generateMaybeStream(prompt, temperature)
}

func (d *Dispatcher) generateMaybeStream(prompt string, temperature float64) (string, error) {
	if d.OnToken != nil {
		d.streamedThisTurn = true
		return ollama.GenerateStream(d.url, d.inst.Config.Models.Generate, prompt, temperature, genTimeoutMs, d.OnToken, d.cancel)
	}
	return ollama.Generate(d.url, d.inst.Config.Models.Generate, prompt, nil, temperature, genTimeoutMs, d.cancel)
}

func isAffirmative(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "y", "yes", "yeah", "yep", "ok", "okay", "sure", "run", "do it", "go":
		return true
	}
	return false
}

func isNegative(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "n", "no", "nope", "cancel", "stop":
		return true
	}
	return false
}

// --- the conversational router ---

type routeResult struct {
	FlowID     string
	Args       string
	Confidence string
}

// GateDecision is the deterministic router gate's verdict.
type GateDecision int

const (
	// GateMatch means the proposal names an on-list flow with non-low confidence.
	GateMatch GateDecision = iota
	// GateFallback means the proposal is rejected.
	GateFallback
)

// Gate is the deterministic gate: a proposal only proceeds if it names an on-list flow with non-low
// confidence. Pure + exported so tests cover it without the model.
func Gate(flowID, confidence string, validIDs []string) GateDecision {
	if flowID == "" || flowID == "none" {
		return GateFallback
	}
	if !containsStr(validIDs, flowID) {
		return GateFallback
	}
	if confidence == "low" {
		return GateFallback
	}
	return GateMatch
}

func (d *Dispatcher) doRoute(line, redirect string, r *model.TurnResult) {
	d.status("route: matching a flow")
	rr := d.routeFlow(line)

	var ids []string
	for _, fi := range d.flowCatalog() {
		ids = append(ids, fi.ID)
	}

	if rr == nil || Gate(rr.FlowID, rr.Confidence, ids) == GateFallback {
		r.Intent = "route"
		r.Text = "No flow matched. Run one by name with /flow <name>, see /flows, or rephrase."
		return
	}

	if d.inst.Config.Router.AutoRunHigh() && rr.Confidence == "high" {
		r.Intent = "flow:" + rr.FlowID
		d.status("route: running '" + rr.FlowID + "' (high)")
		d.runNamedFlow(rr.FlowID, rr.Args, r)
		if redirect != "" {
			d.applyRedirect(r, redirect, "flow:"+rr.FlowID, rr.Args)
		} else {
			r.Text = "-> routed to `" + rr.FlowID + "` (high)\n\n" + r.Text
		}
		return
	}

	d.pendingFlowID = rr.FlowID
	d.pendingArgs = rr.Args
	d.pendingRedirect = redirect
	r.Intent = "route"
	argNote := ""
	if rr.Args != "" {
		argNote = " with: " + rr.Args
	}
	saveNote := ""
	if redirect != "" {
		saveNote = " (saves to " + redirect + ")"
	}
	r.Text = "This looks like the `" + rr.FlowID + "` flow (" + rr.Confidence + " confidence)" + argNote + saveNote +
		".\nRun it? (y / n) - or type a slash command instead."
}

func (d *Dispatcher) routeFlow(request string) *routeResult {
	flows := d.narrowFlows(request, d.flowCatalog(), routeCandidateK)
	if len(flows) == 0 {
		return nil
	}
	var ids, lines []string
	for _, fi := range flows {
		ids = append(ids, fi.ID)
		lines = append(lines, "- "+fi.ID+": "+fi.WhenToUse)
	}
	ids = append(ids, "none")

	schema := jsonx.Schema(jsonx.Obj(
		"flow_id", jsonx.EnumProp(ids),
		"args", jsonx.StrProp(),
		"confidence", jsonx.EnumProp([]string{"high", "medium", "low"})), "flow_id", "confidence")
	prompt := "Route the operator's request to ONE workflow, or 'none' if no workflow fits (a plain " +
		"question with no matching workflow is 'none'). Put the task/topic from their message in " +
		"args. Rate confidence honestly.\n\nWorkflows:\n" + strings.Join(lines, "\n") +
		"\n\nOperator: " + request + "\n\nReturn JSON {flow_id, args, confidence}."
	v, err := ollama.GenerateJSON(d.url, d.inst.Config.DispatchModel(), prompt, schema, 0.1, dispatchTimeoutMs, d.cancel)
	if err != nil {
		return nil
	}
	rr := &routeResult{
		FlowID:     jsonx.GetStringOr(v, "flow_id", "none"),
		Args:       jsonx.GetStringOr(v, "args", ""),
		Confidence: jsonx.GetStringOr(v, "confidence", "low"),
	}
	if rr.Args == "" {
		rr.Args = request
	}
	return rr
}

func (d *Dispatcher) narrowEntries(query string, entries []model.Entry, k int) []model.Entry {
	if len(entries) <= k {
		return entries
	}
	var cands []search.Cand
	for _, e := range entries {
		cands = append(cands, search.Cand{ID: e.ID, Text: e.Title + ". " + e.Summary + " " + strings.Join(e.Keywords, " ")})
	}
	top := search.RankTopK(d.indexDir(), d.url, d.inst.Config.Models.Embed, query, cands, k, d.status)
	if len(top) == 0 {
		return entries
	}
	byID := map[string]model.Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	var out []model.Entry
	for _, id := range top {
		if e, ok := byID[id]; ok {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return entries
	}
	d.status(fmt.Sprintf("route: embedding-narrowed to %d of %d entries", len(out), len(entries)))
	return out
}

func (d *Dispatcher) narrowFlows(query string, flows []model.FlowInfo, k int) []model.FlowInfo {
	if len(flows) <= k {
		return flows
	}
	var cands []search.Cand
	for _, fi := range flows {
		cands = append(cands, search.Cand{ID: fi.ID, Text: fi.Name + ". " + fi.WhenToUse})
	}
	top := search.RankTopK(d.indexDir(), d.url, d.inst.Config.Models.Embed, query, cands, k, d.status)
	if len(top) == 0 {
		return flows
	}
	byID := map[string]model.FlowInfo{}
	for _, fi := range flows {
		byID[fi.ID] = fi
	}
	var out []model.FlowInfo
	for _, id := range top {
		if fi, ok := byID[id]; ok {
			out = append(out, fi)
		}
	}
	if len(out) == 0 {
		return flows
	}
	return out
}

func (d *Dispatcher) flowCatalog() []model.FlowInfo {
	var out []model.FlowInfo
	dir := d.inst.FlowsDirAbs()
	subs, err := readDirNames(dir)
	if err != nil {
		return out
	}
	for _, sub := range subs {
		full := filepath.Join(dir, sub)
		if !model.IsChainDir(full) {
			continue
		}
		if c, err := model.LoadChain(full); err == nil {
			out = append(out, model.FlowInfo{ID: sub, Name: c.ID, WhenToUse: c.Summary})
		}
	}
	return out
}

// ParseCommand splits "/cmd the rest" into ("cmd", "the rest"); cmd is lowercased. Exported for tests.
func ParseCommand(line string) (cmd, rest string) {
	body := line
	if strings.HasPrefix(line, "/") {
		body = line[1:]
	}
	cmd, rest = splitFirst(body)
	return strings.ToLower(cmd), rest
}

func splitFirst(s string) (first, rest string) {
	s = strings.TrimLeft(s, " \t\r\n")
	i := 0
	for i < len(s) && !unicode.IsSpace(rune(s[i])) {
		i++
	}
	first = s[:i]
	if i < len(s) {
		rest = strings.TrimSpace(s[i:])
	}
	return first, rest
}

func (d *Dispatcher) runSlash(line string, r *model.TurnResult) {
	cmd, rest := ParseCommand(line)
	rest, redirect := parseRedirect(rest)
	task := rest
	r.Intent = cmd
	d.status("command: /" + cmd)

	switch cmd {
	case "help", "h", "?":
		r.Text = d.Help()
	case "search", "docs":
		if rest == "" {
			usage(r, "/search [source] <query>   (source: a KB name or a path; -r for raw hits)")
			break
		}
		r.Intent = "search"
		r.Text = d.doSearchKb(rest)
	case "route":
		if rest == "" {
			usage(r, "/route <request>")
			break
		}
		d.doRoute(rest, redirect, r)
		redirect = ""
	case "flow":
		name, input := splitFirst(rest)
		if name == "" {
			usage(r, "/flow <name> <input>")
			break
		}
		d.runNamedFlow(name, input, r)
	case "do":
		if rest == "" {
			usage(r, "/do <tool [arg] | shell command>")
			break
		}
		d.doExec(rest, r)
	case "propose":
		if rest == "" {
			usage(r, "/propose <description>")
			break
		}
		d.doPropose(rest, r)
	case "ws":
		d.doWs(rest, r)
	case "flows":
		var sb strings.Builder
		fl := d.flowCatalog()
		sb.WriteString("Authored flows (/route can match these, or run with /flow <name>):\n")
		if len(fl) == 0 {
			sb.WriteString("  (none in flows/)")
		} else {
			for _, fi := range fl {
				sb.WriteString("  " + fi.ID + " - " + fi.WhenToUse + "\n")
			}
		}
		r.Text = strings.TrimRight(sb.String(), "\n")
	case "tools":
		var sb strings.Builder
		sb.WriteString("Declared tools (run with /do <name> [arg]):\n")
		tl := d.inst.Tools()
		if len(tl) == 0 {
			sb.WriteString("  (none in tools/manifest.json)")
		} else {
			for _, t := range tl {
				sb.WriteString("  " + pad(t.Name, 18) + " " + t.Description + "\n")
			}
		}
		r.Text = strings.TrimRight(sb.String(), "\n")
	case "note":
		if rest == "" {
			usage(r, "/note <text>")
			break
		}
		d.appendNote(rest)
		r.Text = "noted."
	case "notes":
		notes := d.readNotes()
		if notes != "" {
			r.Text = notes
		} else {
			r.Text = "(no notes yet - use /note <text>, or redirect a write with '> path')"
		}
	case "clear", "reset":
		d.history = nil
		r.Intent = "clear"
		r.Text = ""
	case "quit", "exit", "q":
		r.Intent = conventions.IntentQuit
		r.Text = "bye"
	default:
		r.Text = "If you are trying to use a slash command, type /help to see available commands."
	}

	d.applyRedirect(r, redirect, "/"+cmd, task)
}

func usage(r *model.TurnResult, u string) {
	r.Text = "Usage: " + u
	r.IsError = true
}

func (d *Dispatcher) runNamedFlow(name, input string, r *model.TurnResult) {
	chainDir := filepath.Join(d.inst.FlowsDirAbs(), name)
	if !model.IsChainDir(chainDir) {
		r.IsError = true
		r.Text = "no flow '" + name + "' (expected flows/" + name + "/chain.json)"
		return
	}
	c, err := model.LoadChain(chainDir)
	if err != nil {
		r.IsError = true
		r.Text = "[error] loading chain '" + name + "': " + err.Error()
		return
	}
	cr := chain.NewEngine(d.inst, d, d.status).Run(c, input, d.activeWorkspace)
	r.Text = cr.Text
	r.IsError = cr.IsError
}

func (d *Dispatcher) remember(entry string) {
	d.history = append(d.history, entry)
	for len(d.history) > maxHistory {
		d.history = d.history[1:]
	}
}

// --- helpers ---

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + " ..."
}

func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

func readDirNames(dir string) ([]string, error) {
	entries, err := readDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sortStrings(names)
	return names, nil
}
