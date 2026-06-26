// Package mcp serves a ratchet over MCP (stdio JSON-RPC) so a STRONG orchestrator (Claude) can drive
// the same engine the local dispatcher drives. "Same server, two callers." tools/list advertises the
// instance's declared tools; tools/call dispatches by kind. The model never picks a tool here - the
// orchestrator does. Port of src.bak/Server/Mcp.cs.
package mcp

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/scanset/Ratchet/internal/config"
	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/dispatch"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/tool"
	"github.com/scanset/Ratchet/internal/version"
)

const (
	protocolVersion  = "2025-11-25"
	maxProblemsShown = 40
)

// Meter tallies the frontier I/O across the MCP boundary (chars each way, ~tokens = chars/4).
type meterT struct {
	inChars, outChars   int64
	requests, toolCalls int
}

var meter meterT

func (m *meterT) report() string {
	inTok := (m.inChars + 3) / 4
	outTok := (m.outChars + 3) / 4
	return fmt.Sprintf("frontier boundary meter (this MCP session)\n"+
		"  driver -> host (requests in): %d msgs, %d chars (~%d tok)\n"+
		"  host -> driver (results out): %d chars (~%d tok)\n"+
		"  tool calls: %d\n"+
		"  FRONTIER DRIVE COST (in + out): ~%d tok\n"+
		"note: ~tok = chars/4 incl. JSON-RPC overhead.", m.requests, m.inChars, inTok, m.outChars, outTok, m.toolCalls, inTok+outTok)
}

// Serve runs the MCP server over stdio until stdin closes. stdout carries protocol only; logs -> stderr.
func Serve(inst *instance.Instance, url string) {
	var toolNames []string
	for _, t := range inst.Tools() {
		toolNames = append(toolNames, t.Name)
	}
	fmt.Fprintln(os.Stderr, "[ratchet mcp] serving '"+inst.Config.Name+"' tools=["+strings.Join(toolNames, ", ")+"] @ "+url)

	disp := dispatch.New(inst, url, func(s string) { fmt.Fprintln(os.Stderr, "  - "+s) })

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		meter.inChars += int64(len(line))
		meter.requests++
		parsed, err := jsonx.Parse(line)
		if err != nil {
			continue
		}
		msg := jsonx.AsObject(parsed)
		if msg == nil {
			continue
		}
		resp := handle(inst, disp, msg)
		if resp != nil {
			s := jsonx.Serialize(resp)
			meter.outChars += int64(len(s))
			fmt.Fprintln(out, s)
			out.Flush()
		}
	}
	fmt.Fprintln(os.Stderr, "[ratchet mcp] "+strings.ReplaceAll(meter.report(), "\n", "\n[ratchet mcp] "))
}

func handle(inst *instance.Instance, disp *dispatch.Dispatcher, msg map[string]any) map[string]any {
	method := jsonx.GetStringOr(msg, "method", "")
	id, hasID := msg["id"]

	switch method {
	case "initialize":
		ver := protocolVersion
		if cv, ok := jsonx.Pointer(msg, "/params/protocolVersion").(string); ok && cv != "" {
			ver = cv
		}
		return ok(id, jsonx.Obj(
			"protocolVersion", ver,
			"capabilities", jsonx.Obj("tools", jsonx.Obj("listChanged", false)),
			"serverInfo", jsonx.Obj("name", inst.Config.Name, "version", version.Version)))
	case "notifications/initialized":
		return nil
	case "ping":
		return ok(id, map[string]any{})
	case "tools/list":
		return ok(id, toolsList(inst))
	case "tools/call":
		meter.toolCalls++
		name, _ := jsonx.Pointer(msg, "/params/name").(string)
		args := jsonx.AsObject(jsonx.Pointer(msg, "/params/arguments"))
		if args == nil {
			args = map[string]any{}
		}
		if name == "catalog" || name == "read_entry" || name == "meter" {
			return callBuiltin(id, inst, name, args)
		}
		for _, t := range inst.Tools() {
			if t.Name == name {
				return callTool(id, inst, disp, t, args)
			}
		}
		return errResp(id, -32602, "Unknown tool: "+name)
	default:
		if hasID {
			return errResp(id, -32601, "Method not found: "+method)
		}
		return nil
	}
}

func inputSchema(t config.Tool) any {
	if authored := t.InputSchema(); authored != nil {
		return authored
	}
	switch t.Kind {
	case conventions.ToolKindValidate:
		return jsonx.Schema(jsonx.Obj(
			"table", jsonx.Obj("type", "string", "description", "schema/table name to validate"),
			"tsv", jsonx.Obj("type", "string", "description", "table text to check (optional; else the file on disk)")),
			"table")
	case conventions.ToolKindKbAnswer:
		return jsonx.Schema(jsonx.Obj("question", jsonx.StrProp()), "question")
	case conventions.ToolKindPropose, conventions.ToolKindGenerateVerify:
		return jsonx.Schema(jsonx.Obj(
			"table", jsonx.Obj("type", "string", "description", "target table"),
			"request", jsonx.Obj("type", "string", "description", "what row to add")),
			"table", "request")
	default:
		return jsonx.Obj("type", "object", "properties", map[string]any{})
	}
}

func toolsList(inst *instance.Instance) map[string]any {
	var tools []any
	if inst.Manifest != nil {
		tools = append(tools, jsonx.Obj("name", "catalog",
			"description", "List this instance's KB entries (id, group, summary). Optional filters: group, doc_type.",
			"inputSchema", jsonx.Schema(jsonx.Obj("group", jsonx.StrProp(), "doc_type", jsonx.StrProp()))))
		tools = append(tools, jsonx.Obj("name", "read_entry",
			"description", "Read one KB entry's full text by id (routing metadata stripped).",
			"inputSchema", jsonx.Schema(jsonx.Obj("id", jsonx.StrProp()), "id")))
	}
	tools = append(tools, jsonx.Obj("name", "meter",
		"description", "Report this MCP session's frontier I/O so far.",
		"inputSchema", jsonx.Obj("type", "object", "properties", map[string]any{})))
	for _, t := range inst.Tools() {
		tools = append(tools, jsonx.Obj("name", t.Name, "description", t.Description, "inputSchema", inputSchema(t)))
	}
	return jsonx.Obj("tools", tools)
}

func callBuiltin(id any, inst *instance.Instance, name string, args map[string]any) map[string]any {
	switch name {
	case "catalog":
		if inst.Manifest == nil {
			return toolResult(id, "(no manifest.json)", false)
		}
		text := inst.Manifest.Catalog(jsonx.GetStringOr(args, "group", ""), jsonx.GetStringOr(args, "doc_type", ""))
		if text == "" {
			text = "(no matching entries)"
		}
		return toolResult(id, text, false)
	case "read_entry":
		eid, ok2 := jsonx.GetString(args, "id")
		if !ok2 {
			return toolResult(id, "read_entry needs an 'id' argument", true)
		}
		text, err := inst.ReadEntry(eid)
		if err != nil {
			return toolResult(id, err.Error(), true)
		}
		return toolResult(id, text, false)
	case "meter":
		return toolResult(id, meter.report(), false)
	}
	return toolResult(id, "unknown builtin: "+name, true)
}

func callTool(id any, inst *instance.Instance, disp *dispatch.Dispatcher, t config.Tool, args map[string]any) map[string]any {
	var text string
	var isErr bool
	switch t.Kind {
	case conventions.ToolKindValidate:
		table, ok2 := jsonx.GetString(args, "table")
		if !ok2 {
			text, isErr = "validate needs a 'table' argument", true
			break
		}
		vr := disp.Validate(table, jsonx.GetStringOr(args, "tsv", ""))
		text, isErr = vr.ToText(maxProblemsShown), !vr.Ok
	case conventions.ToolKindKbAnswer:
		q, ok2 := jsonx.GetString(args, "question")
		if !ok2 {
			q, ok2 = jsonx.GetString(args, "query")
		}
		if !ok2 {
			text, isErr = "kb_answer needs a 'question' argument", true
			break
		}
		text, isErr = disp.Ask(q), false
	case conventions.ToolKindPropose, conventions.ToolKindGenerateVerify:
		table, okT := jsonx.GetString(args, "table")
		request, okR := jsonx.GetString(args, "request")
		if !okR {
			request, okR = jsonx.GetString(args, "task")
		}
		if !okT || !okR {
			text, isErr = "propose needs 'table' and 'request' arguments", true
			break
		}
		pr := disp.ProposeRow(table, request)
		text, isErr = dispatch.FormatPropose(pr), !pr.Ok
	default:
		if t.HasExec() {
			rr := tool.Run(inst, t, args)
			if rr.Error != "" {
				text, isErr = rr.Error, true
			} else if rr.Output != "" {
				text, isErr = rr.Output, !rr.Ok
			} else {
				text, isErr = "(no output)", !rr.Ok
			}
		} else {
			text, isErr = "tool kind '"+t.Kind+"' is not implemented in the host", true
		}
	}
	return toolResult(id, text, isErr)
}

func ok(id, result any) map[string]any {
	return jsonx.Obj("jsonrpc", "2.0", "id", id, "result", result)
}

func errResp(id any, code int, message string) map[string]any {
	return jsonx.Obj("jsonrpc", "2.0", "id", id, "error", jsonx.Obj("code", code, "message", message))
}

func toolResult(id any, text string, isError bool) map[string]any {
	return ok(id, jsonx.Obj("content", []any{jsonx.Obj("type", "text", "text", text)}, "isError", isError))
}
