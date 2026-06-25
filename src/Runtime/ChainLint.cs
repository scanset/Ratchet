// ChainLint - the author-time validator for an action chain (the FlowLint analog for the new model,
// at a validate.py-equivalent level). Catches what would otherwise be a runtime failure
// with a loose model inside an unbounded graph:
//   - unknown/missing node kinds
//   - chain.json `nodes` vs the action.json files on disk
//   - every edge target (on_success/on_failure/transitions) is a declared node
//   - ai_branch `transitions` keys == `output_schema.next.enum`
//   - every `inputs[].from` is a reachable PREDECESSOR (BFS from entry)
//   - a referenced tool is declared; a ref/search binding names a library; a binding has an `as`
//   - generate/ai_branch prompt.md exists and fits a rough token budget
//   - every {{ slot }} a prompt names is a declared input binding (`as`) - else it renders empty (Flavor A)
//   - every {{ slot }} a search query names is bound ABOVE it in the same node's inputs (a search sees
//     only slots resolved earlier) - else it renders an empty query and retrieves nothing (Flavor B)
// The last two close the silent empty-slot seam: a binding contract that under-delivers now fails at lint.
// Pure + (mostly) static so SelfTest can cover it with an in-memory Chain.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text.RegularExpressions;

namespace Icm
{
    internal static class ChainLint
    {
        private const int CharsPerToken = 4;
        private const int PromptBodyLimit = 600;   // tokens, per context-budget heuristic

        public static List<string> Check(Chain c, List<string> toolNames)
        {
            var p = new List<string>();
            if (c == null) { p.Add("chain is null"); return p; }
            if (string.IsNullOrEmpty(c.Entry)) p.Add("chain has no 'entry'");

            var onDisk = new HashSet<string>(c.Actions.Keys);
            var declared = new HashSet<string>(c.NodeIds);
            foreach (string m in declared) if (!onDisk.Contains(m)) p.Add("declared node '" + m + "' has no action.json on disk");
            foreach (string x in onDisk) if (!declared.Contains(x)) p.Add("action '" + x + "' not declared in chain.json nodes");
            if (c.Entry.Length > 0 && !onDisk.Contains(c.Entry)) p.Add("entry '" + c.Entry + "' has no action.json");

            foreach (KeyValuePair<string, ActionNode> kv in c.Actions)
            {
                ActionNode a = kv.Value;
                string w = "node '" + a.Id + "'";
                if (a.Kind.Length == 0) { p.Add(w + ": missing 'kind'"); continue; }
                if (Array.IndexOf(Conventions.ActionKind.All, a.Kind) < 0) { p.Add(w + ": unknown kind '" + a.Kind + "'"); continue; }

                foreach (string tgt in a.Edges())
                    if (!onDisk.Contains(tgt)) p.Add(w + ": edge -> '" + tgt + "' is not a declared node");

                foreach (InputBinding ib in a.Inputs)
                {
                    if (string.IsNullOrEmpty(ib.As)) p.Add(w + ": an input binding has no 'as'");
                    if (ib.Source == "ref" && string.IsNullOrEmpty(ib.Lib)) p.Add(w + ": ref binding has no library");
                    if (ib.Source == "search" && string.IsNullOrEmpty(ib.Lib)) p.Add(w + ": search binding has no library");
                    if (ib.Source.Length == 0) p.Add(w + ": input '" + ib.As + "' has no source (from/ref/search)");
                }
                CheckSearchRefs(a, w, p);

                if (a.Kind == Conventions.ActionKind.Action)
                {
                    if (string.IsNullOrEmpty(a.Tool) && string.IsNullOrEmpty(a.Endpoint)) p.Add(w + ": action needs a 'tool' (or 'endpoint')");
                    else if (!string.IsNullOrEmpty(a.Tool) && toolNames != null && !toolNames.Contains(a.Tool)) p.Add(w + ": references unknown tool '" + a.Tool + "'");
                    if (string.IsNullOrEmpty(a.OnSuccess)) p.Add(w + ": action needs 'on_success'");
                }
                else if (a.Kind == Conventions.ActionKind.Generate)
                {
                    if (string.IsNullOrEmpty(a.Prompt) && string.IsNullOrEmpty(a.PromptText)) p.Add(w + ": generate needs 'prompt'");
                    if (string.IsNullOrEmpty(a.OnSuccess)) p.Add(w + ": generate needs 'on_success'");
                    CheckPrompt(a, w, p);
                }
                else if (a.Kind == Conventions.ActionKind.AiBranch)
                {
                    if (string.IsNullOrEmpty(a.Prompt) && string.IsNullOrEmpty(a.PromptText)) p.Add(w + ": ai_branch needs 'prompt'");
                    if (a.Transitions.Count < 2) p.Add(w + ": ai_branch needs at least 2 transitions");
                    var keys = new HashSet<string>(a.Transitions.Keys);
                    HashSet<string> enumVals = NextEnum(a.OutputSchema);
                    if (!SetEq(keys, enumVals)) p.Add(w + ": transitions keys {" + Join(keys) + "} != output_schema.next.enum {" + Join(enumVals) + "}");
                    CheckPrompt(a, w, p);
                }
                else if (a.Kind == Conventions.ActionKind.ForEach)
                {
                    if (string.IsNullOrEmpty(a.Flow)) p.Add(w + ": foreach needs 'flow' (the sub-chain to run per item)");
                    if (string.IsNullOrEmpty(a.Over)) p.Add(w + ": foreach needs 'over' (the slot holding the newline list)");
                    if (string.IsNullOrEmpty(a.OnSuccess)) p.Add(w + ": foreach needs 'on_success'");
                    if (string.IsNullOrEmpty(a.OnFailure)) p.Add(w + ": foreach needs 'on_failure'");
                }
                else if (a.Kind == Conventions.ActionKind.Exit)
                {
                    if (string.IsNullOrEmpty(a.Outcome)) p.Add(w + ": exit needs 'outcome'");
                }
            }

            // inputs[].from must be a reachable predecessor (BFS from entry)
            Dictionary<string, int> order = Bfs(c);
            foreach (KeyValuePair<string, ActionNode> kv in c.Actions)
            {
                ActionNode a = kv.Value;
                int co; bool haveCo = order.TryGetValue(a.Id, out co);
                foreach (InputBinding ib in a.Inputs)
                {
                    if (ib.Source != "from" || string.IsNullOrEmpty(ib.From)) continue;
                    // reserved run seeds ($input/$workspace) and chain-declared inputs are always available
                    if (ib.From == "$input" || ib.From == "$workspace" || c.Inputs.Contains(ib.From)) continue;
                    int so;
                    if (!order.TryGetValue(ib.From, out so)) p.Add("node '" + a.Id + "': inputs.from '" + ib.From + "' is not reachable from entry");
                    else if (haveCo && so >= co) p.Add("node '" + a.Id + "': inputs.from '" + ib.From + "' is not a predecessor");
                }
            }
            return p;
        }

        // Token budget + the slot-reference contract: every {{ slot }} the prompt names must be a declared
        // input binding, else Render substitutes "" and the model silently sees an empty intended slot.
        private static void CheckPrompt(ActionNode a, string w, List<string> p)
        {
            string body = a.PromptText;   // inline body (in-memory chains / tests) takes priority over the file
            if (body == null)
            {
                if (string.IsNullOrEmpty(a.Prompt) || string.IsNullOrEmpty(a.Dir)) return; // missing prompt already reported / no file to read
                string rel = a.Prompt.Replace("./", "").Replace(".\\", "");
                string path = Path.Combine(a.Dir, rel);
                if (!File.Exists(path)) { p.Add(w + ": prompt file '" + a.Prompt + "' not found"); return; }
                try { body = File.ReadAllText(path); }
                catch { return; }
            }
            int tokens = (body.Length + CharsPerToken - 1) / CharsPerToken;
            if (tokens > PromptBodyLimit) p.Add(w + ": prompt body " + tokens + " tokens > limit " + PromptBodyLimit);

            var bound = new HashSet<string>();
            foreach (InputBinding ib in a.Inputs) if (!string.IsNullOrEmpty(ib.As)) bound.Add(ib.As);
            foreach (Match m in ChainEngine.SlotRe.Matches(body))
            {
                string slot = m.Groups[1].Value;
                if (!bound.Contains(slot)) p.Add(w + ": prompt references {{ " + slot + " }} but no input binds it (add an input with as: \"" + slot + "\")");
            }
        }

        // A search binding's query is rendered over slots resolved SO FAR (ResolveSlots walks inputs[]
        // top-to-bottom). A {{ slot }} bound at or below the search renders empty -> the query is empty ->
        // retrieval silently returns nothing. So every slot a query names must be bound by an EARLIER input.
        private static void CheckSearchRefs(ActionNode a, string w, List<string> p)
        {
            var seen = new HashSet<string>();
            foreach (InputBinding ib in a.Inputs)
            {
                if (ib.Source == "search" && !string.IsNullOrEmpty(ib.Query))
                    foreach (Match m in ChainEngine.SlotRe.Matches(ib.Query))
                    {
                        string slot = m.Groups[1].Value;
                        if (!seen.Contains(slot)) p.Add(w + ": search '" + ib.As + "' query references {{ " + slot + " }} but no earlier input binds it (a search sees only slots resolved above it)");
                    }
                if (!string.IsNullOrEmpty(ib.As)) seen.Add(ib.As);
            }
        }

        private static Dictionary<string, int> Bfs(Chain c)
        {
            var order = new Dictionary<string, int>();
            if (string.IsNullOrEmpty(c.Entry) || !c.Actions.ContainsKey(c.Entry)) return order;
            var q = new Queue<string>();
            order[c.Entry] = 0; q.Enqueue(c.Entry);
            while (q.Count > 0)
            {
                string cur = q.Dequeue();
                ActionNode a; if (!c.Actions.TryGetValue(cur, out a)) continue;
                foreach (string n in a.Edges())
                    if (!string.IsNullOrEmpty(n) && !order.ContainsKey(n)) { order[n] = order[cur] + 1; q.Enqueue(n); }
            }
            return order;
        }

        private static HashSet<string> NextEnum(Dictionary<string, object> schema)
        {
            var s = new HashSet<string>();
            if (schema == null) return s;
            Dictionary<string, object> props = Json.GetObject(schema, "properties");
            Dictionary<string, object> next = props != null ? Json.GetObject(props, "next") : null;
            if (next != null) foreach (object e in Json.GetArr(next, "enum")) if (e != null) s.Add(e.ToString());
            return s;
        }

        private static bool SetEq(HashSet<string> a, HashSet<string> b)
        {
            if (a.Count != b.Count) return false;
            foreach (string x in a) if (!b.Contains(x)) return false;
            return true;
        }

        private static string Join(HashSet<string> s)
        {
            var l = new List<string>(s); l.Sort(StringComparer.OrdinalIgnoreCase);
            return string.Join(",", l.ToArray());
        }
    }
}
