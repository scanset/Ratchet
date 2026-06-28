// Package cli is the console entry layer: the verb handlers (Run), the REPL operator console
// (console.go), and the deterministic-core self test (selftest.go). Port of src.bak/Cli/*.cs.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scanset/Ratchet/internal/chain"
	"github.com/scanset/Ratchet/internal/dispatch"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/mcp"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/oracle"
	"github.com/scanset/Ratchet/internal/runrec"
	"github.com/scanset/Ratchet/internal/search"
	"github.com/scanset/Ratchet/internal/snapshot"
	"github.com/scanset/Ratchet/internal/tool"
	"github.com/scanset/Ratchet/internal/version"
)

const (
	genTimeoutMs     = 300000
	maxProblemsShown = 40
)

func effectiveURL(inst *instance.Instance) string {
	if env := os.Getenv("OLLAMA_URL"); env != "" {
		return env
	}
	return inst.Config.OllamaURL
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func isCommand(s string) bool {
	switch s {
	case "open", "chat", "mcp", "flow", "validate", "reindex", "index", "tokenize", "list", "flows", "tools",
		"runs", "rollback", "validate-flow", "doctor", "gen", "selftest", "version", "help", "-h", "--help", "-v", "--version":
		return true
	}
	return false
}

// Run dispatches a CLI invocation and returns the process exit code.
func Run(args []string) int {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	// VSCode-style shorthand: `ratchet <dir>` opens the operator console. Commands win.
	if cmd != "" && !isCommand(cmd) {
		if _, err := os.Stat(cmd); err == nil {
			inst, err := instance.Open(cmd)
			if err != nil {
				return fail(err)
			}
			runConsole(inst, effectiveURL(inst))
			return 0
		}
	}

	switch cmd {
	case "", "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "version", "-v", "--version":
		fmt.Println("ratchet " + version.Version)
		return 0
	case "open":
		return needDir(args, "ratchet open <dir>", cmdOpen)
	case "list":
		return cmdList(args)
	case "validate":
		dir, table := arg(args, 1), arg(args, 2)
		if dir == "" || table == "" {
			return fail(fmt.Errorf("usage: ratchet validate <dir> <table>"))
		}
		return cmdValidate(dir, table)
	case "validate-flow":
		dir := arg(args, 1)
		if dir == "" {
			return fail(fmt.Errorf("usage: ratchet validate-flow <dir> [name]"))
		}
		return cmdValidateFlow(dir, arg(args, 2))
	case "doctor":
		dir := arg(args, 1)
		if dir == "" {
			return fail(fmt.Errorf("usage: ratchet doctor <dir>"))
		}
		inst, err := instance.Open(dir)
		if err != nil {
			return fail(err)
		}
		return tool.Doctor(inst, effectiveURL(inst))
	case "gen":
		dir := arg(args, 1)
		if dir == "" || len(args) < 3 {
			return fail(fmt.Errorf("usage: ratchet gen <dir> <prompt...>"))
		}
		return cmdGen(dir, strings.Join(args[2:], " "))
	case "chat":
		return needDir(args, "ratchet chat <dir>", func(inst *instance.Instance) int {
			runConsole(inst, effectiveURL(inst))
			return 0
		})
	case "mcp":
		return needDir(args, "ratchet mcp <dir>", func(inst *instance.Instance) int {
			mcp.Serve(inst, effectiveURL(inst))
			return 0
		})
	case "flow":
		return cmdFlow(args)
	case "reindex":
		return needDir(args, "ratchet reindex <dir>", func(inst *instance.Instance) int {
			search.Reindex(inst.Root, inst.Config.Name, inst.Config.Domain, func(s string) { fmt.Fprintln(os.Stderr, "  - "+s) })
			return 0
		})
	case "index":
		dir := arg(args, 1)
		if dir == "" {
			return fail(fmt.Errorf("usage: ratchet index <kb-dir>"))
		}
		abs, _ := filepath.Abs(dir)
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return fail(fmt.Errorf("not a directory: %s", abs))
		}
		search.WriteKbManifest(abs, func(s string) { fmt.Fprintln(os.Stderr, "  - "+s) })
		return 0
	case "flows":
		return needDir(args, "ratchet flows <dir>", cmdFlows)
	case "tools":
		return needDir(args, "ratchet tools <dir>", cmdTools)
	case "runs":
		return cmdRuns(args)
	case "rollback":
		return cmdRollback(args)
	case "tokenize":
		return cmdTokenize()
	case "selftest":
		if SelfTest() != 0 {
			return 2
		}
		return 0
	default:
		return fail(fmt.Errorf("unknown command '%s'\n\n%s", cmd, usage))
	}
}

func needDir(args []string, usageMsg string, fn func(*instance.Instance) int) int {
	dir := arg(args, 1)
	if dir == "" {
		return fail(fmt.Errorf("usage: %s", usageMsg))
	}
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	return fn(inst)
}

func cmdOpen(inst *instance.Instance) int {
	c := inst.Config
	fmt.Println("ratchet '" + c.Name + "'  (" + c.Domain + ")")
	fmt.Println("  root      : " + inst.Root)
	embed := c.Models.Embed
	if embed == "" {
		embed = "(none)"
	}
	fmt.Println("  models    : generate=" + c.Models.Generate + " dispatch=" + c.DispatchModel() + " embed=" + embed)
	fmt.Println("  ollama    : " + effectiveURL(inst))
	if inst.Manifest != nil {
		fmt.Printf("  kb entries: %d\n", len(inst.Manifest.Entries))
		for _, e := range inst.Manifest.Entries {
			g := ""
			if e.Group != "" {
				g = "[" + e.Group + "] "
			}
			fmt.Println("    - " + pad(e.ID, 22) + " " + g + e.Title)
		}
	} else {
		fmt.Println("  kb entries: (no manifest.json)")
	}
	var names []string
	for _, t := range inst.Tools() {
		names = append(names, t.Name)
	}
	fmt.Println("  tools     : " + strings.Join(names, ", "))
	return 0
}

func cmdValidate(dir, table string) int {
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	schema, err := model.LoadTableSchema(inst.SchemaPath(table))
	if err != nil {
		return fail(err)
	}
	data, err := inst.ReadAt(inst.SamplePath(table))
	if err != nil {
		return fail(err)
	}
	vr := model.ValidateResult{Table: table}
	vr.Problems = oracle.ValidateTsv(schema, data, oracle.BuildRefs(inst))
	vr.Ok = len(vr.Problems) == 0
	if vr.Ok {
		fmt.Println(vr.ToText(maxProblemsShown))
		return 0
	}
	fmt.Fprintln(os.Stderr, vr.ToText(maxProblemsShown))
	return 2
}

func cmdValidateFlow(dir, name string) int {
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	var tools []string
	for _, t := range inst.Tools() {
		tools = append(tools, t.Name)
	}

	cdir := inst.FlowsDirAbs()
	var chainDirs []string
	if name != "" {
		one := filepath.Join(cdir, name)
		if !model.IsChainDir(one) {
			fmt.Println("FAIL  " + name + ": no chain.json at flows/" + name + "/")
			return 2
		}
		chainDirs = append(chainDirs, one)
	} else if entries, err := os.ReadDir(cdir); err == nil {
		var subs []string
		for _, e := range entries {
			if e.IsDir() {
				subs = append(subs, filepath.Join(cdir, e.Name()))
			}
		}
		sort.Slice(subs, func(i, j int) bool { return strings.ToLower(subs[i]) < strings.ToLower(subs[j]) })
		for _, s := range subs {
			if model.IsChainDir(s) {
				chainDirs = append(chainDirs, s)
			}
		}
	}

	problems := 0
	var chainNames []string
	for _, sub := range chainDirs {
		cid := filepath.Base(sub)
		chainNames = append(chainNames, cid)
		c, err := model.LoadChain(sub)
		if err != nil {
			fmt.Println("FAIL  " + cid + " (chain): " + err.Error())
			problems++
			continue
		}
		cp := chain.Lint(c, tools)
		if len(cp) == 0 {
			fmt.Printf("ok    %s (chain, %d node(s))\n", cid, len(c.Actions))
		} else {
			fmt.Println("FAIL  " + cid + " (chain):")
			for _, p := range cp {
				fmt.Println("        " + p)
			}
			problems += len(cp)
		}
	}

	for _, p := range oracle.NamespaceProblems(chainNames, tools) {
		fmt.Println("FAIL  namespace: " + p)
		problems++
	}
	if problems > 0 {
		return 2
	}
	return 0
}

func cmdGen(dir, prompt string) int {
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	out, err := ollama.Generate(effectiveURL(inst), inst.Config.Models.Generate, prompt, nil, 0.3, genTimeoutMs, nil)
	if err != nil {
		return fail(err)
	}
	fmt.Println(out)
	return 0
}

func cmdFlow(args []string) int {
	dir, name := arg(args, 1), arg(args, 2)
	if dir == "" || name == "" {
		return fail(fmt.Errorf("usage: ratchet flow <dir> <name> [--ws <workspace>] [input...]"))
	}
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	ws := ""
	var rest []string
	for i := 3; i < len(args); i++ {
		if args[i] == "--ws" && i+1 < len(args) {
			ws = args[i+1]
			i++
		} else {
			rest = append(rest, args[i])
		}
	}
	input := strings.Join(rest, " ")

	chainDir := filepath.Join(inst.FlowsDirAbs(), name)
	if !model.IsChainDir(chainDir) {
		return fail(fmt.Errorf("no flow '%s' (expected flows/%s/chain.json)", name, name))
	}
	c, err := model.LoadChain(chainDir)
	if err != nil {
		return fail(err)
	}
	workspace := ""
	if ws != "" {
		workspace = filepath.Join(inst.WorkspacesDirAbs(), ws)
		if st, err := os.Stat(workspace); err != nil || !st.IsDir() {
			return fail(fmt.Errorf("no workspace '%s' (under workspaces/)", ws))
		}
	}
	disp := dispatch.New(inst, effectiveURL(inst), func(s string) { fmt.Fprintln(os.Stderr, "  - "+s) })
	cr := chain.NewEngine(inst, disp, func(s string) { fmt.Fprintln(os.Stderr, "  - "+s) }).Run(c, input, workspace)
	if cr.Text != "" {
		fmt.Println(cr.Text)
	}
	if cr.IsError {
		return 2
	}
	return 0
}

// cmdTokenize is a stdin filter exposing the engine's canonical tokenizer (search.Tokens: lowercase,
// [a-z0-9_]+, light-stemmed) so tools (e.g. the route_score oracle) tokenize identically to retrieval
// instead of reimplementing the stemmer. Each input line -> one output line of space-joined tokens.
func cmdTokenize() int {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		fmt.Println(strings.Join(search.Tokens(sc.Text()), " "))
	}
	return 0
}

func cmdRuns(args []string) int {
	dir := arg(args, 1)
	if dir == "" {
		return fail(fmt.Errorf("usage: ratchet runs <dir> [n]"))
	}
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	idx, _ := runrec.ReadIndex(inst)
	if len(idx) == 0 {
		fmt.Println("No runs recorded yet.")
		return 0
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].RunID > idx[j].RunID })
	limit := 15
	if n := atoi(arg(args, 2)); n > 0 {
		limit = n
	}
	if limit > len(idx) {
		limit = len(idx)
	}
	for _, e := range idx[:limit] {
		roll := ""
		if e.Rollbackable && snapshot.Exists(inst, e.RunID) {
			roll = "  [rollbackable]"
		}
		ws := e.Workspace
		if ws == "" {
			ws = "-"
		}
		fmt.Printf("%s  %-12s ws:%-12s %-10s %d tok  %d chg%s\n",
			e.RunID, e.Chain, ws, e.Outcome, e.TokensTotal, e.ChangedFiles, roll)
	}
	return 0
}

func cmdRollback(args []string) int {
	dir := arg(args, 1)
	if dir == "" {
		return fail(fmt.Errorf("usage: ratchet rollback <dir> [id|latest] [--ws <name>] [--yes]"))
	}
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	id, wsFlag, yes := "", "", false
	for i := 2; i < len(args); i++ {
		switch {
		case args[i] == "--yes":
			yes = true
		case args[i] == "--ws" && i+1 < len(args):
			wsFlag = args[i+1]
			i++
		default:
			id = args[i]
		}
	}

	idx, _ := runrec.ReadIndex(inst)
	wsName := wsFlag
	if wsName == "" && id != "" && id != "latest" {
		for _, e := range idx {
			if e.RunID == id {
				wsName = e.Workspace
				break
			}
		}
	}
	if wsName == "" {
		return fail(fmt.Errorf("specify --ws <name> (or an explicit run id whose workspace is recorded)"))
	}
	candidates := snapshot.Rollbackable(inst, wsName)
	if len(candidates) == 0 {
		return fail(fmt.Errorf("nothing to roll back for workspace '%s'", wsName))
	}
	if id == "" || id == "latest" {
		id = candidates[0].RunID
	}
	if !snapshot.Exists(inst, id) {
		return fail(fmt.Errorf("run '%s' has no snapshot (pruned or not for this workspace)", id))
	}
	wsAbs := filepath.Join(inst.WorkspacesDirAbs(), wsName)
	if !yes {
		fmt.Printf("Would restore workspace '%s' to run %s. Re-run with --yes to apply.\n", wsName, id)
		return 0
	}
	newID, changed, err := snapshot.RollbackTo(inst, wsAbs, wsName, id, inst.Config.Name, version.Version, "cli", 10)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("Rolled back '%s' to run %s (%d files changed). Rollback recorded as run %s.\n", wsName, id, changed, newID)
	return 0
}

func atoi(s string) int {
	n := 0
	if s == "" {
		return 0
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func cmdFlows(inst *instance.Instance) int {
	fdir := inst.FlowsDirAbs()
	fmt.Println("action chains:")
	if entries, err := os.ReadDir(fdir); err == nil {
		var subs []string
		for _, e := range entries {
			if e.IsDir() {
				subs = append(subs, filepath.Join(fdir, e.Name()))
			}
		}
		sort.Slice(subs, func(i, j int) bool { return strings.ToLower(subs[i]) < strings.ToLower(subs[j]) })
		for _, sub := range subs {
			if !model.IsChainDir(sub) {
				continue
			}
			if c, err := model.LoadChain(sub); err == nil {
				fmt.Println("  " + pad(filepath.Base(sub), 18) + " " + c.Summary)
			}
		}
	}
	fmt.Println("\nbuilt-in capabilities (in the console):")
	fmt.Println("  plain text         ungrounded chat")
	fmt.Println("  /search [src] <q>  grounded answer from a knowledge base")
	fmt.Println("  /route <request>   let the model pick a flow")
	return 0
}

func cmdTools(inst *instance.Instance) int {
	fmt.Println("declared tools (run with /do <name> [arg]):")
	for _, t := range inst.Tools() {
		fmt.Println("  " + pad(t.Name, 18) + " " + t.Description)
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error: "+err.Error())
	return 1
}

func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

const usage = `ratchet - the cross-platform ICM host
  ratchet <dir>                       open the operator console on a ratchet dir (rel or abs)
  ratchet open  <dir>                 load + summarize a ratchet instance
  ratchet chat  <dir>                 operator console (dispatcher; needs Ollama)
  ratchet mcp   <dir>                 serve this ratchet over MCP (stdio)
  ratchet flow  <dir> <name> [--ws w] [in...]  run an action chain (flows/<name>/chain.json)
  ratchet validate <dir> <table>      run the oracle on a table
  ratchet validate-flow <dir> [name]  lint action chain(s)
  ratchet doctor <dir>                preflight a ratchet's declared toolchain
  ratchet reindex <dir>               regenerate manifest.json from <!--icm--> blocks
  ratchet index <kb-dir>              build manifest.json for a knowledge library
  ratchet tokenize                    tokenize stdin lines with the search tokenizer (tokens per line)
  ratchet list  <dir> [--group G] [--type T] [--json]   enumerate the KB catalog
  ratchet flows <dir>                 list the instance's flows
  ratchet tools <dir>                 list the instance's declared tools
  ratchet runs  <dir> [n]             list recent chain runs (the audit log)
  ratchet rollback <dir> [id|latest] [--ws w] [--yes]  restore a workspace to a run's pre-state
  ratchet gen   <dir> <prompt...>     one raw generate call
  ratchet selftest                    check the deterministic core (no model)
  ratchet version                     print the host version

  env OLLAMA_URL overrides the config ollama_url
`
