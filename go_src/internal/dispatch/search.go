package dispatch

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/meta"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/search"
)

// doSearchKb grounds an answer on a knowledge source. Source resolution: a registered KB name, a path
// (absolute or instance-relative), or - if omitted - the default KB(s) (else the instance kb/ dir).
// "-r"/"--hits" returns the raw locations instead.
func (d *Dispatcher) doSearchKb(rest string) string {
	hitsOnly := false
	rest = stripFlag(rest, "-r", &hitsOnly)
	rest = stripFlag(rest, "--hits", &hitsOnly)
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "Usage: /search [source] <query>   (source: a KB name or a path; -r for raw hits)"
	}

	reg := d.inst.Knowledge()
	first, more := splitFirst(rest)
	var dirAbs, key, query, label string

	if kb := reg.Find(first); kb != nil {
		dirAbs, key, query, label = kb.Path, kb.Name, more, kb.Name
	} else if adhoc := d.resolveSearchPath(first); adhoc != "" {
		dirAbs, key, query, label = adhoc, "", more, first
	} else {
		defs := reg.Defaults()
		if len(defs) > 0 {
			dirAbs, key, label = defs[0].Path, defs[0].Name, defs[0].Name
		} else {
			dirAbs = filepath.Join(d.inst.Root, conventions.KbDir)
			key, label = "kb", "kb"
		}
		query = rest
	}
	if strings.TrimSpace(query) == "" {
		return "Usage: /search [source] <query>"
	}

	d.status("search: '" + label + "' for " + truncate(query, 60))
	ranked := search.Rank(d.indexDir(), key, dirAbs, query, routeCandidateK)
	if len(ranked) == 0 {
		return "(no matches for '" + query + "' in " + label + ")"
	}
	man := search.LoadManifestMap(dirAbs)

	if hitsOnly {
		var sb strings.Builder
		for _, rel := range ranked {
			e, has := man[rel]
			title := rel
			sum := ""
			if has {
				title = e.Title
				if e.Summary != "" {
					sum = "  -  " + e.Summary
				}
			}
			sb.WriteString(rel + "  (" + title + ")" + sum + "\n")
		}
		return strings.TrimRight(sb.String(), "\n")
	}

	picked := ranked[0]
	if len(ranked) > 1 {
		if p := d.pickDoc(query, ranked, man, label); p != "" {
			picked = p
		}
	}

	data, err := os.ReadFile(filepath.Join(dirAbs, filepath.FromSlash(picked)))
	if err != nil {
		return "[error] reading " + picked + ": " + err.Error()
	}
	content := meta.StripMeta(string(data))

	system := d.readSystem()
	var sb strings.Builder
	if system != "" {
		sb.WriteString(system + "\n\n")
	}
	if focus := d.workspaceFocus(); focus != "" {
		sb.WriteString(focus + "\n\n")
	}
	sb.WriteString("Answer the question using ONLY the reference below, from '" + label + "/" + picked +
		"'. If it does not contain the answer, say so.\n\n")
	sb.WriteString("--- " + picked + " ---\n" + truncate(content, 6000) + "\n--- END ---\n\nQuestion: " + query)
	d.status("search: grounding on " + picked)
	out, err := d.generateMaybeStream(sb.String(), 0.2)
	if err != nil {
		return "[error] " + err.Error()
	}
	return out
}

func (d *Dispatcher) pickDoc(query string, candidates []string, man map[string]model.Entry, label string) string {
	ids := append([]string{}, candidates...)
	ids = append(ids, "none")
	var lines []string
	for _, rel := range candidates {
		desc := rel
		if e, has := man[rel]; has {
			if e.Summary != "" {
				desc = e.Summary
			} else {
				desc = e.Title
			}
		}
		lines = append(lines, "- "+rel+" : "+desc)
	}
	schema := jsonx.Schema(jsonx.Obj("doc", jsonx.EnumProp(ids)), "doc")
	prompt := "Pick the ONE document whose content best answers the question, or 'none'.\n\n" +
		"Documents in '" + label + "':\n" + strings.Join(lines, "\n") + "\n\nQuestion: " + query
	v, err := ollama.GenerateJSON(d.url, d.inst.Config.DispatchModel(), prompt, schema, 0.1, dispatchTimeoutMs, d.cancel)
	if err != nil {
		return candidates[0]
	}
	doc := jsonx.GetStringOr(v, "doc", "none")
	if doc == "none" {
		return ""
	}
	return doc
}

func (d *Dispatcher) resolveSearchPath(token string) string {
	if token == "" {
		return ""
	}
	if filepath.IsAbs(token) {
		if st, err := os.Stat(token); err == nil && st.IsDir() {
			abs, _ := filepath.Abs(token)
			return abs
		}
		return ""
	}
	p, err := d.inst.Resolve(token)
	if err != nil {
		return ""
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return ""
}

func (d *Dispatcher) readSystem() string {
	s, err := d.inst.ReadFile(conventions.SystemFile)
	if err != nil {
		return ""
	}
	return s
}

// stripFlag removes a whole-token flag (e.g. "-r") from a command argument; sets *set when found.
func stripFlag(s, flag string, set *bool) string {
	if s == "" {
		return s
	}
	padded := " " + s + " "
	find := " " + flag + " "
	i := strings.Index(padded, find)
	if i < 0 {
		return s
	}
	*set = true
	return strings.TrimSpace(padded[:i] + padded[i+len(find)-1:])
}
