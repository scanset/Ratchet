// ICM config loader (icm.config.json). Port of config.rs.
//
// The seam that swaps models and behaviour without touching engine code. One DEVLOG lesson is
// baked in ("the local terminal is a dispatcher, not a chat"): the host carries SEPARATE model
// seats. A small `dispatch` seat makes the one constrained routing decision; the heavy
// `generate` seat only runs behind the oracle. Collapsing them onto one 18 GB model is what
// made the Python chat thrash.

using System;
using System.Collections.Generic;
using System.IO;

namespace Icm
{
    internal class Models
    {
        // The heavy proposer: drafts code/rows/answers. Runs behind the oracle.
        public string Generate = "qwen3-coder:latest";
        // The small seat for the single constrained dispatch/route decision per turn.
        // null falls back to Generate (see Config.DispatchModel).
        public string Dispatch = null;
        // The embedder: narrows candidates, never decides or generates.
        public string Embed = null;
    }

    // A tool the instance exposes (over MCP, or to the local dispatcher). `Kind` selects the
    // engine behaviour; `Name`/`Description` are how a caller sees it. Any extra per-tool fields
    // ride along in `Extra` (the serde flatten analogue).
    internal class Tool
    {
        public string Name = "";
        public string Kind = "";
        public string Description = "";
        public Dictionary<string, object> Extra = new Dictionary<string, object>();

        public static Tool From(Dictionary<string, object> obj)
        {
            var t = new Tool();
            t.Name = Json.GetStringOr(obj, "name", "");
            t.Kind = Json.GetStringOr(obj, "kind", "");
            t.Description = Json.GetStringOr(obj, "description", "");
            foreach (var kv in obj)
            {
                if (kv.Key != "name" && kv.Key != "kind" && kv.Key != "description")
                    t.Extra[kv.Key] = kv.Value;
            }
            return t;
        }

        // The argv to run, from an explicit `command` array, or built from a `script` convenience
        // field (a .ps1 run through powershell). null when this tool declares neither.
        public List<string> CommandTokens()
        {
            object c;
            if (Extra.TryGetValue("command", out c))
            {
                var list = new List<string>();
                foreach (object o in Json.AsArr(c)) if (o != null) list.Add(o.ToString());
                if (list.Count > 0) return list;
            }
            string script = Json.GetString(Extra, "script");
            if (!string.IsNullOrEmpty(script))
                return new List<string>(new string[] { "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script });
            return null;
        }

        // The argument name (if any) whose value is piped to the tool's stdin instead of argv.
        public string StdinArg() { return Json.GetString(Extra, "stdin"); }

        // Per-tool timeout in ms (config `timeout` is in seconds); default 60s.
        public int TimeoutMs()
        {
            double? t = Json.GetNumber(Extra, "timeout");
            return t.HasValue ? (int)(t.Value * 1000) : 60000;
        }

        // The instance-authored JSON Schema for the tool's arguments (or null).
        public object InputSchema()
        {
            object s;
            return Extra.TryGetValue("inputSchema", out s) ? s : null;
        }

        // Optional environment overrides for the tool process.
        public Dictionary<string, object> EnvVars() { return Json.GetObject(Extra, "env"); }
    }

    // Conversational-router behaviour for the terminal console. "confirm" (default) proposes the
    // inferred flow and asks before running; "on" auto-runs a high-confidence match; "off" disables
    // routing (plain text goes straight to /ask).
    internal class Router
    {
        public string Autorun = "confirm";   // confirm | on | off
        public bool Enabled() { return Autorun == "confirm" || Autorun == "on"; }
        public bool AutoRunHigh() { return Autorun == "on"; }
    }

    internal class Config
    {
        public string Name = "";
        public string Domain = "";
        public Models Models = new Models();
        public Router Router = new Router();
        public string OllamaUrl = "http://localhost:11434"; // host and Ollama share the machine
        public List<Tool> Tools = new List<Tool>();
        // Opaque oracle config (e.g. the TSV table schemas, or where to find them). The host
        // keeps it generic; the instance fills in the domain specifics.
        public object Oracle = null;

        // --- the launch config: wiring (where this loaded from, the write root, and dir references) ---
        public string SourcePath = null;   // the config file this loaded from; relative dir refs resolve against its folder
        public string Workdir = null;      // the write/sandbox root (default: the config file's folder)
        public string FlowsDir = null;     // dir overrides; null = the conventional <workdir>/<name>
        public string ToolsDir = null;
        public string SchemasDir = null;
        public string SamplesDir = null;
        public string WorkspacesDir = null; // projects container; may point anywhere (a write location)
        public List<KnowledgeBase> KnowledgeBases = new List<KnowledgeBase>();
        // Opaque preflight checks the host's `doctor` validates (generic mechanism; the instance declares
        // which tools it needs). Each entry: name + one of exe/file/env/http/model/kb/tool + required?/hint.
        public object Requirements = null;

        public static Config Load(string path)
        {
            string text;
            try { text = File.ReadAllText(path); }
            catch (Exception e) { throw new IcmError("reading " + path + ": " + e.Message); }

            Dictionary<string, object> root;
            try { root = Json.AsObject(Json.Parse(text)); }
            catch (Exception e) { throw new IcmError("parsing " + path + ": " + e.Message); }
            if (root == null) throw new IcmError("parsing " + path + ": not a JSON object");

            var c = new Config();
            c.SourcePath = path;
            c.Name = Json.GetStringOr(root, "name", "");
            c.Domain = Json.GetStringOr(root, "domain", "");
            c.Models = ResolveModels(root);
            Dictionary<string, object> ro = Json.GetObject(root, "router");
            if (ro != null) c.Router.Autorun = Json.GetStringOr(ro, "autorun", c.Router.Autorun);
            c.OllamaUrl = Json.GetStringOr(root, "ollama_url", c.OllamaUrl);
            foreach (object t in Json.GetArr(root, "tools"))
            {
                var to = t as Dictionary<string, object>;
                if (to != null) c.Tools.Add(Tool.From(to));
            }
            c.Workdir = Json.GetString(root, "workdir");
            c.FlowsDir = Json.GetString(root, "flowsDir");
            c.ToolsDir = Json.GetString(root, "toolsDir");
            c.SchemasDir = Json.GetString(root, "schemasDir");
            c.SamplesDir = Json.GetString(root, "samplesDir");
            c.WorkspacesDir = Json.GetString(root, "workspacesDir");
            c.KnowledgeBases = KnowledgeBase.LoadList(Json.GetArr(root, "knowledgeBases"));
            object oracle;
            if (root.TryGetValue("oracle", out oracle)) c.Oracle = oracle;
            object reqs;
            if (root.TryGetValue("requirements", out reqs)) c.Requirements = reqs;
            return c;
        }

        // A default config for a directory that has no icm.config.json (e.g. an early manifest-only
        // instance). Keeps the host forgiving: such a dir still opens and can answer from its KB.
        public static Config Default(string name)
        {
            var c = new Config();
            c.Name = string.IsNullOrEmpty(name) ? "(unnamed)" : name;
            return c;
        }

        // Resolve the model seats with a COMPAT SHIM: prefer the nested `models.{...}` (this host's
        // convention), but fall back to the flat `model` / `embed_model` / `dispatch_model` fields the
        // Python ICMs use, so this host can open those instances unchanged.
        private static Models ResolveModels(Dictionary<string, object> root)
        {
            var m = new Models();
            Dictionary<string, object> mo = Json.GetObject(root, "models");

            string gen = mo != null ? Json.GetString(mo, "generate") : null;
            if (gen == null) gen = Json.GetString(root, "model");          // flat fallback
            if (!string.IsNullOrEmpty(gen)) m.Generate = gen;

            string dis = mo != null ? Json.GetString(mo, "dispatch") : null;
            if (dis == null) dis = Json.GetString(root, "dispatch_model"); // flat fallback (rare)
            m.Dispatch = dis;

            string emb = mo != null ? Json.GetString(mo, "embed") : null;
            if (emb == null) emb = Json.GetString(root, "embed_model");    // flat fallback
            m.Embed = emb;

            return m;
        }

        // The dispatch seat, falling back to the generate seat when unset.
        public string DispatchModel()
        {
            return string.IsNullOrEmpty(Models.Dispatch) ? Models.Generate : Models.Dispatch;
        }

    }
}
