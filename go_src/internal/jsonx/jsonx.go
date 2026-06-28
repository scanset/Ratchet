// Package jsonx is the dynamic-JSON helper layer: parse/serialize plus the navigation and
// schema-builder helpers the engine uses for Ollama request bodies, JSON schemas, and JSON-RPC.
// It is the hybrid port of src.bak/Json.cs - typed structs (encoding/json) handle the known,
// file-shaped data elsewhere; this package covers the genuinely dynamic surfaces.
//
// Parsed shape (Parse): JSON object -> map[string]any, array -> []any, number -> json.Number,
// strings/bools/null as-is. Numbers are kept as json.Number so whole values round-trip without a
// trailing ".0", matching the C# output.
package jsonx

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Parse turns arbitrary JSON text into the dynamic value graph described above.
func Parse(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Serialize marshals v to compact JSON. Errors (only possible for unsupported types we never pass)
// yield "".
func Serialize(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// SerializePretty marshals v to 2-space-indented JSON without HTML-escaping (<, >, & stay literal),
// for human-readable generated files. No trailing newline, matching the C# output. Empty objects and
// arrays render inline as {} / [].
func SerializePretty(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

// --- builders: construct JSON objects / grammar schemas for Ollama and JSON-RPC ---

// Obj builds an object from alternating key, value, key, value ... arguments. Keys must be strings.
func Obj(kv ...any) map[string]any {
	d := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			d[k] = kv[i+1]
		}
	}
	return d
}

// StrProp is a {"type":"string"} property.
func StrProp() map[string]any { return Obj("type", "string") }

// EnumProp is a {"type":"string","enum":[...]} property.
func EnumProp(values []string) map[string]any {
	arr := make([]any, len(values))
	for i, v := range values {
		arr[i] = v
	}
	return Obj("type", "string", "enum", arr)
}

// Schema builds {"type":"object","properties":{...},"required":[...]}. required is always an array
// (empty when none given), matching the C# builder.
func Schema(properties map[string]any, required ...string) map[string]any {
	req := make([]any, len(required))
	for i, r := range required {
		req[i] = r
	}
	return Obj("type", "object", "properties", properties, "required", req)
}

// --- navigation: the serde_json Value::get / as_str / as_array analogues ---

// AsObject returns v as a JSON object, or nil.
func AsObject(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// GetObject returns the child object at key, or nil.
func GetObject(obj map[string]any, key string) map[string]any {
	if obj == nil {
		return nil
	}
	return AsObject(obj[key])
}

// GetString returns the string at key and whether it was present AND a string (the C# nullable
// GetString: ok==false stands in for null). Use GetStringOr when you only want a fallback.
func GetString(obj map[string]any, key string) (string, bool) {
	if obj == nil {
		return "", false
	}
	v, ok := obj[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// GetStringOr returns the string at key, or fallback when absent/non-string.
func GetStringOr(obj map[string]any, key, fallback string) string {
	if s, ok := GetString(obj, key); ok {
		return s
	}
	return fallback
}

// GetBool returns the bool at key, or fallback when absent/non-bool.
func GetBool(obj map[string]any, key string, fallback bool) bool {
	if obj == nil {
		return fallback
	}
	if v, ok := obj[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return fallback
}

// GetNumber returns the numeric value at key as float64 and whether it was present and numeric.
func GetNumber(obj map[string]any, key string) (float64, bool) {
	if obj == nil {
		return 0, false
	}
	v, ok := obj[key]
	if !ok {
		return 0, false
	}
	return ToDouble(v)
}

// ToDouble coerces a dynamic JSON scalar to float64. It handles json.Number, the CLR-ish numeric
// types Go may produce (float64, int, int64), and a numeric string (the C# string fallback).
func ToDouble(v any) (float64, bool) {
	switch n := v.(type) {
	case nil:
		return 0, false
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// GetArr returns the array at key as a slice, or an empty slice.
func GetArr(obj map[string]any, key string) []any {
	if obj == nil {
		return []any{}
	}
	if v, ok := obj[key]; ok {
		return AsArr(v)
	}
	return []any{}
}

// AsArr returns v as a slice, or an empty slice.
func AsArr(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{}
}

// Pointer navigates a slash path like "/params/name" through nested objects (the serde_json
// Value::pointer analogue used by the MCP handler). Returns nil on any miss.
func Pointer(root any, pointer string) any {
	if pointer == "" {
		return root
	}
	cur := root
	for _, token := range strings.Split(pointer, "/") {
		if token == "" {
			continue // leading empty from the first '/'
		}
		if obj := AsObject(cur); obj != nil {
			next, ok := obj[token]
			if !ok {
				return nil
			}
			cur = next
			continue
		}
		if arr, ok := cur.([]any); ok { // array index token (JSON Pointer: e.g. selections/0/kb)
			idx, err := strconv.Atoi(token)
			if err != nil || idx < 0 || idx >= len(arr) {
				return nil
			}
			cur = arr[idx]
			continue
		}
		return nil
	}
	return cur
}
