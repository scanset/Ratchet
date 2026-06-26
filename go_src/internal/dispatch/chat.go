package dispatch

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
)

// doChat is plain-text conversation: UNGROUNDED chat. The model is told it cannot act (the operator
// drives via slash commands) and is given the active-workspace focus + recent history as context.
func (d *Dispatcher) doChat(line string) string {
	system := d.readSystem()
	var sb strings.Builder
	if system != "" {
		sb.WriteString(system + "\n\n")
	}
	sb.WriteString("You are the conversational assistant of the '" + d.inst.Config.Name + "' tool for: " + d.inst.Config.Domain + ". ")
	sb.WriteString("Chat to plan; you cannot run tools or edit files yourself - the operator acts by typing slash commands. ")
	sb.WriteString("When an action would help, name the exact command (/search, /route, /flow, /do, /propose, /ws). Do not invent commands or facts.\n")
	if focus := d.workspaceFocus(); focus != "" {
		sb.WriteString("\n" + focus + "\n")
	}
	if notes := d.readNotes(); notes != "" {
		sb.WriteString("\nProject notes (NOTES.md):\n" + truncate(notes, 1500) + "\n")
	}
	if len(d.history) > 0 {
		sb.WriteString("\nConversation so far:\n" + strings.Join(d.history, "\n") + "\n")
	}
	sb.WriteString("\nOperator: " + line + "\n\nReply briefly and concretely.")
	out, err := d.generateMaybeStream(sb.String(), 0.4)
	if err != nil {
		return "[error] " + err.Error()
	}
	return out
}

// --- /ws: switch or create the active workspace ---

func (d *Dispatcher) doWs(rest string, r *model.TurnResult) {
	r.Intent = "ws"
	sub, name := splitFirst(rest)
	sub = strings.ToLower(sub)
	name = strings.TrimSpace(name)
	if sub != "switch" && sub != "create" {
		r.Text = "Usage: /ws switch <name> | /ws create <name>"
		r.IsError = true
		return
	}
	if name == "" {
		usage(r, "/ws "+sub+" <name>")
		return
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		r.Text = "workspace name may not contain path separators or '..'"
		r.IsError = true
		return
	}

	abs := filepath.Join(d.inst.WorkspacesDirAbs(), name)
	if sub == "switch" {
		if !isDir(abs) {
			r.Text = "no workspace '" + name + "' (create it with /ws create " + name + ")"
			r.IsError = true
			return
		}
		d.activeWorkspace = abs
		r.Text = "active workspace: " + name
		return
	}
	// create
	if isDir(abs) {
		r.Text = "workspace '" + name + "' already exists (use /ws switch " + name + ")"
		r.IsError = true
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		r.Text = "[error] creating workspace: " + err.Error()
		r.IsError = true
		return
	}
	_ = os.WriteFile(filepath.Join(abs, "project.json"), []byte(jsonx.SerializePretty(jsonx.Obj("name", name))), 0o644)
	d.activeWorkspace = abs
	r.Text = "created and switched to workspace: " + name
}

func (d *Dispatcher) workspaceDirAbs() string { return d.activeWorkspace }

// workspaceFocus is the session-focus block injected into chat/search prompts.
func (d *Dispatcher) workspaceFocus() string {
	abs := d.workspaceDirAbs()
	if abs == "" {
		return ""
	}
	name := filepath.Base(strings.TrimRight(abs, `\/`))
	var sb strings.Builder
	sb.WriteString("Active workspace: " + name + " (" + abs + ")")
	if data, err := os.ReadFile(filepath.Join(abs, "project.json")); err == nil {
		sb.WriteString("\nproject.json: " + truncate(strings.ReplaceAll(string(data), "\n", " "), 300))
	}
	if entries, err := os.ReadDir(abs); err == nil {
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name()+"/")
			} else {
				names = append(names, e.Name())
			}
		}
		if len(names) > 0 {
			sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
			if len(names) > 40 {
				names = names[:40]
			}
			sb.WriteString("\nfiles: " + strings.Join(names, ", "))
		}
	}
	return sb.String()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// --- capability wrappers reused by the MCP server ---

// Ask grounds an answer on the best KB entry for the query.
func (d *Dispatcher) Ask(query string) string {
	d.cancel = ollama.NewCancel()
	return d.doAsk(query)
}

// RouteEntryID returns the best KB entry id for the query (or "").
func (d *Dispatcher) RouteEntryID(query string) string {
	d.cancel = ollama.NewCancel()
	return d.route(query)
}

func (d *Dispatcher) doAsk(query string) string {
	d.status("route: picking a KB entry")
	id := d.route(query)
	if id == "" {
		return "That isn't covered in this ratchet's knowledge base."
	}
	d.status("read: kb entry '" + id + "'")
	entry, err := d.inst.ReadEntry(id)
	if err != nil {
		return "[error] " + err.Error()
	}
	system := d.readSystem()
	prompt := system + "\n\nAnswer the question using ONLY the entry text below. If it does not contain " +
		"the answer, say so.\n\n--- ENTRY TEXT ---\n" + entry + "\n--- END ---\n\nQuestion: " + query
	d.status("answer: generating (grounded)")
	out, err := d.generateMaybeStream(prompt, 0.2)
	if err != nil {
		return "[error] " + err.Error()
	}
	return out
}

// route is a constrained pick of one KB entry id (or "").
func (d *Dispatcher) route(query string) string {
	if d.inst.Manifest == nil || len(d.inst.Manifest.Entries) == 0 {
		return ""
	}
	entries := d.narrowEntries(query, d.inst.Manifest.Entries, routeCandidateK)
	var ids, lines []string
	for _, e := range entries {
		ids = append(ids, e.ID)
		grp := ""
		if e.Group != "" {
			grp = " (" + e.Group + ")"
		}
		kw := ""
		if len(e.Keywords) > 0 {
			kw = "  [keywords: " + strings.Join(e.Keywords, ", ") + "]"
		}
		lines = append(lines, "- "+e.ID+grp+" : "+e.Title+" - "+e.Summary+kw)
	}
	ids = append(ids, "none")
	schema := jsonx.Schema(jsonx.Obj("entry_id", jsonx.EnumProp(ids)), "entry_id")
	prompt := "Pick the single KB entry whose content can answer the question, or 'none' if nothing " +
		"fits.\n\nIndex:\n" + strings.Join(lines, "\n") + "\n\nQuestion: " + query
	v, err := ollama.GenerateJSON(d.url, d.inst.Config.DispatchModel(), prompt, schema, 0.1, dispatchTimeoutMs, d.cancel)
	if err != nil {
		return ""
	}
	id := jsonx.GetStringOr(v, "entry_id", "none")
	if id == "none" {
		return ""
	}
	return id
}
