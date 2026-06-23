// ChainEngine - runs an action chain (filesystem-as-graph). The loop reads one action.json
// per step, resolves its declared inputs[] into slots (from a prior node, a fixed ref, or a search
// injection), runs the node, and follows the edge - under the chain's budgets, writing run state to
// runs/<id>/. AI proposes (a decision edge, or generated text); the host executes. A node sees ONLY
// its declared inputs (no cumulative tape).
//
// Node kinds: action (a tools/ script + validators), generate (free text via the generate seat),
// ai_branch (enum decision -> transition), summarizer (deterministic merge), exit (outcome).

using System;
using System.Collections.Generic;
using System.Text;
using System.Text.RegularExpressions;

namespace Icm
{
    internal class ChainResult
    {
        public string Outcome = "";
        public string Text = "";
        public int Steps;
        public bool IsError;
    }

    internal class ChainEngine
    {
        private const int HardStepCap = 100;       // backstop when a chain declares no max_steps
        private const int DecideTimeoutMs = 60000;

        private static readonly Regex SlotRe = new Regex(@"\{\{\s*([A-Za-z0-9_\-]+)\s*\}\}", RegexOptions.Compiled);

        private readonly Instance icm;
        private readonly Dispatcher disp;
        private readonly Action<string> status;

        public ChainEngine(Instance icm, Dispatcher disp, Action<string> status)
        {
            this.icm = icm;
            this.disp = disp;
            this.status = status != null ? status : delegate(string s) { };
        }

        public ChainResult Run(Chain c, string input, string workspace)
        {
            var res = new ChainResult();
            var state = new Dictionary<string, string>();   // node id -> output text
            state["$input"] = input != null ? input : "";
            state["$workspace"] = workspace != null ? workspace : "";   // the active workspace (proj for project chains)
            SplitInputs(c.Inputs, state["$input"], state);              // chain-declared named slots (head/tail)

            string runId = DateTime.Now.ToString("yyyyMMdd-HHmmss-fff");
            WriteState(runId, "meta.json", Json.Obj("chain", c.Id, "input", state["$input"], "started", DateTime.Now.ToString("s")));

            int maxSteps = c.MaxSteps > 0 ? c.MaxSteps : HardStepCap;
            long tok0 = TokenMeter.Total;
            string lastOutput = "";
            string step = c.Entry;
            int n = 0;

            while (!string.IsNullOrEmpty(step))
            {
                if (n >= maxSteps) { res.IsError = true; res.Outcome = "aborted: max_steps (" + maxSteps + ")"; break; }
                if (c.MaxTokens > 0 && (TokenMeter.Total - tok0) > c.MaxTokens) { res.IsError = true; res.Outcome = "aborted: max_tokens"; break; }

                ActionNode a;
                if (!c.Actions.TryGetValue(step, out a)) { res.IsError = true; res.Outcome = "aborted: missing node '" + step + "'"; break; }
                n++;
                status("step " + n + ": " + a.Id + " (" + a.Kind + ")");

                if (a.Kind == Conventions.ActionKind.Exit)
                {
                    res.Outcome = !string.IsNullOrEmpty(a.Outcome) ? a.Outcome : "success";
                    WriteState(runId, "step-" + n.ToString("000") + ".json", Json.Obj("node", a.Id, "kind", a.Kind, "outcome", res.Outcome));
                    break;
                }
                else if (a.Kind == Conventions.ActionKind.Generate)
                {
                    Dictionary<string, string> slots = ResolveSlots(a, state);
                    string outp;
                    try
                    {
                        string gp = Render(ReadPrompt(a), slots);
                        if (a.OutputSchema != null)
                        {
                            // Structured generate: force the declared schema and store the raw JSON so
                            // later bindings can pull individual fields via their `path` (JSON pointer).
                            Dictionary<string, object> jv = Ollama.GenerateJson(disp.Url, icm.Config.Models.Generate, gp, a.OutputSchema, 0.2, DecideTimeoutMs, new Cancel());
                            outp = Json.Serialize(jv);
                        }
                        else { outp = disp.Generate(gp, 0.2); }
                    }
                    catch (IcmError e) { res.IsError = true; res.Outcome = "aborted: " + e.Message; break; }
                    state[a.Id] = outp; lastOutput = outp;
                    WriteState(runId, "step-" + n.ToString("000") + ".json", Json.Obj("node", a.Id, "kind", a.Kind, "output", Cap(outp, 4000)));
                    step = a.OnSuccess;
                }
                else if (a.Kind == Conventions.ActionKind.AiBranch)
                {
                    Dictionary<string, string> slots = ResolveSlots(a, state);
                    string next;
                    try { next = Decide(a, Render(ReadPrompt(a), slots)); }
                    catch (IcmError e) { res.IsError = true; res.Outcome = "aborted: " + e.Message; break; }
                    state[a.Id] = next;
                    WriteState(runId, "step-" + n.ToString("000") + ".json", Json.Obj("node", a.Id, "kind", a.Kind, "next", next));
                    string tgt;
                    if (!a.Transitions.TryGetValue(next, out tgt)) { res.IsError = true; res.Outcome = "aborted: '" + a.Id + "' returned unroutable '" + next + "'"; break; }
                    step = tgt;
                }
                else if (a.Kind == Conventions.ActionKind.Action)
                {
                    Dictionary<string, string> slots = ResolveSlots(a, state);
                    bool ok; string output;
                    RunActionNode(a, slots, out ok, out output);
                    state[a.Id] = output; lastOutput = output;
                    WriteState(runId, "step-" + n.ToString("000") + ".json", Json.Obj("node", a.Id, "kind", a.Kind, "ok", ok, "output", Cap(output, 4000)));
                    step = ok ? a.OnSuccess : a.OnFailure;
                }
                else if (a.Kind == Conventions.ActionKind.Summarizer)
                {
                    Dictionary<string, string> slots = ResolveSlots(a, state);
                    var sb = new StringBuilder();
                    foreach (KeyValuePair<string, string> kv in slots) { sb.Append(kv.Key); sb.Append(": "); sb.Append(kv.Value); sb.Append("\n"); }
                    state[a.Id] = sb.ToString().TrimEnd();
                    WriteState(runId, "step-" + n.ToString("000") + ".json", Json.Obj("node", a.Id, "kind", a.Kind, "output", Cap(state[a.Id], 4000)));
                    step = a.OnSuccess;
                }
                else { res.IsError = true; res.Outcome = "aborted: unknown kind '" + a.Kind + "'"; break; }
            }

            res.Steps = n;
            if (string.IsNullOrEmpty(res.Outcome)) res.Outcome = string.IsNullOrEmpty(step) ? "ended (no exit)" : res.Outcome;
            res.Text = lastOutput.Length > 0 ? lastOutput : ("[chain " + c.Id + " -> " + res.Outcome + ", " + n + " step(s)]");
            WriteState(runId, "outcome.json", Json.Obj("outcome", res.Outcome, "steps", res.Steps, "error", res.IsError));
            return res;
        }

        // --- slot resolution (declared-inputs-only context) ---

        private Dictionary<string, string> ResolveSlots(ActionNode a, Dictionary<string, string> state)
        {
            var slots = new Dictionary<string, string>();
            foreach (InputBinding ib in a.Inputs)
            {
                if (string.IsNullOrEmpty(ib.As)) continue;
                string val = "";
                if (ib.Source == "from") { string v; val = ApplyPath(state.TryGetValue(ib.From, out v) ? v : "", ib.Path); }
                else if (ib.Source == "ref") val = ResolveRef(ib);
                else if (ib.Source == "search") val = ResolveSearch(ib, slots);   // may reference earlier slots
                if (ib.MaxChars > 0 && val.Length > ib.MaxChars) val = val.Substring(0, ib.MaxChars);
                slots[ib.As] = val;
            }
            return slots;
        }

        // Pull a field out of a prior node's output when the binding declares a `path` other than ".".
        // The path is a JSON pointer (a bare field name like "cppref_q" is treated as "/cppref_q") into a
        // structured (output_schema) generate result. "." or empty returns the whole value; non-JSON or a
        // missing field yields "". This is what lets a plan node route per-field into different searches.
        private static string ApplyPath(string raw, string path)
        {
            if (string.IsNullOrEmpty(path) || path == ".") return raw;
            object root;
            try { root = Json.Parse(raw); } catch { return ""; }
            string ptr = path.StartsWith("/") ? path : "/" + path;
            object node = Json.Pointer(root, ptr);
            if (node == null) return "";
            string s = node as string;
            return s != null ? s : Json.Serialize(node);
        }

        private string ResolveRef(InputBinding ib)
        {
            string dir = LibDir(ib.Lib);
            if (dir == null) return "";
            string rel = ib.RefPath;
            if (string.IsNullOrEmpty(rel) && !string.IsNullOrEmpty(ib.Id))
            {
                Dictionary<string, Entry> man = Indexer.LoadManifestMap(dir);
                foreach (Entry e in man.Values) if (string.Equals(e.Id, ib.Id, StringComparison.OrdinalIgnoreCase)) { rel = e.Path; break; }
            }
            return ReadDocOrEmpty(dir, rel);
        }

        private string ResolveSearch(InputBinding ib, Dictionary<string, string> slots)
        {
            string dir = LibDir(ib.Lib);
            if (dir == null) return "";
            string q = Render(ib.Query, slots);
            if (q.Trim().Length == 0) return "";
            KnowledgeBase kb = icm.Knowledge().Find(ib.Lib);
            string key = kb != null ? kb.Name : null;
            List<string> ranked = KbIndex.Rank(icm, key, dir, q, ib.K > 0 ? ib.K : 3);
            var sb = new StringBuilder();
            foreach (string rel in ranked)
            {
                string doc = ReadDocOrEmpty(dir, rel);
                if (doc.Length > 0) { sb.Append(doc); sb.Append("\n\n"); }
            }
            return sb.ToString().TrimEnd();
        }

        private string LibDir(string lib)
        {
            if (string.IsNullOrEmpty(lib)) return null;
            KnowledgeBase kb = icm.Knowledge().Find(lib);
            if (kb != null) return kb.Path;
            try { string p = icm.Resolve(lib); return System.IO.Directory.Exists(p) ? p : null; }
            catch (IcmError) { return null; }
        }

        private static string ReadDocOrEmpty(string dir, string rel)
        {
            if (string.IsNullOrEmpty(rel)) return "";
            try { return Indexer.StripMeta(System.IO.File.ReadAllText(System.IO.Path.Combine(dir, rel.Replace('/', System.IO.Path.DirectorySeparatorChar)))); }
            catch { return ""; }
        }

        // --- node execution ---

        private string Decide(ActionNode a, string prompt)
        {
            object schema = a.OutputSchema != null ? (object)a.OutputSchema : Json.Schema(Json.Obj("next", Json.StrProp()), "next");
            Dictionary<string, object> v = Ollama.GenerateJson(disp.Url, icm.Config.DispatchModel(), prompt, schema, 0.1, DecideTimeoutMs, new Cancel());
            return Json.GetStringOr(v, "next", "");
        }

        private void RunActionNode(ActionNode a, Dictionary<string, string> slots, out bool ok, out string output)
        {
            Tool t = icm.FindTool(a.Tool);
            if (t == null) { ok = false; output = "[no such tool: " + a.Tool + "]"; return; }
            var args = new Dictionary<string, object>();
            if (a.Body != null) foreach (KeyValuePair<string, object> kv in a.Body) args[kv.Key] = Render(kv.Value != null ? kv.Value.ToString() : "", slots);
            ToolRunResult rr = ToolRunner.Run(icm, t, args);
            output = rr.Error != null ? rr.Error : rr.Output;
            ok = rr.Ok && rr.Error == null && Validate(a, output);
        }

        private static bool Validate(ActionNode a, string output)
        {
            foreach (Validator v in a.Validate)
            {
                bool pass;
                switch (v.Predicate)
                {
                    case "is_non_empty": pass = output != null && output.Trim().Length > 0; break;
                    case "is_empty": pass = output == null || output.Trim().Length == 0; break;
                    case "exists": pass = output != null; break;
                    case "is_array": pass = AsParsed(output) is object[] || AsParsed(output) is List<object>; break;
                    case "is_object": pass = Json.AsObject(AsParsed(output)) != null; break;
                    default: pass = true; break;
                }
                if (!pass) return false;
            }
            return true;
        }

        private static object AsParsed(string s)
        {
            try { return Json.Parse(s); } catch { return null; }
        }

        // --- helpers ---

        private string ReadPrompt(ActionNode a)
        {
            if (string.IsNullOrEmpty(a.Prompt) || string.IsNullOrEmpty(a.Dir)) return "";
            string rel = a.Prompt.Replace("./", "").Replace(".\\", "");
            try { return System.IO.File.ReadAllText(System.IO.Path.Combine(a.Dir, rel)); }
            catch { return ""; }
        }

        private static string Render(string template, Dictionary<string, string> slots)
        {
            if (string.IsNullOrEmpty(template)) return "";
            return SlotRe.Replace(template, delegate(Match m)
            {
                string v; return slots.TryGetValue(m.Groups[1].Value, out v) ? v : "";
            });
        }

        private static string Cap(string s, int max)
        {
            if (s == null) return "";
            return s.Length <= max ? s : s.Substring(0, max) + " ...";
        }

        // Split $input into the chain's declared named slots: each leading name takes one whitespace
        // token; the LAST name captures the remainder. Generic head/tail mapping.
        private static void SplitInputs(List<string> names, string input, Dictionary<string, string> state)
        {
            if (names == null || names.Count == 0) return;
            string remaining = (input != null ? input : "").Trim();
            int lead = names.Count - 1;
            for (int i = 0; i < lead; i++)
            {
                int sp = -1;
                for (int j = 0; j < remaining.Length; j++) if (char.IsWhiteSpace(remaining[j])) { sp = j; break; }
                if (sp < 0) { state[names[i]] = remaining; remaining = ""; }
                else { state[names[i]] = remaining.Substring(0, sp); remaining = remaining.Substring(sp).TrimStart(); }
            }
            state[names[names.Count - 1]] = remaining;
        }

        private void WriteState(string runId, string file, Dictionary<string, object> obj)
        {
            try { icm.WriteFile(Conventions.RunsDir + "/" + runId + "/" + file, Json.SerializePretty(obj)); }
            catch (IcmError) { }
        }
    }
}
