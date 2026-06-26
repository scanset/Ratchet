package tool

import (
	"runtime"
	"strings"
	"testing"

	"github.com/scanset/Ratchet/internal/config"
	"github.com/scanset/Ratchet/internal/instance"
)

func TestSubstitute(t *testing.T) {
	got := substitute("echo {name} {n}", map[string]any{"name": "hi", "n": 3})
	if got != "echo hi 3" {
		t.Fatalf("substitute: %q", got)
	}
	// unknown placeholders are left untouched
	if got := substitute("{a}-{b}", map[string]any{"a": "x"}); got != "x-{b}" {
		t.Fatalf("unknown placeholder: %q", got)
	}
	if got := substitute("noplaceholders", map[string]any{"a": "x"}); got != "noplaceholders" {
		t.Fatalf("no-brace fast path: %q", got)
	}
}

func TestResolveArgv(t *testing.T) {
	// explicit command argv passes through unchanged
	cmd := config.Tool{Extra: map[string]any{"command": []any{"echo", "hi"}}}
	if got := ResolveArgv(cmd); len(got) != 2 || got[0] != "echo" || got[1] != "hi" {
		t.Fatalf("command argv: %v", got)
	}
	// a .ps1 script dispatches to a powershell interpreter with -File
	ps := config.Tool{Extra: map[string]any{"script": "tools/x.ps1"}}
	argv := ResolveArgv(ps)
	if len(argv) < 2 || !strings.Contains(strings.ToLower(argv[0]), "powershell") && !strings.Contains(strings.ToLower(argv[0]), "pwsh") {
		t.Fatalf(".ps1 interpreter: %v", argv)
	}
	if argv[len(argv)-1] != "tools/x.ps1" || argv[len(argv)-2] != "-File" {
		t.Fatalf(".ps1 argv tail: %v", argv)
	}
	// a .sh script dispatches to a shell; a .py to python
	if argv := ResolveArgv(config.Tool{Extra: map[string]any{"script": "x.sh"}}); argv[len(argv)-1] != "x.sh" {
		t.Fatalf(".sh argv: %v", argv)
	}
	// no command and no script -> nil
	if ResolveArgv(config.Tool{Extra: map[string]any{}}) != nil {
		t.Fatal("empty tool should resolve to nil argv")
	}
}

// Real execution on the host: a command tool with a substituted placeholder, run with the instance
// root as cwd. Uses the platform echo so it works on unix and Windows runners.
func TestRunCommandTool(t *testing.T) {
	inst := &instance.Instance{Root: t.TempDir()}
	var tl config.Tool
	if runtime.GOOS == "windows" {
		tl = config.Tool{Name: "say", Extra: map[string]any{"command": []any{"cmd", "/c", "echo {msg}"}}}
	} else {
		tl = config.Tool{Name: "say", Extra: map[string]any{"command": []any{"sh", "-c", "echo {msg}"}}}
	}
	rr := Run(inst, tl, map[string]any{"msg": "hello-ratchet"})
	if rr.Error != "" {
		t.Fatalf("run error: %s", rr.Error)
	}
	if !rr.Ok || !strings.Contains(rr.Output, "hello-ratchet") {
		t.Fatalf("run output wrong: ok=%v out=%q", rr.Ok, rr.Output)
	}
}

func TestRunNonzeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exit-code shape covered on unix")
	}
	inst := &instance.Instance{Root: t.TempDir()}
	tl := config.Tool{Name: "fail", Extra: map[string]any{"command": []any{"sh", "-c", "exit 3"}}}
	rr := Run(inst, tl, nil)
	if rr.Ok || rr.ExitCode != 3 {
		t.Fatalf("expected exit 3, got ok=%v code=%d", rr.Ok, rr.ExitCode)
	}
	if !strings.Contains(rr.Output, "[exit code 3]") {
		t.Fatalf("output should note exit code: %q", rr.Output)
	}
}

func TestRunMissingExec(t *testing.T) {
	inst := &instance.Instance{Root: t.TempDir()}
	tl := config.Tool{Name: "ghost", Extra: map[string]any{"command": []any{"definitely-not-a-real-binary-xyz"}}}
	rr := Run(inst, tl, nil)
	if rr.Error == "" {
		t.Fatal("expected a launch error for a missing binary")
	}
}

func TestRunShellEcho(t *testing.T) {
	out := RunShell(t.TempDir(), "echo shell-works", 10000)
	if !strings.Contains(out, "shell-works") {
		t.Fatalf("RunShell output: %q", out)
	}
}
