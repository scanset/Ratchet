package dispatch

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
	"github.com/scanset/Ratchet/internal/ollama"
	"github.com/scanset/Ratchet/internal/oracle"
)

// Validate runs the oracle on a table: against tsv if non-empty, else samples/<table>.txt on disk.
func (d *Dispatcher) Validate(table, tsv string) model.ValidateResult {
	res := model.ValidateResult{Table: table}
	if table == "" {
		res.Problems = []model.Problem{{Row: 0, Col: "(table)", Msg: "no table name given"}}
		return res
	}
	schema, err := model.LoadTableSchema(d.inst.SchemaPath(table))
	if err != nil {
		res.Problems = []model.Problem{{Row: 0, Col: "(schema)", Msg: err.Error()}}
		return res
	}
	data := tsv
	if data == "" {
		data, err = d.inst.ReadAt(d.inst.SamplePath(table))
		if err != nil {
			res.Problems = []model.Problem{{Row: 0, Col: "(file)", Msg: err.Error()}}
			return res
		}
	}
	d.status("oracle: validating '" + table + "' against its schema")
	res.Problems = oracle.ValidateTsv(schema, data, oracle.BuildRefs(d.inst))
	res.Ok = len(res.Problems) == 0
	return res
}

func (d *Dispatcher) doPropose(query string, r *model.TurnResult) {
	table := d.pickTable(query)
	if table == "" {
		r.Text = "No table schemas to propose into (need schemas/<table>.json)."
		r.IsError = true
		return
	}
	pr := d.ProposeRow(table, query)
	r.Text = FormatPropose(pr)
	r.IsError = !pr.Ok
	if pr.Ok {
		r.ProposedTable = pr.Table
		r.ProposedRow = pr.Row
	}
}

// ProposeRow has the model propose a row, the oracle gate it, with bounded repair.
func (d *Dispatcher) ProposeRow(table, request string) model.ProposeResult {
	d.cancel = ollama.NewCancel()
	res := model.ProposeResult{Table: table}

	schema, err := model.LoadTableSchema(d.inst.SchemaPath(table))
	if err != nil {
		res.Error = err.Error()
		return res
	}

	header := d.tableHeader(table)
	if header == "" {
		var names []string
		for _, c := range schema.Columns {
			names = append(names, c.Name)
		}
		header = strings.Join(names, "\t")
	}
	res.Header = header
	cols := strings.Split(header, "\t")

	props := map[string]any{}
	for _, c := range cols {
		props[c] = jsonx.StrProp()
	}
	genSchema := jsonx.Schema(props, cols...)

	basePrompt := "You are proposing exactly ONE new row for the tab-separated table '" + table +
		"' in the domain: " + d.inst.Config.Domain + ".\n" +
		"Columns in order and their constraints:\n" + describeColumns(cols, schema) +
		d.exampleBlock(table) +
		"Rules: give a value for EVERY column; numbers must be PLAIN digits only (no commas, " +
		"units, quotes, or thousands separators) and within any stated range; booleans as 0 or 1; " +
		"enum columns must be EXACTLY one of the listed values.\n" +
		"Request: " + request + "\nReturn JSON with one field per column name."

	prompt := basePrompt
	for attempt := 0; attempt <= maxProposeRepairs; attempt++ {
		d.status("propose: generating row (attempt " + strconv.Itoa(attempt+1) + ")")
		temp := 0.2
		if attempt > 0 {
			temp = 0.3
		}
		v, err := ollama.GenerateJSON(d.url, d.inst.Config.Models.Generate, prompt, genSchema, temp, genTimeoutMs, d.cancel)
		if err != nil {
			res.Error = err.Error()
			return res
		}

		var cells []string
		for _, c := range cols {
			val := jsonx.GetStringOr(v, c, "")
			val = strings.TrimSpace(strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(val))
			cells = append(cells, val)
		}
		row := strings.Join(cells, "\t")
		res.Row = row
		res.Attempts = attempt + 1

		problems := oracle.ValidateTsv(schema, header+"\n"+row, nil)
		headerBad := false
		for _, p := range problems {
			if p.Row == 0 {
				headerBad = true
			}
		}
		if headerBad {
			res.Problems = problems
			res.Error = "schema/header mismatch for '" + table + "' (a declared column is missing from the table header)"
			return res
		}
		if len(problems) == 0 {
			res.Ok = true
			d.status("propose: PASS")
			return res
		}

		res.Problems = problems
		d.status("propose: FAIL (" + strconv.Itoa(len(problems)) + " problem(s)), repairing")
		if attempt == maxProposeRepairs {
			break
		}
		var sb strings.Builder
		sb.WriteString(basePrompt)
		sb.WriteString("\n\nYour previous row FAILED validation:\n")
		for _, p := range problems {
			sb.WriteString("  " + p.String() + "\n")
		}
		sb.WriteString("Previous row (tab-separated): " + row + "\nReturn a corrected JSON row.")
		prompt = sb.String()
	}
	return res
}

func (d *Dispatcher) pickTable(query string) string {
	tables := d.SchemaTables()
	if len(tables) == 0 {
		return ""
	}
	if len(tables) == 1 {
		return tables[0]
	}
	schema := jsonx.Schema(jsonx.Obj("table", jsonx.EnumProp(tables)), "table")
	prompt := "Pick which table this request adds a row to.\nTables: " +
		strings.Join(tables, ", ") + "\nRequest: " + query
	v, err := ollama.GenerateJSON(d.url, d.inst.Config.DispatchModel(), prompt, schema, 0.1, dispatchTimeoutMs, d.cancel)
	if err != nil {
		return tables[0]
	}
	return jsonx.GetStringOr(v, "table", tables[0])
}

// SchemaTables lists the schema table names under schemas/.
func (d *Dispatcher) SchemaTables() []string {
	var out []string
	dir := d.inst.SchemasDirAbs()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return out
	}
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	return out
}

func (d *Dispatcher) tableHeader(table string) string {
	data, err := d.inst.ReadAt(d.inst.SamplePath(table))
	if err != nil {
		return ""
	}
	lines := oracle.NonEmptyLines(data)
	if len(lines) > 0 {
		return lines[0]
	}
	return ""
}

func (d *Dispatcher) exampleBlock(table string) string {
	data, err := d.inst.ReadAt(d.inst.SamplePath(table))
	if err != nil {
		return ""
	}
	lines := oracle.NonEmptyLines(data)
	if len(lines) <= 1 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Existing example rows (tab-separated):\n")
	for i := 1; i < len(lines) && i <= 2; i++ {
		sb.WriteString(lines[i] + "\n")
	}
	return sb.String()
}

func describeColumns(cols []string, schema *model.TableSchema) string {
	byName := map[string]model.ColSpec{}
	for _, c := range schema.Columns {
		byName[c.Name] = c
	}
	var sb strings.Builder
	for _, c := range cols {
		cs, ok := byName[c]
		if !ok {
			sb.WriteString("- " + c + ": string (free text)\n")
			continue
		}
		sb.WriteString("- " + c + ": " + cs.CType)
		if cs.Required {
			sb.WriteString(", required")
		}
		if cs.Min != nil || cs.Max != nil {
			lo, hi := "*", "*"
			if cs.Min != nil {
				lo = numStr(*cs.Min)
			}
			if cs.Max != nil {
				hi = numStr(*cs.Max)
			}
			sb.WriteString(", range " + lo + ".." + hi)
		}
		if len(cs.Values) > 0 {
			sb.WriteString(", one of: " + strings.Join(cs.Values, "|"))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func numStr(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}

// FormatPropose renders a propose result the way the C# Dispatcher.FormatPropose did.
func FormatPropose(pr model.ProposeResult) string {
	if pr.Ok {
		return "PASS - proposed row for '" + pr.Table + "' (validated in " + strconv.Itoa(pr.Attempts) + " attempt(s)):\n" + pr.Row
	}
	if pr.Error != "" && len(pr.Problems) == 0 {
		return "[error] " + pr.Error
	}
	var sb strings.Builder
	sb.WriteString("FAIL - no valid row for '" + pr.Table + "' after " + strconv.Itoa(pr.Attempts) + " attempt(s).\n")
	if pr.Error != "" {
		sb.WriteString(pr.Error + "\n")
	}
	sb.WriteString("Last row: " + pr.Row + "\n")
	for _, p := range pr.Problems {
		sb.WriteString("  " + p.String() + "\n")
	}
	return sb.String()
}
