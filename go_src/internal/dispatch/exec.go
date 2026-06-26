package dispatch

import (
	"github.com/scanset/Ratchet/internal/config"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/tool"
)

// doExec runs a declared tool by name, or a pasted shell command; output enters context.
func (d *Dispatcher) doExec(rest string, r *model.TurnResult) {
	first, more := splitFirst(rest)
	if t := d.inst.FindTool(first); t != nil {
		r.Intent = "do:" + first
		d.runToolByName(first, "", more, r)
		return
	}
	r.Intent = "do"
	d.status("do: running a shell command")
	wd := d.workspaceDirAbs()
	if wd == "" {
		wd = d.inst.Root
	}
	r.Text = tool.RunShell(wd, rest, doCommandTimeoutMs)
}

// runToolByName runs a declared command/script tool, mapping rest to argName (else the tool's stdin
// arg, else its first required input).
func (d *Dispatcher) runToolByName(toolName, argName, rest string, r *model.TurnResult) {
	t := d.inst.FindTool(toolName)
	if t == nil {
		r.Text = "no such tool: " + toolName
		r.IsError = true
		return
	}
	args := map[string]any{}
	key := argName
	if key == "" {
		key = t.StdinArg()
	}
	if key == "" {
		key = firstRequiredArg(*t)
	}
	if key != "" && rest != "" {
		args[key] = rest
	}
	rr := tool.Run(d.inst, *t, args)
	switch {
	case rr.Error != "":
		r.Text = rr.Error
	case rr.Output != "":
		r.Text = rr.Output
	default:
		r.Text = "(no output)"
	}
	r.IsError = rr.Error != "" || !rr.Ok
}

func firstRequiredArg(t config.Tool) string {
	schema := jsonx.AsObject(t.InputSchema())
	if schema == nil {
		return ""
	}
	req := jsonx.GetArr(schema, "required")
	if len(req) > 0 && req[0] != nil {
		if s, ok := req[0].(string); ok {
			return s
		}
	}
	return ""
}
