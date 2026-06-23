// Doctor - preflight validation of the tools a ratchet declares it needs. Generic mechanism: the host
// knows how to run a small set of check types; the ratchet's ratchet.json `requirements` array says
// which apply. Domain-specific probes (e.g. cl via vcvars) use the `tool` type to run an instance tool.
// Read-only. Reports [ok]/[warn]/[MISS] + hint; returns 0 if all required pass, else 2.
using System;
using System.Collections.Generic;
using System.IO;
using System.Net;

namespace Icm
{
    internal static class Doctor
    {
        public static int Run(Instance icm, string url)
        {
            Console.WriteLine("ratchet: " + icm.Config.Name);
            Console.WriteLine();
            List<object> reqs = Json.AsArr(icm.Config.Requirements);
            if (reqs == null || reqs.Count == 0)
            {
                Console.WriteLine("(no requirements declared in ratchet.json)");
                return 0;
            }

            int problems = 0, warns = 0;
            foreach (object o in reqs)
            {
                Dictionary<string, object> r = o as Dictionary<string, object>;
                if (r == null) continue;
                string name = Json.GetStringOr(r, "name", "(unnamed)");
                bool required = Json.GetBool(r, "required", true);
                string hint = Json.GetStringOr(r, "hint", "");
                bool ok; string detail;
                Check(icm, url, r, out ok, out detail);
                if (ok) { Report("ok", name, detail); }
                else if (required) { Report("MISS", name, detail + Tail(hint)); problems++; }
                else { Report("warn", name, detail + Tail(hint)); warns++; }
            }

            Console.WriteLine();
            if (problems > 0)
            {
                Console.WriteLine("doctor: " + problems + " problem(s), " + warns + " warning(s)");
                return 2;
            }
            Console.WriteLine("doctor: all required checks passed" + (warns > 0 ? " (" + warns + " warning(s))" : ""));
            return 0;
        }

        private static string Tail(string hint) { return string.IsNullOrEmpty(hint) ? "" : "  - " + hint; }

        private static void Report(string tag, string name, string detail)
        {
            Console.WriteLine(("[" + tag + "]").PadRight(8) + name.PadRight(22) + " " + detail);
        }

        private static void Check(Instance icm, string url, Dictionary<string, object> r, out bool ok, out string detail)
        {
            string v;
            if ((v = Json.GetString(r, "exe")) != null) { ok = OnPath(v); detail = ok ? "on PATH" : "not on PATH"; return; }
            if ((v = Json.GetString(r, "file")) != null) { string p = Environment.ExpandEnvironmentVariables(v); ok = File.Exists(p) || Directory.Exists(p); detail = ok ? "present" : ("missing: " + v); return; }
            if ((v = Json.GetString(r, "env")) != null) { string e = Environment.GetEnvironmentVariable(v); ok = !string.IsNullOrEmpty(e); detail = ok ? ("set: " + e) : "not set"; return; }
            if ((v = Json.GetString(r, "http")) != null) { ok = HttpOk(v); detail = ok ? "reachable" : ("unreachable: " + v); return; }
            if ((v = Json.GetString(r, "model")) != null) { ok = HasModel(url, v); detail = ok ? "pulled" : "not pulled"; return; }
            if ((v = Json.GetString(r, "kb")) != null) { CheckKb(icm, v, out ok, out detail); return; }
            if ((v = Json.GetString(r, "tool")) != null) { ok = RunTool(icm, v); detail = ok ? "passed" : "failed"; return; }
            ok = false; detail = "unknown requirement (need one of exe/file/env/http/model/kb/tool)";
        }

        private static bool OnPath(string name)
        {
            string path = Environment.GetEnvironmentVariable("PATH");
            if (string.IsNullOrEmpty(path)) return false;
            string[] exts = new string[] { "", ".exe", ".cmd", ".bat", ".com" };
            foreach (string dir in path.Split(';'))
            {
                if (dir.Length == 0) continue;
                foreach (string ext in exts)
                {
                    try { if (File.Exists(Path.Combine(dir, name + ext))) return true; }
                    catch { }
                }
            }
            return false;
        }

        private static bool HttpOk(string u)
        {
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(u);
                req.Timeout = 5000; req.Method = "GET";
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                {
                    int code = (int)resp.StatusCode;
                    return code >= 200 && code < 300;
                }
            }
            catch { return false; }
        }

        private static bool HasModel(string url, string model)
        {
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url.TrimEnd('/') + "/api/tags");
                req.Timeout = 5000; req.Method = "GET";
                string body;
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                using (StreamReader sr = new StreamReader(resp.GetResponseStream()))
                { body = sr.ReadToEnd(); }
                Dictionary<string, object> root = Json.AsObject(Json.Parse(body));
                if (root == null) return false;
                foreach (object m in Json.GetArr(root, "models"))
                {
                    Dictionary<string, object> mo = m as Dictionary<string, object>;
                    if (mo == null) continue;
                    string nm = Json.GetStringOr(mo, "name", "");
                    if (nm == model || nm.StartsWith(model + ":")) return true;
                }
                return false;
            }
            catch { return false; }
        }

        private static void CheckKb(Instance icm, string kbName, out bool ok, out string detail)
        {
            KnowledgeBase kb = icm.Knowledge().Find(kbName);
            if (kb == null) { ok = false; detail = "no knowledgeBase named " + kbName; return; }
            string dir = kb.Path;
            if (!File.Exists(Path.Combine(dir, Conventions.ManifestFile))) { ok = false; detail = "no manifest (ratchet index " + dir + ")"; return; }
            int files = 0, entries = 0;
            // Count .md the way the indexer does: README.md folder guides are skipped (not routable).
            try { foreach (string fp in Directory.GetFiles(dir, "*.md", SearchOption.AllDirectories)) { if (!Path.GetFileName(fp).Equals("README.md", StringComparison.OrdinalIgnoreCase)) files++; } } catch { }
            try { entries = Indexer.LoadManifestMap(dir).Count; } catch { }
            ok = (entries == files);
            detail = ok ? (files + " docs, manifest current") : ("drift: " + files + " docs vs " + entries + " entries (reindex)");
        }

        private static bool RunTool(Instance icm, string toolName)
        {
            Tool t = icm.FindTool(toolName);
            if (t == null) return false;
            try
            {
                ToolRunResult rr = ToolRunner.Run(icm, t, new Dictionary<string, object>());
                return rr.Ok && rr.Error == null;
            }
            catch { return false; }
        }
    }
}
