package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scanset/Ratchet/internal/chain"
	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/dispatch"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/markdown"
	"github.com/scanset/Ratchet/internal/meta"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/oracle"
	"github.com/scanset/Ratchet/internal/search"
)

// SelfTest runs the deterministic core checks in-process (no model, no instance dir) and prints a
// per-check pass/fail line. Returns the number of failed checks. Mirrors src.bak/Cli/SelfTest.cs.
func SelfTest() int {
	fail := 0
	fail += check("json roundtrip + navigation", jsonRoundtrip)
	fail += check("json schema builders", jsonSchema)
	fail += check("json pretty-print", jsonPretty)
	fail += check("tsv lines + rows", tsvHandling)
	fail += check("oracle pass", oraclePass)
	fail += check("oracle catches faults", oracleFaults)
	fail += check("cross-table refs", crossTableRefs)
	fail += check("namespace uniqueness", namespaceCheck)
	fail += check("path conventions", pathConventions)
	fail += check("path-escape guard", pathGuard)
	fail += check("metadata block parse + strip", metaBlock)
	fail += check("markdown parse", markdownParse)
	fail += check("manifest enumeration helpers", manifestHelpers)
	fail += check("chain lint", chainLintCheck)
	fail += check("router gate", routerGate)
	fail += check("slash command parse", slashParse)
	fail += check("slash redirect + fence strip", slashRedirect)
	fail += check("embedder rank", embedderRank)

	if fail == 0 {
		fmt.Println("selftest: ALL PASS")
	} else {
		fmt.Printf("selftest: %d FAILED\n", fail)
	}
	return fail
}

func check(name string, test func() bool) int {
	ok := test()
	if ok {
		fmt.Println("  ok    " + name)
		return 0
	}
	fmt.Println("  FAIL  " + name)
	return 1
}

func jsonRoundtrip() bool {
	o := jsonx.Obj("a", "x", "n", 2)
	back := jsonx.AsObject(parse(jsonx.Serialize(o)))
	s, _ := jsonx.GetString(back, "a")
	n, ok := jsonx.GetNumber(back, "n")
	return s == "x" && ok && n == 2
}

func jsonSchema() bool {
	s := jsonx.Schema(jsonx.Obj("x", jsonx.StrProp(), "k", jsonx.EnumProp([]string{"a", "b"})), "x")
	typ, _ := jsonx.GetString(s, "type")
	req := jsonx.GetArr(s, "required")
	k := jsonx.GetObject(jsonx.GetObject(s, "properties"), "k")
	return typ == "object" && len(req) == 1 && req[0] == "x" && len(jsonx.GetArr(k, "enum")) == 2
}

func jsonPretty() bool {
	pretty := jsonx.SerializePretty(jsonx.Obj("note", "a<b's", "list", []any{1, 2}, "empty", []any{}))
	back := jsonx.AsObject(parse(pretty))
	note, _ := jsonx.GetString(back, "note")
	return back != nil && note == "a<b's" && strings.Contains(pretty, "\n  ") &&
		strings.Contains(pretty, "[]") && len(jsonx.GetArr(back, "list")) == 2
}

func tsvHandling() bool {
	lines := oracle.NonEmptyLines("a\r\n\n  \nb\n")
	rows := oracle.Rows("h1\th2\nc\td")
	return len(lines) == 2 && lines[0] == "a" && lines[1] == "b" &&
		len(rows) == 2 && len(rows[0]) == 2 && rows[1][1] == "d"
}

func demoSchema() *model.TableSchema {
	min1, max99 := 1.0, 99.0
	return &model.TableSchema{Columns: []model.ColSpec{
		{Name: "Id", CType: "int", Required: true, Min: &min1, Max: &max99},
		{Name: "name", CType: "string"},
		{Name: "cls", CType: "enum", Values: []string{"a", "b"}},
	}}
}

func oraclePass() bool {
	return len(oracle.ValidateTsv(demoSchema(), "Id\tname\tcls\n5\tx\ta", nil)) == 0
}

func oracleFaults() bool {
	r := len(oracle.ValidateTsv(demoSchema(), "Id\tname\tcls\n200\tx\tz", nil))
	c := len(oracle.ValidateTsv(demoSchema(), "Id\tname\tcls\n5\tx", nil))
	i := len(oracle.ValidateTsv(demoSchema(), "Id\tname\tcls\nxx\tx\ta", nil))
	return r == 2 && c == 1 && i == 1
}

func crossTableRefs() bool {
	s := &model.TableSchema{Name: "drops", Columns: []model.ColSpec{
		{Name: "Id", CType: "int"}, {Name: "item", CType: "ref", RefTable: "items"},
	}}
	refs := oracle.RefSet{"items": {"gold": true, "gem": true}}
	hit := len(oracle.ValidateTsv(s, "Id\titem\n1\tgold", refs))
	miss := len(oracle.ValidateTsv(s, "Id\titem\n1\tzzz", refs))
	skip := len(oracle.ValidateTsv(s, "Id\titem\n1\tzzz", nil))
	return hit == 0 && miss == 1 && skip == 0
}

func namespaceCheck() bool {
	if len(oracle.NamespaceProblems([]string{"answer", "csharp"}, []string{"csc", "build"})) != 0 {
		return false
	}
	return len(oracle.NamespaceProblems([]string{"answer", "answer", "build"}, []string{"csc", "csc", "build"})) >= 3
}

func pathConventions() bool {
	return conventions.SchemaRel("skills") == "schemas/skills.json" &&
		conventions.SampleRel("skills") == "samples/skills.txt" &&
		conventions.FlowRel("answer") == "flows/answer.json"
}

func pathGuard() bool {
	root, _ := filepath.Abs(os.TempDir())
	i := &instance.Instance{Root: root}
	if _, err := i.Resolve("sub/file.txt"); err != nil {
		return false
	}
	_, e1 := i.Resolve("../escape.txt")
	_, e2 := i.Resolve(`C:\Windows\System32`)
	return e1 != nil && e2 != nil
}

func metaBlock() bool {
	doc := "<!--icm\n{ \"id\": \"x\", \"keywords\": [\"a\", \"b\"] }\n-->\n# Title\n\nbody text"
	m := meta.ExtractMeta(doc)
	id, _ := jsonx.GetString(m, "id")
	if m == nil || id != "x" || len(jsonx.GetArr(m, "keywords")) != 2 {
		return false
	}
	stripped := meta.StripMeta(doc)
	if strings.Contains(stripped, "icm") || !strings.HasPrefix(stripped, "# Title") {
		return false
	}
	return meta.ExtractMeta("# plain\ntext") == nil && meta.StripMeta("# plain") == "# plain"
}

func markdownParse() bool {
	sp := markdown.ParseInline("use `csc` and **flags**")
	if len(sp) != 4 || sp[1].Style != markdown.CodeSpan || sp[1].Text != "csc" || sp[3].Style != markdown.Bold {
		return false
	}
	ln := markdown.ParseInline("see [docs](http://x)")
	if ln[1].Style != markdown.Link || ln[1].Href != "http://x" {
		return false
	}
	if markdown.StripFence("```csharp\nint x = 1;\n```") != "int x = 1;" {
		return false
	}
	doc := markdown.Parse("# Title\n```\ncode line\n```\n- item one")
	return doc[0].Kind == markdown.Heading && doc[0].Level == 1
}

func manifestHelpers() bool {
	m := &model.Manifest{Entries: []model.Entry{
		{ID: "a", Group: "creational", DocType: "pattern", Summary: "sa"},
		{ID: "b", Group: "structural", DocType: "pattern", Summary: "sb"},
		{ID: "c", Group: "creational", DocType: "pattern", Summary: "sc"},
	}}
	cat := m.Catalog("creational", "")
	return len(m.Groups()) == 2 && len(m.ByGroup("creational")) == 2 &&
		strings.Contains(cat, "a [creational]: sa") && !strings.Contains(cat, "- b")
}

func chainLintCheck() bool {
	var tools []string
	good := &model.Chain{Entry: "c.start", NodeIds: []string{"c.start", "c.done"}, Actions: map[string]model.ActionNode{}}
	good.Actions["c.start"] = model.ActionNode{ID: "c.start", Kind: "ai_branch", Prompt: "./prompt.md",
		Transitions:  map[string]string{"go": "c.done", "stop": "c.done"},
		OutputSchema: jsonx.Obj("properties", jsonx.Obj("next", jsonx.Obj("enum", []any{"go", "stop"})))}
	good.Actions["c.done"] = model.ActionNode{ID: "c.done", Kind: "exit", Outcome: "success"}
	return len(chain.Lint(good, tools)) == 0
}

func routerGate() bool {
	ids := []string{"answer", "csharp", "write_grounded"}
	return dispatch.Gate("csharp", "high", ids) == dispatch.GateMatch &&
		dispatch.Gate("csharp", "low", ids) == dispatch.GateFallback &&
		dispatch.Gate("none", "high", ids) == dispatch.GateFallback &&
		dispatch.Gate("bogus", "high", ids) == dispatch.GateFallback
}

func slashParse() bool {
	cmd, rest := dispatch.ParseCommand("/write a string reverser")
	if cmd != "write" || rest != "a string reverser" {
		return false
	}
	cmd, rest = dispatch.ParseCommand("/ASK   Foo bar ")
	return cmd == "ask" && rest == "Foo bar"
}

func slashRedirect() bool {
	clean, path := dispatch.ParseRedirect("a hex viewer > out/Hex.cs")
	if clean != "a hex viewer" || path != "out/Hex.cs" {
		return false
	}
	clean, path = dispatch.ParseRedirect("no redirect here")
	return path == "" && clean == "no redirect here"
}

func embedderRank() bool {
	q := []float64{1, 0}
	cands := []search.VecCand{
		{ID: "a", Vec: []float64{1, 0}}, {ID: "b", Vec: []float64{0, 1}}, {ID: "c", Vec: []float64{0.7, 0.7}},
	}
	top := search.RankByVectors(q, cands, 2)
	return len(top) == 2 && top[0] == "a" && top[1] == "c"
}

func parse(s string) any {
	v, err := jsonx.Parse(s)
	if err != nil {
		return nil
	}
	return v
}
