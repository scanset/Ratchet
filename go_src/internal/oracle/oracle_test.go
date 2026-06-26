package oracle

import (
	"testing"

	"github.com/scanset/Ratchet/internal/model"
)

func fptr(f float64) *float64 { return &f }

// Ports SelfTest.TsvHandling.
func TestTsv(t *testing.T) {
	lines := NonEmptyLines("a\r\n\n  \nb\n")
	rows := Rows("h1\th2\nc\td")
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("lines wrong: %v", lines)
	}
	if len(rows) != 2 || len(rows[0]) != 2 || rows[1][1] != "d" {
		t.Fatalf("rows wrong: %v", rows)
	}
}

func demoSchema() *model.TableSchema {
	return &model.TableSchema{Columns: []model.ColSpec{
		{Name: "Id", CType: "int", Required: true, Min: fptr(1), Max: fptr(99)},
		{Name: "name", CType: "string"},
		{Name: "cls", CType: "enum", Values: []string{"a", "b"}},
	}}
}

// Ports SelfTest.OraclePass.
func TestOraclePass(t *testing.T) {
	if got := len(ValidateTsv(demoSchema(), "Id\tname\tcls\n5\tx\ta", nil)); got != 0 {
		t.Fatalf("expected clean pass, got %d problems", got)
	}
}

// Ports SelfTest.OracleFaults.
func TestOracleFaults(t *testing.T) {
	rangeBad := len(ValidateTsv(demoSchema(), "Id\tname\tcls\n200\tx\tz", nil)) // 200>99 + z not enum
	count := len(ValidateTsv(demoSchema(), "Id\tname\tcls\n5\tx", nil))         // wrong column count
	notInt := len(ValidateTsv(demoSchema(), "Id\tname\tcls\nxx\tx\ta", nil))    // Id not int
	if rangeBad != 2 || count != 1 || notInt != 1 {
		t.Fatalf("faults wrong: range=%d count=%d notInt=%d", rangeBad, count, notInt)
	}
}

// Ports SelfTest.CrossTableRefs.
func TestCrossTableRefs(t *testing.T) {
	s := &model.TableSchema{Name: "drops", Columns: []model.ColSpec{
		{Name: "Id", CType: "int"},
		{Name: "item", CType: "ref", RefTable: "items"},
	}}
	refs := RefSet{"items": {"gold": true, "gem": true}}
	hit := len(ValidateTsv(s, "Id\titem\n1\tgold", refs)) // in set -> 0
	miss := len(ValidateTsv(s, "Id\titem\n1\tzzz", refs)) // not in set -> 1
	skip := len(ValidateTsv(s, "Id\titem\n1\tzzz", nil))  // nil refs -> skipped -> 0
	if hit != 0 || miss != 1 || skip != 0 {
		t.Fatalf("refs wrong: hit=%d miss=%d skip=%d", hit, miss, skip)
	}
}

// Ports SelfTest.NamespaceCheck.
func TestNamespace(t *testing.T) {
	flows := []string{"answer", "csharp", "write_grounded"}
	tools := []string{"csc_check", "build_project"}
	if len(NamespaceProblems(flows, tools)) != 0 {
		t.Fatal("clean namespace should report no problems")
	}
	f2 := []string{"answer", "answer", "build"} // dup flow + flow==tool
	t2 := []string{"csc", "csc", "build"}       // dup tool + tool==flow
	if len(NamespaceProblems(f2, t2)) < 3 {
		t.Fatalf("expected >=3 problems, got %d", len(NamespaceProblems(f2, t2)))
	}
}

func TestIdSet(t *testing.T) {
	s := &model.TableSchema{Key: "Id", Columns: []model.ColSpec{{Name: "Id", CType: "int"}}}
	set := IdSet(s, "Id\tname\n1\tx\n2\ty")
	if !set["1"] || !set["2"] || len(set) != 2 {
		t.Fatalf("id set wrong: %v", set)
	}
}
