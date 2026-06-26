// Package tool runs declared command/script tools and preflights a ratchet's requirements (doctor).
// The GUARDRAIL: a tool's command is authored by the ratchet, never by the model; the model (or a
// flow) only fills declared {placeholder} arguments, and argv is passed directly to the OS (no shell),
// so there is no shell-injection surface. Port of src.bak/Runtime/{ToolRunner,Doctor}.cs, with the
// Windows-only exec assumptions replaced by runtime OS detection (see platform.go).
package tool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/scanset/Ratchet/internal/config"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/model"
)

// ResolveArgv builds the actual argv to run for a tool: an explicit `command` as-is, or a `script`
// dispatched to an interpreter by extension and host OS. Returns nil if the tool declares neither.
func ResolveArgv(t config.Tool) []string {
	if cmd := t.Command(); cmd != nil {
		return cmd
	}
	script := t.Script()
	if script == "" {
		return nil
	}
	switch strings.ToLower(filepath.Ext(script)) {
	case ".ps1":
		return []string{psInterp(), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}
	case ".sh":
		return []string{shInterp(), script}
	case ".py":
		return []string{pyInterp(), script}
	default:
		return []string{script} // direct exec (shebang / executable bit)
	}
}

// Run executes a declared command/script tool, substituting {placeholder} arg tokens, with the
// instance root as the working directory and a per-tool timeout.
func Run(inst *instance.Instance, t config.Tool, args map[string]any) model.ToolRunResult {
	var res model.ToolRunResult
	argv := ResolveArgv(t)
	if len(argv) == 0 {
		res.Error = "tool '" + t.Name + "' declares no command/script"
		return res
	}
	if args == nil {
		args = map[string]any{}
	}
	sub := make([]string, len(argv))
	for i, tok := range argv {
		sub[i] = substitute(tok, args)
	}

	timeoutMs := t.TimeoutMs()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, sub[0], sub[1:]...)
	cmd.Dir = inst.Root // sandbox: relative paths resolve under the instance
	if env := t.EnvVars(); env != nil {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+toStr(v))
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if key := t.StdinArg(); key != "" {
		if v, ok := args[key]; ok {
			cmd.Stdin = strings.NewReader(toStr(v))
		}
	}

	err := cmd.Run()
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.TimedOut = true
		res.ExitCode = -1
	case err == nil:
		res.ExitCode = 0
	default:
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.Error = "running '" + sub[0] + "': " + err.Error()
			return res
		}
	}
	res.Ok = !res.TimedOut && res.ExitCode == 0

	var out strings.Builder
	out.WriteString(strings.TrimRight(res.Stdout, " \t\r\n"))
	if strings.TrimSpace(res.Stderr) != "" {
		appendLine(&out, "[stderr] "+strings.TrimSpace(res.Stderr))
	}
	if res.TimedOut {
		appendLine(&out, "[timed out after "+strconv.Itoa(timeoutMs)+" ms]")
	}
	if !res.Ok && !res.TimedOut {
		appendLine(&out, "[exit code "+strconv.Itoa(res.ExitCode)+"]")
	}
	res.Output = out.String()
	return res
}

// RunShell runs an operator-typed command through the host shell (sh/bash on unix, PowerShell on
// Windows), capturing stdout+stderr merged, with a timeout and the given working dir. Operator-
// authorized arbitrary execution; the model never composes the command.
func RunShell(workdir, command string, timeoutMs int) string {
	argv := shellArgv(command)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_ = cmd.Run()
	out := strings.TrimRight(buf.String(), " \t\r\n")
	if ctx.Err() == context.DeadlineExceeded {
		if out != "" {
			out += "\n"
		}
		out += "[timed out after " + strconv.Itoa(timeoutMs/1000) + "s]"
	}
	if out == "" {
		out = "(no output)"
	}
	return truncate(out, 8000)
}

// substitute replaces {key} tokens with argument values. Unknown placeholders are left untouched.
func substitute(token string, args map[string]any) string {
	if !strings.Contains(token, "{") {
		return token
	}
	out := token
	for k, v := range args {
		out = strings.ReplaceAll(out, "{"+k+"}", toStr(v))
	}
	return out
}

func appendLine(sb *strings.Builder, s string) {
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteString(s)
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + " ..."
}

// errorsAs is a tiny local wrapper so we don't import errors just for one As call.
func errorsAs(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
