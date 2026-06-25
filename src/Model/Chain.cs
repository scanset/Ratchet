// Chain / ActionNode - the data model for an action-chain flow (filesystem-as-graph):
// flows/<chain>/chain.json (the graph) + actions/<a>/{action.json, prompt.md} (the nodes). This is
// the loader + types; the run engine is F3 (Runtime). Added ALONGSIDE the legacy Flow model so the
// existing node-array flows keep working during migration.

using System;
using System.Collections.Generic;
using System.IO;

namespace Icm
{
    // A response predicate on an `action` node (the deterministic side-effect's success check).
    internal class Validator
    {
        public string Path = "";
        public string Predicate = "";
        public static Validator Parse(Dictionary<string, object> o)
        {
            var v = new Validator();
            v.Path = Json.GetStringOr(o, "path", "");
            v.Predicate = Json.GetStringOr(o, "predicate", "");
            return v;
        }
    }

    // One slot bound into a node's prompt/body. Source is exactly one of from / ref / search:
    //   from   - a prior node's output (jq path)            { from, path, as }
    //   ref    - a fixed knowledge entry                    { ref: <lib>, id|path, as, max_chars? }
    //   search - a templated query against a library        { search: <lib>, query, k, as, max_chars? }
    internal class InputBinding
    {
        public string As = "";
        public string Source = "";   // "from" | "ref" | "search"
        public string From;          // from: prior-node id
        public string Path;          // from: jq path
        public string Lib;           // ref/search: knowledge library name
        public string Id;            // ref: entry id
        public string RefPath;       // ref: entry path
        public string Query;         // search: templated query
        public int K = 3;            // search: top-k
        public int MaxChars = 0;     // injected-slot cap (0 = none)

        public static InputBinding Parse(Dictionary<string, object> o)
        {
            var b = new InputBinding();
            b.As = Json.GetStringOr(o, "as", "");
            double? mc = Json.GetNumber(o, "max_chars"); if (mc.HasValue) b.MaxChars = (int)mc.Value;
            string f = Json.GetString(o, "from");
            string r = Json.GetString(o, "ref");
            string s = Json.GetString(o, "search");
            if (f != null) { b.Source = "from"; b.From = f; b.Path = Json.GetStringOr(o, "path", "."); }
            else if (r != null) { b.Source = "ref"; b.Lib = r; b.Id = Json.GetString(o, "id"); b.RefPath = Json.GetString(o, "path"); }
            else if (s != null) { b.Source = "search"; b.Lib = s; b.Query = Json.GetStringOr(o, "query", ""); double? k = Json.GetNumber(o, "k"); if (k.HasValue) b.K = (int)k.Value; }
            return b;
        }
    }

    internal class ActionNode
    {
        public string Id = "";
        public string Kind = "";   // action | ai_branch | summarizer | exit
        public string Dir = "";    // the action's folder (abs); resolves prompt.md
        public List<InputBinding> Inputs = new List<InputBinding>();
        // action
        public string Tool;        // ICM: a tools/ script (the deterministic side-effect)
        public string Endpoint;    // alt/compat: a "VERB /path" string (RefArch HTTP form)
        public Dictionary<string, object> Body;
        public List<Validator> Validate = new List<Validator>();
        public string OnSuccess;
        public string OnFailure;
        // ai_branch
        public string Prompt;      // ./prompt.md (read from Dir at runtime)
        public string PromptText;  // inline prompt body; when set it is used instead of reading Prompt (in-memory chains / tests)
        public Dictionary<string, object> OutputSchema;
        public Dictionary<string, string> Transitions = new Dictionary<string, string>();
        // exit
        public string Outcome;
        // foreach: run sub-chain `Flow` once per newline item in slot `Over`, input = `ItemInput` ({{ item }})
        public string Over;
        public string Flow;
        public string ItemInput;
        public Dictionary<string, object> Extra = new Dictionary<string, object>();

        public static ActionNode Parse(Dictionary<string, object> o)
        {
            var a = new ActionNode();
            a.Id = Json.GetStringOr(o, "id", "");
            a.Kind = Json.GetStringOr(o, "kind", "");
            foreach (object i in Json.GetArr(o, "inputs")) { var io = i as Dictionary<string, object>; if (io != null) a.Inputs.Add(InputBinding.Parse(io)); }
            a.Tool = Json.GetString(o, "tool");
            a.Endpoint = Json.GetString(o, "endpoint");
            a.Body = Json.GetObject(o, "body");
            foreach (object v in Json.GetArr(o, "validate")) { var vo = v as Dictionary<string, object>; if (vo != null) a.Validate.Add(Validator.Parse(vo)); }
            a.OnSuccess = Json.GetString(o, "on_success");
            a.OnFailure = Json.GetString(o, "on_failure");
            a.Prompt = Json.GetString(o, "prompt");
            a.Over = Json.GetString(o, "over");
            a.Flow = Json.GetString(o, "flow");
            a.ItemInput = Json.GetString(o, "input");
            a.OutputSchema = Json.GetObject(o, "output_schema");
            Dictionary<string, object> tr = Json.GetObject(o, "transitions");
            if (tr != null) foreach (var kv in tr) if (kv.Value != null) a.Transitions[kv.Key] = kv.Value.ToString();
            a.Outcome = Json.GetString(o, "outcome");
            foreach (var kv in o) a.Extra[kv.Key] = kv.Value;   // keep extras (summarizer from/produce, model, ...)
            return a;
        }

        public static ActionNode Load(string path)
        {
            string text;
            try { text = File.ReadAllText(path); }
            catch (Exception e) { throw new IcmError("reading action " + path + ": " + e.Message); }
            Dictionary<string, object> o = Json.AsObject(Json.Parse(text));
            if (o == null) throw new IcmError("parsing action " + path + ": not a JSON object");
            ActionNode a = Parse(o);
            a.Dir = Path.GetDirectoryName(Path.GetFullPath(path));
            return a;
        }

        // The next-node ids this node can transition to.
        public List<string> Edges()
        {
            var e = new List<string>();
            if (Kind == Conventions.ActionKind.AiBranch) { foreach (var kv in Transitions) e.Add(kv.Value); }
            else { if (!string.IsNullOrEmpty(OnSuccess)) e.Add(OnSuccess); if (!string.IsNullOrEmpty(OnFailure)) e.Add(OnFailure); }
            return e;
        }
    }

    internal class Chain
    {
        public string Id = "";
        public string Version = "";
        public string Entry = "";
        public string Summary = "";   // routing text for /route (the dispatch tier's match surface)
        public List<string> Inputs = new List<string>(); // named slots $input is split into (head/tail); seeded as run state
        public int MaxSteps = 0;
        public int MaxTokens = 0;
        public double MaxWallclock = 0;
        public List<string> NodeIds = new List<string>();
        public Dictionary<string, ActionNode> Actions = new Dictionary<string, ActionNode>();
        public string Dir = "";

        public static Chain Load(string chainDir)
        {
            var c = new Chain();
            c.Dir = Path.GetFullPath(chainDir);
            string cj = Path.Combine(c.Dir, "chain.json");
            string text;
            try { text = File.ReadAllText(cj); }
            catch (Exception e) { throw new IcmError("reading chain " + cj + ": " + e.Message); }
            Dictionary<string, object> o = Json.AsObject(Json.Parse(text));
            if (o == null) throw new IcmError("parsing chain " + cj + ": not a JSON object");

            c.Id = Json.GetStringOr(o, "id", Path.GetFileName(c.Dir.TrimEnd('\\', '/')));
            c.Version = Json.GetStringOr(o, "version", "");
            c.Entry = Json.GetStringOr(o, "entry", "");
            c.Summary = Json.GetStringOr(o, "summary", Json.GetStringOr(o, "whenToUse", ""));
            foreach (object i in Json.GetArr(o, "inputs")) if (i != null) c.Inputs.Add(i.ToString());
            Dictionary<string, object> b = Json.GetObject(o, "budgets");
            if (b != null)
            {
                double? ms = Json.GetNumber(b, "max_steps"); if (ms.HasValue) c.MaxSteps = (int)ms.Value;
                double? mt = Json.GetNumber(b, "max_total_tokens"); if (mt.HasValue) c.MaxTokens = (int)mt.Value;
                double? mw = Json.GetNumber(b, "max_wallclock_seconds"); if (mw.HasValue) c.MaxWallclock = mw.Value;
            }
            foreach (object n in Json.GetArr(o, "nodes")) if (n != null) c.NodeIds.Add(n.ToString());

            string adir = Path.Combine(c.Dir, "actions");
            if (Directory.Exists(adir))
            {
                string[] files = Directory.GetFiles(adir, "action.json", SearchOption.AllDirectories);
                Array.Sort(files, StringComparer.OrdinalIgnoreCase);
                foreach (string af in files)
                {
                    try { ActionNode a = ActionNode.Load(af); if (a.Id.Length > 0) c.Actions[a.Id] = a; }
                    catch (IcmError) { }
                }
            }
            return c;
        }

        // Is `dir` an action-chain (has chain.json)? Distinguishes a chain dir from a legacy flow file.
        public static bool IsChainDir(string dir)
        {
            return Directory.Exists(dir) && File.Exists(Path.Combine(dir, "chain.json"));
        }
    }
}
