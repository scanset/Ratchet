package dispatch

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/markdown"
	"github.com/scanset/Ratchet/internal/model"
)

// parseRedirect parses a trailing " > path" redirect off a command's argument. The target must look
// like a path (has an extension or a separator, no spaces) so prose with " > " is not misread.
// Returns the argument without the redirect and the path ("" if none). Exported behavior tested.
func parseRedirect(rest string) (clean, path string) {
	if rest == "" {
		return "", ""
	}
	idx := strings.LastIndex(rest, " > ")
	if idx >= 0 {
		p := strings.TrimSpace(rest[idx+3:])
		looksLikePath := p != "" && !strings.Contains(p, " ") &&
			(strings.Contains(p, ".") || strings.Contains(p, `\`) || strings.Contains(p, "/"))
		if looksLikePath {
			return strings.TrimSpace(rest[:idx]), p
		}
	}
	return rest, ""
}

// ParseRedirect is the exported form for tests (returns clean, path).
func ParseRedirect(rest string) (string, string) { return parseRedirect(rest) }

// applyRedirect writes a command/flow's text output to a workspace file ("> path"), code fences
// stripped, recorded in NOTES.md.
func (d *Dispatcher) applyRedirect(r *model.TurnResult, redirect, label, task string) {
	if redirect == "" || r.IsError || r.Text == "" || r.Intent == "clear" || r.Intent == conventions.IntentQuit {
		return
	}
	content := markdown.StripFence(r.Text)
	if err := d.inst.WriteFile(redirect, content); err != nil {
		r.IsError = true
		r.Text = "[error] writing " + redirect + ": " + err.Error()
		return
	}
	if p, err := d.inst.Resolve(redirect); err == nil {
		r.WrittenPath = p
	}
	d.appendNote("wrote `" + redirect + "` (" + label + ": " + truncate(task, 80) + ")")
	r.Text = "Wrote " + redirect + " (" + strconv.Itoa(len(content)) + " chars)."
	d.streamedThisTurn = false
}

func (d *Dispatcher) appendNote(text string) {
	existing := d.readNotes()
	if existing == "" {
		existing = "# " + d.inst.Config.Name + " - session notes\n"
	}
	stamp := time.Now().Format("2006-01-02 15:04")
	_ = d.inst.WriteFile(conventions.NotesFile, strings.TrimRight(existing, "\n")+"\n- ["+stamp+"] "+text+"\n")
}

func (d *Dispatcher) readNotes() string {
	s, err := d.inst.ReadFile(conventions.NotesFile)
	if err != nil {
		return ""
	}
	return s
}

// Help renders the operator console help.
func (d *Dispatcher) Help() string {
	var sb strings.Builder
	sb.WriteString("This is the " + d.inst.Config.Name + " operator console (" + d.inst.Config.Domain + ").\n")
	sb.WriteString("Just type to chat (ungrounded). Use slash commands to act.\n\n")
	sb.WriteString("Generic commands (the harness):\n")
	sb.WriteString("  /search [source] <query> grounded answer from a knowledge base (a KB name, a path, or the default; -r for raw hits)\n")
	sb.WriteString("  /route <request>         let the model pick the best flow (you confirm)\n")
	sb.WriteString("  /flow <name> [input]     run a flow by name\n")
	sb.WriteString("  /do <tool|command>       run a declared tool, or a shell command you type\n")
	sb.WriteString("  /propose <description>   propose a table row, oracle-validated\n")
	sb.WriteString("  /ws switch|create <name> switch or create the active workspace\n")
	sb.WriteString("  /flows                   list the instance's flows (id + summary)\n")
	sb.WriteString("  /tools                   list the instance's declared tools (name + description)\n")
	sb.WriteString("  /note <text>  /notes     add to / show NOTES.md (session memory)\n")
	sb.WriteString("  /clear   /help   /quit\n")
	sb.WriteString("\nAppend ' > path' to save a command's output to a file.\n")
	sb.WriteString("Plain text is ungrounded chat; slashes are for commands.")
	return sb.String()
}

func readDir(dir string) ([]os.DirEntry, error) { return os.ReadDir(dir) }

func sortStrings(ss []string) {
	sort.Slice(ss, func(i, j int) bool { return strings.ToLower(ss[i]) < strings.ToLower(ss[j]) })
}
