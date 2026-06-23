// icm - the ICM host console CLI.
//
//   icm open  <dir>                 load + summarize an ICM instance
//   icm chat  <dir>                 operator console (dispatcher; needs Ollama)
//   icm mcp   <dir>                 serve this ICM over MCP (stdio)
//   icm flow  <dir> <name> [in...]  run an authored workflow (flows/<name>.json)
//   icm validate <dir> <table>      run the oracle on schemas/<table>.json + samples/<table>.txt
//   icm docsearch <dir> <corpus> <query...>   hybrid search a built refdocs corpus
//   icm reindex <dir>               regenerate manifest.json from files' <!--icm--> metadata blocks
//   icm gen   <dir> <prompt...>     one raw generate call (smoke-test the model seat)
//   icm selftest                    check the deterministic core (no model needed)
//
// OLLAMA_URL overrides the config's ollama_url.

using System;
using System.Collections.Generic;

namespace Icm
{
    internal static class Program
    {
        private const int GenTimeoutMs = 300000;
        private const int MaxProblemsShown = 40;

        private static string EffectiveUrl(Instance icm)
        {
            string env = Environment.GetEnvironmentVariable("OLLAMA_URL");
            return string.IsNullOrEmpty(env) ? icm.Config.OllamaUrl : env;
        }

        private static void CmdOpen(string dir)
        {
            Instance icm = Instance.Open(dir);
            Config c = icm.Config;
            Console.WriteLine("ICM '" + c.Name + "'  (" + c.Domain + ")");
            Console.WriteLine("  root      : " + icm.Root);
            string embed = string.IsNullOrEmpty(c.Models.Embed) ? "(none)" : c.Models.Embed;
            Console.WriteLine("  models    : generate=" + c.Models.Generate + " dispatch=" + c.DispatchModel() + " embed=" + embed);
            Console.WriteLine("  ollama    : " + EffectiveUrl(icm));
            if (icm.Manifest != null)
            {
                Console.WriteLine("  kb entries: " + icm.Manifest.Entries.Count);
                foreach (Entry e in icm.Manifest.Entries)
                {
                    string g = e.Group.Length > 0 ? "[" + e.Group + "] " : "";
                    Console.WriteLine("    - " + e.Id.PadRight(22) + " " + g + e.Title);
                }
            }
            else Console.WriteLine("  kb entries: (no manifest.json)");
            var names = new List<string>();
            foreach (Tool t in icm.Tools()) names.Add(t.Name);
            Console.WriteLine("  tools     : " + string.Join(", ", names.ToArray()));
        }

        private static void CmdList(string dir, string group, string type, bool asJson)
        {
            Instance icm = Instance.Open(dir);
            if (icm.Manifest == null) { Console.Error.WriteLine("no manifest.json in " + icm.Root); return; }
            var entries = new List<Entry>();
            foreach (Entry e in icm.Manifest.Entries)
            {
                if (group != null && !string.Equals(e.Group, group, StringComparison.OrdinalIgnoreCase)) continue;
                if (type != null && !string.Equals(e.DocType, type, StringComparison.OrdinalIgnoreCase)) continue;
                entries.Add(e);
            }
            if (asJson)
            {
                var arr = new List<object>();
                foreach (Entry e in entries)
                    arr.Add(Json.Obj("id", e.Id, "title", e.Title, "group", e.Group, "doc_type", e.DocType, "path", e.Path, "summary", e.Summary, "keywords", e.Keywords.ToArray()));
                Console.WriteLine(Json.SerializePretty(arr.ToArray()));
                return;
            }
            entries.Sort(delegate(Entry a, Entry b)
            {
                int g = string.Compare(a.Group, b.Group, StringComparison.OrdinalIgnoreCase);
                return g != 0 ? g : string.Compare(a.Id, b.Id, StringComparison.OrdinalIgnoreCase);
            });
            string cur = " ";
            foreach (Entry e in entries)
            {
                string g = e.Group.Length > 0 ? e.Group : "(top level)";
                if (g != cur) { Console.WriteLine((cur == " " ? "" : "\n") + "[" + g + "]"); cur = g; }
                Console.WriteLine("  " + e.Id.PadRight(24) + " " + e.Summary);
            }
            Console.WriteLine("\n" + entries.Count + " entr" + (entries.Count == 1 ? "y" : "ies"));
        }

        private static void CmdValidateFlow(string dir, string name)
        {
            Instance icm = Instance.Open(dir);
            var tools = new List<string>();
            foreach (Tool t in icm.Tools()) tools.Add(t.Name);

            int problemsTotal = 0;

            // Lint the named chain, or every flows/<chain>/ that has a chain.json.
            string cdir = icm.FlowsDirAbs();
            var chainDirs = new List<string>();
            if (name != null)
            {
                string one = System.IO.Path.Combine(cdir, name);
                if (!Chain.IsChainDir(one)) { Console.WriteLine("FAIL  " + name + ": no chain.json at flows/" + name + "/"); Environment.Exit(2); }
                chainDirs.Add(one);
            }
            else if (System.IO.Directory.Exists(cdir))
            {
                string[] subs = System.IO.Directory.GetDirectories(cdir);
                System.Array.Sort(subs, StringComparer.OrdinalIgnoreCase);
                foreach (string sub in subs) if (Chain.IsChainDir(sub)) chainDirs.Add(sub);
            }

            var chainNames = new List<string>();
            foreach (string sub in chainDirs)
            {
                string cid = System.IO.Path.GetFileName(sub);
                chainNames.Add(cid);
                Chain chain;
                try { chain = Chain.Load(sub); }
                catch (IcmError e) { Console.WriteLine("FAIL  " + cid + " (chain): " + e.Message); problemsTotal++; continue; }
                List<string> cp = ChainLint.Check(chain, tools);
                if (cp.Count == 0) Console.WriteLine("ok    " + cid + " (chain, " + chain.Actions.Count + " node(s))");
                else
                {
                    Console.WriteLine("FAIL  " + cid + " (chain):");
                    foreach (string p in cp) Console.WriteLine("        " + p);
                    problemsTotal += cp.Count;
                }
            }

            // Namespace check: chain + tool names must be globally unique (flat routing by name).
            foreach (string p in Oracle.NamespaceProblems(chainNames, tools))
            { Console.WriteLine("FAIL  namespace: " + p); problemsTotal++; }

            if (problemsTotal > 0) Environment.Exit(2);
        }

        private static void CmdValidate(string dir, string table)
        {
            Instance icm = Instance.Open(dir);
            TableSchema schema = TableSchema.Load(icm.SchemaPath(table));
            string data = icm.ReadAt(icm.SamplePath(table));
            var vr = new ValidateResult();
            vr.Table = table;
            vr.Problems = Oracle.ValidateTsv(schema, data, Oracle.BuildRefs(icm));
            vr.Ok = vr.Problems.Count == 0;
            if (vr.Ok) Console.WriteLine(vr.ToText(MaxProblemsShown));
            else { Console.Error.WriteLine(vr.ToText(MaxProblemsShown)); Environment.Exit(2); }
        }

        private static void CmdGen(string dir, string prompt)
        {
            Instance icm = Instance.Open(dir);
            string outText = Ollama.Generate(EffectiveUrl(icm), icm.Config.Models.Generate, prompt, null, 0.3, GenTimeoutMs);
            Console.WriteLine(outText);
        }

        private static void CmdFlow(string dir, string name, string input, string ws)
        {
            Instance icm = Instance.Open(dir);
            var status = (Action<string>)delegate(string s) { Console.Error.WriteLine("  - " + s); };
            var disp = new Dispatcher(icm, EffectiveUrl(icm), status);
            string chainDir = System.IO.Path.Combine(icm.FlowsDirAbs(), name);
            if (!Chain.IsChainDir(chainDir)) throw new IcmError("no flow '" + name + "' (expected flows/" + name + "/chain.json)");
            Chain c = Chain.Load(chainDir);
            // --ws <name> sets the active workspace (the abs path under workspaces/), matching what the
            // console's /ws switch does, so workspace-bound chains ($workspace) run non-interactively.
            string workspace = null;
            if (!string.IsNullOrEmpty(ws))
            {
                workspace = System.IO.Path.Combine(icm.WorkspacesDirAbs(), ws);
                if (!System.IO.Directory.Exists(workspace)) throw new IcmError("no workspace '" + ws + "' (under workspaces/)");
            }
            ChainResult cr = new ChainEngine(icm, disp, status).Run(c, input, workspace);
            if (!string.IsNullOrEmpty(cr.Text)) Console.WriteLine(cr.Text);
            if (cr.IsError) Environment.Exit(2);
        }

        private static string Arg(string[] args, int i) { return (i < args.Length) ? args[i] : null; }

        // The set of recognized verbs; anything else that names a directory is the `icm <dir>` shorthand.
        private static bool IsCommand(string s)
        {
            switch (s)
            {
                case "open": case "chat": case "mcp": case "flow": case "validate":
                case "reindex": case "index": case "list": case "flows": case "validate-flow": case "doctor": case "gen": case "selftest":
                case "help": case "-h": case "--help": return true;
                default: return false;
            }
        }

        private static void Run(string[] args)
        {
            string cmd = args.Length > 0 ? args[0] : "";

            // VSCode-style shorthand: `icm <dir>` or `icm <config.json>` opens the operator console.
            // The path is relative to the terminal's working directory, or absolute. Commands win.
            if (cmd.Length > 0 && !IsCommand(cmd) && (System.IO.Directory.Exists(cmd) || System.IO.File.Exists(cmd)))
            {
                Instance icm = Instance.Open(cmd);
                ConsoleChat.Run(icm, EffectiveUrl(icm));
                return;
            }

            switch (cmd)
            {
                case "open":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm open <dir>");
                    CmdOpen(dir);
                    break;
                }
                case "validate":
                {
                    string dir = Arg(args, 1), table = Arg(args, 2);
                    if (dir == null || table == null) throw new IcmError("usage: icm validate <dir> <table>");
                    CmdValidate(dir, table);
                    break;
                }
                case "validate-flow":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm validate-flow <dir> [name]");
                    CmdValidateFlow(dir, Arg(args, 2));   // name optional: omit to lint every flow
                    break;
                }
                case "doctor":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: ratchet doctor <dir>");
                    Instance icm = Instance.Open(dir);
                    int code = Doctor.Run(icm, EffectiveUrl(icm));
                    if (code != 0) Environment.Exit(code);
                    break;
                }
                case "gen":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm gen <dir> <prompt...>");
                    string prompt = (args.Length > 2) ? string.Join(" ", args, 2, args.Length - 2) : "";
                    if (prompt.Length == 0) throw new IcmError("usage: icm gen <dir> <prompt...>");
                    CmdGen(dir, prompt);
                    break;
                }
                case "chat":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm chat <dir>");
                    Instance icm = Instance.Open(dir);
                    ConsoleChat.Run(icm, EffectiveUrl(icm));
                    break;
                }
                case "mcp":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm mcp <dir>");
                    Instance icm = Instance.Open(dir);
                    Mcp.Serve(icm, EffectiveUrl(icm));
                    break;
                }
                case "flow":
                {
                    string dir = Arg(args, 1), name = Arg(args, 2);
                    if (dir == null || name == null) throw new IcmError("usage: icm flow <dir> <name> [--ws <workspace>] [input...]");
                    // Pull an optional "--ws <workspace>" out of the trailing args; the rest is the input.
                    string ws = null;
                    var rest = new System.Collections.Generic.List<string>();
                    for (int i = 3; i < args.Length; i++)
                    {
                        if (args[i] == "--ws" && i + 1 < args.Length) { ws = args[i + 1]; i++; }
                        else rest.Add(args[i]);
                    }
                    string input = string.Join(" ", rest.ToArray());
                    CmdFlow(dir, name, input, ws);
                    break;
                }
                case "reindex":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm reindex <dir>");
                    Instance icm = Instance.Open(dir);
                    Indexer.Reindex(icm, delegate(string s) { Console.Error.WriteLine("  - " + s); });
                    break;
                }
                case "index":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm index <kb-dir>   (builds manifest.json from file content)");
                    string abs = System.IO.Path.GetFullPath(dir);
                    if (!System.IO.Directory.Exists(abs)) throw new IcmError("not a directory: " + abs);
                    Indexer.WriteKbManifest(abs, delegate(string s) { Console.Error.WriteLine("  - " + s); });
                    break;
                }
                case "flows":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm flows <dir>");
                    Instance icmF = Instance.Open(dir);
                    string fdir = icmF.FlowsDirAbs();
                    Console.WriteLine("action chains:");
                    if (System.IO.Directory.Exists(fdir))
                    {
                        string[] subs = System.IO.Directory.GetDirectories(fdir); System.Array.Sort(subs, StringComparer.OrdinalIgnoreCase);
                        foreach (string sub in subs)
                        {
                            if (!Chain.IsChainDir(sub)) continue;
                            try { Chain c = Chain.Load(sub); Console.WriteLine("  " + System.IO.Path.GetFileName(sub).PadRight(18) + " " + c.Summary); }
                            catch (IcmError) { }
                        }
                    }
                    Console.WriteLine("\nbuilt-in capabilities (in the console):");
                    Console.WriteLine("  plain text         ungrounded chat");
                    Console.WriteLine("  /search [src] <q>  grounded answer from a knowledge base");
                    Console.WriteLine("  /route <request>   let the model pick a flow");
                    break;
                }
                case "list":
                {
                    string dir = Arg(args, 1);
                    if (dir == null) throw new IcmError("usage: icm list <dir> [--group G] [--type T] [--json]");
                    string group = null, type = null; bool asJson = false;
                    for (int i = 2; i < args.Length; i++)
                    {
                        if (args[i] == "--group" && i + 1 < args.Length) group = args[++i];
                        else if (args[i] == "--type" && i + 1 < args.Length) type = args[++i];
                        else if (args[i] == "--json") asJson = true;
                    }
                    CmdList(dir, group, type, asJson);
                    break;
                }
                case "selftest":
                    if (SelfTest.RunAll() != 0) Environment.Exit(2);
                    break;
                case "":
                case "-h":
                case "--help":
                case "help":
                    Console.WriteLine(Usage);
                    break;
                default:
                    throw new IcmError("unknown command '" + cmd + "'\n\n" + Usage);
            }
        }

        private const string Usage =
            "icm - ICM host\n" +
            "  icm <dir>                       open the operator console on an ICM dir (VSCode-style; rel or abs)\n" +
            "  icm open  <dir>                 load + summarize an ICM instance\n" +
            "  icm chat  <dir>                 operator console (dispatcher; needs Ollama)\n" +
            "  icm mcp   <dir>                 serve this ICM over MCP (stdio)\n" +
            "  icm flow  <dir> <name> [in...]  run an action chain (flows/<name>/chain.json)\n" +
            "  icm validate <dir> <table>      run the oracle on a table\n" +
            "  icm validate-flow <dir> [name]  lint action chain(s): bad node kinds, missing fields, unknown tools\n" +
            "  icm doctor <dir>                preflight: validate the tools the ratchet declares it needs\n" +
            "  icm reindex <dir>               regenerate manifest.json from files' <!--icm--> blocks\n" +
            "  icm index <kb-dir>              build manifest.json for a knowledge library from file content\n" +
            "  icm list  <dir> [--group G] [--type T] [--json]   enumerate the KB catalog\n" +
            "  icm flows <dir>                 list the instance's workflows (the router's menu)\n" +
            "  icm gen   <dir> <prompt...>     one raw generate call\n" +
            "  icm selftest                    check the deterministic core (no model)\n" +
            "\n" +
            "  env OLLAMA_URL overrides the config ollama_url";

        private static int Main(string[] args)
        {
            try { Run(args); return 0; }
            catch (IcmError e) { Console.Error.WriteLine("error: " + e.Message); return 1; }
        }
    }
}
