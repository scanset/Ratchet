package jsonx

import (
	"strings"
	"testing"
)

// Ports SelfTest.JsonRoundtrip: build -> serialize -> parse -> navigate.
func TestRoundtrip(t *testing.T) {
	o := Obj("a", "x", "n", 2)
	back := AsObject(mustParse(t, Serialize(o)))
	if s, _ := GetString(back, "a"); s != "x" {
		t.Fatalf("a: want x, got %q", s)
	}
	n, ok := GetNumber(back, "n")
	if !ok || n != 2 {
		t.Fatalf("n: want 2, got %v ok=%v", n, ok)
	}
}

// Ports SelfTest.JsonSchema: schema builder shape + enum prop.
func TestSchema(t *testing.T) {
	s := Schema(Obj("x", StrProp(), "k", EnumProp([]string{"a", "b"})), "x")
	if typ, _ := GetString(s, "type"); typ != "object" {
		t.Fatalf("type: want object, got %q", typ)
	}
	req := GetArr(s, "required")
	if len(req) != 1 || req[0] != "x" {
		t.Fatalf("required: want [x], got %v", req)
	}
	k := GetObject(GetObject(s, "properties"), "k")
	if len(GetArr(k, "enum")) != 2 {
		t.Fatalf("enum: want 2, got %d", len(GetArr(k, "enum")))
	}
}

// Ports SelfTest.JsonPretty: round-trips, indents, leaves printable chars unescaped, empty array inline.
func TestPretty(t *testing.T) {
	pretty := SerializePretty(Obj("note", "a<b's", "list", []any{1, 2}, "empty", []any{}))
	back := AsObject(mustParse(t, pretty))
	if back == nil {
		t.Fatal("pretty did not round-trip to an object")
	}
	if s, _ := GetString(back, "note"); s != "a<b's" {
		t.Fatalf("note: want a<b's (unescaped), got %q", s)
	}
	if !strings.Contains(pretty, "\n  ") {
		t.Fatalf("pretty not indented:\n%s", pretty)
	}
	if !strings.Contains(pretty, "[]") {
		t.Fatalf("empty array not inline:\n%s", pretty)
	}
	if len(GetArr(back, "list")) != 2 {
		t.Fatalf("list: want 2 items")
	}
}

func TestPointer(t *testing.T) {
	root := mustParse(t, `{"params":{"name":"go","arguments":{"k":1}}}`)
	if got := Pointer(root, "/params/name"); got != "go" {
		t.Fatalf("pointer name: want go, got %v", got)
	}
	if got := Pointer(root, "/params/missing"); got != nil {
		t.Fatalf("missing pointer: want nil, got %v", got)
	}
	if got := Pointer(root, ""); got == nil {
		t.Fatalf("empty pointer should return root")
	}
}

func TestPointerArrayIndex(t *testing.T) {
	root := mustParse(t, `{"selections":[{"kb":"cache","query":"ttl"},{"kb":"concurrency","query":"fanin"}]}`)
	if got := Pointer(root, "/selections/0/kb"); got != "cache" {
		t.Fatalf("selections/0/kb: want cache, got %v", got)
	}
	if got := Pointer(root, "/selections/1/query"); got != "fanin" {
		t.Fatalf("selections/1/query: want fanin, got %v", got)
	}
	if got := Pointer(root, "/selections/9/kb"); got != nil {
		t.Fatalf("out-of-range index: want nil, got %v", got)
	}
	if got := Pointer(root, "/selections/x/kb"); got != nil {
		t.Fatalf("non-numeric array token: want nil, got %v", got)
	}
}

func TestNumberFidelity(t *testing.T) {
	// whole numbers serialize without a trailing ".0"
	if got := Serialize(Obj("k", 3)); !strings.Contains(got, `"k":3`) {
		t.Fatalf("whole number: want k:3, got %s", got)
	}
	// parsed json.Number coerces via ToDouble
	v := mustParse(t, `{"n": 42}`)
	n, ok := GetNumber(AsObject(v), "n")
	if !ok || n != 42 {
		t.Fatalf("parsed number: want 42, got %v ok=%v", n, ok)
	}
}

func TestGetStringPresence(t *testing.T) {
	o := Obj("a", "x", "z", nil)
	if _, ok := GetString(o, "a"); !ok {
		t.Fatal("present string should be ok")
	}
	if _, ok := GetString(o, "missing"); ok {
		t.Fatal("absent key should not be ok")
	}
	if _, ok := GetString(o, "z"); ok {
		t.Fatal("null value should not be ok (C# null)")
	}
	if got := GetStringOr(o, "missing", "fb"); got != "fb" {
		t.Fatalf("GetStringOr: want fb, got %q", got)
	}
}

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
