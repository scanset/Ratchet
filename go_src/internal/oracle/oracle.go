// Package oracle is the deterministic verifier: a schema-driven TSV validator (the engine's
// "compiler") plus the flow/tool namespace-uniqueness check and the shared TSV line handling. The
// model proposes an edited row; this says yes or no, deterministically. Port of
// src.bak/Runtime/{Oracle,Tsv}.cs.
//
// Checks: header has the columns the schema names; every row has the right column COUNT (the classic
// tab-corruption catch); each typed cell parses and sits in range; enum cells are in the allowed set;
// and ref cells resolve against another table's id set when one is provided.
package oracle

import (
	"math"
	"strconv"
	"strings"

	"github.com/scanset/Ratchet/internal/model"
)

// RefSet maps a table name to the set of valid ids in its key column (for cross-table ref checks).
type RefSet map[string]map[string]bool

// IdSet builds the id set for a table from its schema key column.
func IdSet(schema *model.TableSchema, tsv string) map[string]bool {
	out := map[string]bool{}
	if schema.Key == "" {
		return out
	}
	table := Rows(tsv)
	if len(table) == 0 {
		return out
	}
	header := table[0]
	ki := indexOf(header, schema.Key)
	if ki < 0 {
		return out
	}
	for r := 1; r < len(table); r++ {
		if ki < len(table[r]) {
			v := table[r][ki]
			if strings.TrimSpace(v) != "" {
				out[v] = true
			}
		}
	}
	return out
}

// NamespaceProblems checks the flow + tool name namespace: names must be globally UNIQUE so flat
// routing (run a flow or tool by name) is unambiguous. Reports duplicate flow names, duplicate tool
// names, and any name used by BOTH a flow and a tool.
func NamespaceProblems(flowNames, toolNames []string) []string {
	var problems []string
	problems = append(problems, dups(flowNames, "flow")...)
	problems = append(problems, dups(toolNames, "tool")...)
	toolSet := map[string]bool{}
	for _, t := range toolNames {
		toolSet[strings.ToLower(t)] = true
	}
	reported := map[string]bool{}
	for _, f := range flowNames {
		lf := strings.ToLower(f)
		if toolSet[lf] && !reported[lf] {
			reported[lf] = true
			problems = append(problems, "name '"+f+"' is used by BOTH a flow and a tool (names must be unique)")
		}
	}
	return problems
}

func dups(names []string, kind string) []string {
	var problems []string
	seen := map[string]bool{}
	dup := map[string]bool{}
	for _, n := range names {
		ln := strings.ToLower(n)
		if seen[ln] {
			if !dup[ln] {
				dup[ln] = true
				problems = append(problems, "duplicate "+kind+" name '"+n+"'")
			}
		} else {
			seen[ln] = true
		}
	}
	return problems
}

// ValidateTsv validates tsv against schema. Pass nil refs to skip ref resolution (single-table
// validation); pass a built map (table name -> valid ids) for cross-table integrity.
func ValidateTsv(schema *model.TableSchema, tsv string, refs RefSet) []model.Problem {
	var problems []model.Problem
	table := Rows(tsv)
	if len(table) == 0 {
		return []model.Problem{{Row: 0, Col: "(file)", Msg: "empty file"}}
	}
	header := table[0]
	ncols := len(header)

	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i // last wins, mirrors the C# insert
	}

	type target struct {
		col model.ColSpec
		i   int
	}
	var targets []target
	for _, c := range schema.Columns {
		if i, ok := idx[c.Name]; ok {
			targets = append(targets, target{c, i})
		} else {
			problems = append(problems, model.Problem{Row: 0, Col: c.Name, Msg: "column declared in schema is missing from the header"})
		}
	}

	for ri := 1; ri < len(table); ri++ {
		r := table[ri]
		// THE tab-corruption catch: every row must have the header's column count.
		if len(r) != ncols {
			problems = append(problems, model.Problem{Row: ri, Col: "(row)",
				Msg: "has " + strconv.Itoa(len(r)) + " columns, expected " + strconv.Itoa(ncols) + " (tab added/dropped?)"})
			continue // index-based cell checks would be meaningless on a misaligned row
		}
		for _, t := range targets {
			c := t.col
			cell := strings.TrimSpace(r[t.i])
			if cell == "" {
				if c.Required {
					problems = append(problems, model.Problem{Row: ri, Col: c.Name, Msg: "required, but empty"})
				}
				continue
			}
			switch c.CType {
			case "int":
				if n, err := strconv.ParseInt(cell, 10, 64); err == nil {
					checkRange(&problems, ri, c, float64(n))
				} else {
					problems = append(problems, model.Problem{Row: ri, Col: c.Name, Msg: "'" + cell + "' is not an integer"})
				}
			case "float":
				if n, err := strconv.ParseFloat(cell, 64); err == nil {
					checkRange(&problems, ri, c, n)
				} else {
					problems = append(problems, model.Problem{Row: ri, Col: c.Name, Msg: "'" + cell + "' is not a number"})
				}
			case "bool":
				if cell != "0" && cell != "1" {
					problems = append(problems, model.Problem{Row: ri, Col: c.Name, Msg: "'" + cell + "' must be 0 or 1"})
				}
			case "enum":
				if !containsStr(c.Values, cell) {
					problems = append(problems, model.Problem{Row: ri, Col: c.Name, Msg: "'" + cell + "' not in [" + strings.Join(c.Values, ", ") + "]"})
				}
			case "ref":
				if c.RefTable != "" && refs != nil {
					set, ok := refs[c.RefTable]
					if !ok || !set[cell] {
						problems = append(problems, model.Problem{Row: ri, Col: c.Name, Msg: "'" + cell + "' not found in table '" + c.RefTable + "'"})
					}
				}
				// refs == nil: single-table mode, ref integrity skipped by design
			default:
				// "string": any non-empty value is fine
			}
		}
	}
	return problems
}

func checkRange(problems *[]model.Problem, ri int, c model.ColSpec, n float64) {
	if c.Min != nil && n < *c.Min {
		*problems = append(*problems, model.Problem{Row: ri, Col: c.Name, Msg: num(n) + " < min " + num(*c.Min)})
	}
	if c.Max != nil && n > *c.Max {
		*problems = append(*problems, model.Problem{Row: ri, Col: c.Name, Msg: num(n) + " > max " + num(*c.Max)})
	}
}

// num renders a number without a trailing ".0" for whole values, matching the C# output.
func num(n float64) string {
	if !math.IsInf(n, 0) && n == math.Floor(n) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
