package chain

import (
	"testing"

	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
)

// Ports SelfTest.ChainLintCheck.
func TestLint(t *testing.T) {
	var tools []string

	// clean: ai_branch with matching transitions/enum -> exit.
	good := &model.Chain{Entry: "c.start", NodeIds: []string{"c.start", "c.done"}, Actions: map[string]model.ActionNode{}}
	s := model.ActionNode{ID: "c.start", Kind: "ai_branch", Prompt: "./prompt.md",
		Transitions:  map[string]string{"go": "c.done", "stop": "c.done"},
		OutputSchema: jsonx.Obj("properties", jsonx.Obj("next", jsonx.Obj("enum", []any{"go", "stop"})))}
	good.Actions["c.start"] = s
	good.Actions["c.done"] = model.ActionNode{ID: "c.done", Kind: "exit", Outcome: "success"}
	if got := Lint(good, tools); len(got) != 0 {
		t.Fatalf("clean chain should lint clean, got: %v", got)
	}

	// broken: one transition, an edge to a missing node, transitions != enum.
	bad := &model.Chain{Entry: "c.start", NodeIds: []string{"c.start"}, Actions: map[string]model.ActionNode{}}
	bad.Actions["c.start"] = model.ActionNode{ID: "c.start", Kind: "ai_branch", Prompt: "./prompt.md",
		Transitions:  map[string]string{"go": "c.missing"},
		OutputSchema: jsonx.Obj("properties", jsonx.Obj("next", jsonx.Obj("enum", []any{"go", "other"})))}
	if got := Lint(bad, tools); len(got) < 3 {
		t.Fatalf("broken chain should have >=3 problems, got %d: %v", len(got), got)
	}

	// Flavor A: a generate prompt references an unbound slot -> must lint; binding it clears the lint.
	unbound := &model.Chain{Entry: "g.gen", NodeIds: []string{"g.gen", "g.done"}, Actions: map[string]model.ActionNode{}}
	gen := model.ActionNode{ID: "g.gen", Kind: "generate", PromptText: "fix {{ task }} given {{ errors }}", OnSuccess: "g.done",
		Inputs: []model.InputBinding{{As: "task", Source: "from", From: "$input", Path: "."}}}
	unbound.Actions["g.gen"] = gen
	unbound.Actions["g.done"] = model.ActionNode{ID: "g.done", Kind: "exit", Outcome: "success"}
	if got := Lint(unbound, tools); len(got) == 0 {
		t.Fatal("unbound {{ errors }} should lint")
	}
	gen.Inputs = append(gen.Inputs, model.InputBinding{As: "errors", Source: "from", From: "$input", Path: "."})
	unbound.Actions["g.gen"] = gen
	if got := Lint(unbound, tools); len(got) != 0 {
		t.Fatalf("all slots bound should lint clean, got: %v", got)
	}

	// Flavor B: a search query references a slot bound BELOW it -> must lint; reordering clears it.
	fwd := &model.Chain{Entry: "s.gen", NodeIds: []string{"s.gen", "s.done"}, Actions: map[string]model.ActionNode{}}
	sgen := model.ActionNode{ID: "s.gen", Kind: "generate", PromptText: "{{ refs }}\n{{ task }}", OnSuccess: "s.done",
		Inputs: []model.InputBinding{
			{As: "refs", Source: "search", Lib: "kb", Query: "{{ task }}"}, // task bound below
			{As: "task", Source: "from", From: "$input", Path: "."},
		}}
	fwd.Actions["s.gen"] = sgen
	fwd.Actions["s.done"] = model.ActionNode{ID: "s.done", Kind: "exit", Outcome: "success"}
	if got := Lint(fwd, tools); len(got) == 0 {
		t.Fatal("search query reading a slot bound below it should lint")
	}
	sgen.Inputs = []model.InputBinding{
		{As: "task", Source: "from", From: "$input", Path: "."},
		{As: "refs", Source: "search", Lib: "kb", Query: "{{ task }}"},
	}
	fwd.Actions["s.gen"] = sgen
	if got := Lint(fwd, tools); len(got) != 0 {
		t.Fatalf("reordered search should lint clean, got: %v", got)
	}

	// Feedback cycle: plan -> gate, gate fails back to plan, and plan binds gate's output (empty on the
	// first pass). The back-edge is legitimate because the cycle closes, so it must lint clean.
	cyc := &model.Chain{Entry: "p.plan", NodeIds: []string{"p.plan", "p.gate", "p.done", "p.fail"}, Actions: map[string]model.ActionNode{}}
	cyc.Actions["p.plan"] = model.ActionNode{ID: "p.plan", Kind: "generate", PromptText: "{{ task }}{{ fb }}", OnSuccess: "p.gate", OnFailure: "p.fail",
		Inputs: []model.InputBinding{
			{As: "task", Source: "from", From: "$input", Path: "."},
			{As: "fb", Source: "from", From: "p.gate", Path: "."}, // binds a downstream node (the loop)
		}}
	cyc.Actions["p.gate"] = model.ActionNode{ID: "p.gate", Kind: "action", Tool: "t", OnSuccess: "p.done", OnFailure: "p.plan"}
	cyc.Actions["p.done"] = model.ActionNode{ID: "p.done", Kind: "exit", Outcome: "success"}
	cyc.Actions["p.fail"] = model.ActionNode{ID: "p.fail", Kind: "exit", Outcome: "fail"}
	if got := Lint(cyc, []string{"t"}); len(got) != 0 {
		t.Fatalf("feedback cycle (plan<->gate) should lint clean, got: %v", got)
	}

	// Non-cycle forward reference: a node binds a downstream node it does NOT loop back from -> must flag.
	noc := &model.Chain{Entry: "n.a", NodeIds: []string{"n.a", "n.b", "n.done"}, Actions: map[string]model.ActionNode{}}
	noc.Actions["n.a"] = model.ActionNode{ID: "n.a", Kind: "generate", PromptText: "{{ later }}", OnSuccess: "n.b",
		Inputs: []model.InputBinding{{As: "later", Source: "from", From: "n.b", Path: "."}}}
	noc.Actions["n.b"] = model.ActionNode{ID: "n.b", Kind: "action", Tool: "t", OnSuccess: "n.done", OnFailure: "n.done"}
	noc.Actions["n.done"] = model.ActionNode{ID: "n.done", Kind: "exit", Outcome: "success"}
	if got := Lint(noc, []string{"t"}); len(got) == 0 {
		t.Fatal("forward reference with no cycle should flag 'not a predecessor'")
	}
}
