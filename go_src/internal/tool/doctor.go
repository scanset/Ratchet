// Doctor - preflight validation of the tools a ratchet declares it needs. Generic mechanism: the host
// knows how to run a small set of check types; the ratchet's ratchet.json `requirements` array says
// which apply. Read-only. Reports [ok]/[warn]/[MISS] + hint; returns 0 if all required pass, else 2.
// Port of src.bak/Runtime/Doctor.cs.
package tool

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/search"
)

// Doctor preflights the ratchet's declared requirements against the host. Returns 0 when all required
// checks pass, else 2.
func Doctor(inst *instance.Instance, url string) int {
	fmt.Println("ratchet: " + inst.Config.Name)
	fmt.Println()
	reqs := jsonx.AsArr(inst.Config.Requirements)
	if len(reqs) == 0 {
		fmt.Println("(no requirements declared in ratchet.json)")
		return 0
	}

	problems, warns := 0, 0
	for _, o := range reqs {
		r := jsonx.AsObject(o)
		if r == nil {
			continue
		}
		name := jsonx.GetStringOr(r, "name", "(unnamed)")
		required := jsonx.GetBool(r, "required", true)
		hint := jsonx.GetStringOr(r, "hint", "")
		ok, detail := check(inst, url, r)
		switch {
		case ok:
			report("ok", name, detail)
		case required:
			report("MISS", name, detail+tail(hint))
			problems++
		default:
			report("warn", name, detail+tail(hint))
			warns++
		}
	}

	// If no per-KB requirement is declared, validate every registered knowledge base automatically -
	// so a catalog-driven registry (kb/catalog.json) gets the same coverage without a requirement each.
	hasKbReq := false
	for _, o := range reqs {
		if r := jsonx.AsObject(o); r != nil {
			if _, ok := jsonx.GetString(r, "kb"); ok {
				hasKbReq = true
				break
			}
		}
	}
	if !hasKbReq {
		for _, kb := range inst.Knowledge().Bases {
			if ok, detail := checkKb(inst, kb.Name); ok {
				report("ok", "KB "+kb.Name, detail)
			} else {
				report("MISS", "KB "+kb.Name, detail)
				problems++
			}
		}
	}

	fmt.Println()
	if problems > 0 {
		fmt.Printf("doctor: %d problem(s), %d warning(s)\n", problems, warns)
		return 2
	}
	suffix := ""
	if warns > 0 {
		suffix = fmt.Sprintf(" (%d warning(s))", warns)
	}
	fmt.Println("doctor: all required checks passed" + suffix)
	return 0
}

func tail(hint string) string {
	if hint == "" {
		return ""
	}
	return "  - " + hint
}

func report(tag, name, detail string) {
	fmt.Printf("%-8s%-22s %s\n", "["+tag+"]", name, detail)
}

func check(inst *instance.Instance, url string, r map[string]any) (bool, string) {
	if v, ok := jsonx.GetString(r, "exe"); ok {
		_, err := exec.LookPath(v)
		if err == nil {
			return true, "on PATH"
		}
		return false, "not on PATH"
	}
	if v, ok := jsonx.GetString(r, "file"); ok {
		p := os.ExpandEnv(v)
		if _, err := os.Stat(p); err == nil {
			return true, "present"
		}
		return false, "missing: " + v
	}
	if v, ok := jsonx.GetString(r, "env"); ok {
		if e := os.Getenv(v); e != "" {
			return true, "set: " + e
		}
		return false, "not set"
	}
	if v, ok := jsonx.GetString(r, "http"); ok {
		if httpOK(v) {
			return true, "reachable"
		}
		return false, "unreachable: " + v
	}
	if v, ok := jsonx.GetString(r, "model"); ok {
		if hasModel(url, v) {
			return true, "pulled"
		}
		return false, "not pulled"
	}
	if v, ok := jsonx.GetString(r, "kb"); ok {
		return checkKb(inst, v)
	}
	if v, ok := jsonx.GetString(r, "tool"); ok {
		if runTool(inst, v) {
			return true, "passed"
		}
		return false, "failed"
	}
	return false, "unknown requirement (need one of exe/file/env/http/model/kb/tool)"
}

func httpOK(u string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func hasModel(url, model string) bool {
	names, err := ollama.Tags(url)
	if err != nil {
		return false
	}
	for _, nm := range names {
		if nm == model || strings.HasPrefix(nm, model+":") {
			return true
		}
	}
	return false
}

func checkKb(inst *instance.Instance, kbName string) (bool, string) {
	kb := inst.Knowledge().Find(kbName)
	if kb == nil {
		return false, "no knowledgeBase named " + kbName
	}
	dir := kb.Path
	if _, err := os.Stat(filepath.Join(dir, conventions.ManifestFile)); err != nil {
		return false, "no manifest (ratchet index " + dir + ")"
	}
	files := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") &&
			!strings.EqualFold(filepath.Base(p), "README.md") {
			files++
		}
		return nil
	})
	entries := len(search.LoadManifestMap(dir))
	if entries == files {
		return true, fmt.Sprintf("%d docs, manifest current", files)
	}
	return false, fmt.Sprintf("drift: %d docs vs %d entries (reindex)", files, entries)
}

func runTool(inst *instance.Instance, toolName string) bool {
	t := inst.FindTool(toolName)
	if t == nil {
		return false
	}
	rr := Run(inst, *t, map[string]any{})
	return rr.Ok && rr.Error == ""
}
