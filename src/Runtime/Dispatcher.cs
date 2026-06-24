// The dispatcher: the operator console's command layer. Reusable across the console REPL
// (Cli/ConsoleChat.cs), the MCP server, and the flow engine.
//
// The command model: plain text is UNGROUNDED chat (the model never picks an action from it).
// Acting is always an explicit slash command - /search (grounded answer over a knowledge base),
// /route (model picks ONE flow from the closed catalog, gated + confirmed), /flow (run a named
// flow), /do (run a named tool or a pasted shell command), /recipes, /propose, /ws. The model
// proposes into constrained slots; a deterministic gate/oracle decides; the operator drives.
//
// Threading: a Dispatcher is single-threaded per use - one in-flight operation at a time (the
// `cancel` handle is reassigned per call). Callers serialize their calls.

using System;
using System.Collections.Generic;
using System.Text;

namespace Icm
{
    internal class Dispatcher
    {
        private const int DispatchTimeoutMs = 60000;
        private const int GenTimeoutMs = 300000;
        private const int RewriteTimeoutMs = 30000;
        private const int MaxHistory = 6;        // turns kept for coreference rewrite
        private const int MaxProposeRepairs = 4; // bounded repair on a failing proposed row
        private const int MaxProblemsShown = 40;
        private const int RouteCandidateK = 8;       // embedder narrows a single KB route to this many
        private const int RouteManyCandidateK = 12;  // ... and a multi-route to this many

        private readonly Instance icm;
        private readonly string url;
        private readonly Action<string> status;
        private readonly List<string> history = new List<string>(); // "you: ..." / "icm: ..." lines
        private Cancel cancel;                                       // the in-flight op's cancel handle
        private string pendingFlowId;   // a router-proposed flow awaiting y/n confirmation
        private string pendingArgs;
        private string pendingRedirect; // a "> path" to save the pending flow's output to, if any
        private bool streamedThisTurn;  // set when this turn streamed its output via OnToken
        private string activeWorkspace; // the session focus: a relative path under workspaces/, or null

        // Optional per-caller token sink. When set (the console sets it), freeform generation
        // (ask / make / chat) streams tokens here as they arrive. Null = non-streaming (MCP).
        public Action<string> OnToken;

        public Dispatcher(Instance icm, string url, Action<string> status)
        {
            this.icm = icm;
            this.url = url;
            this.status = status != null ? status : delegate(string s) { };
        }

        public Instance Icm { get { return icm; } }
        public string Url { get { return url; } }

        private void Status(string msg) { status(msg); }

        // Abort the in-flight operation's model call (best effort). Safe to call from another thread.
        public void CancelCurrent() { Cancel c = cancel; if (c != null) c.Abort(); }

        // One turn: conversation rewrite (if there is history) -> classify -> run capability.
        // Never throws for model/oracle failures; it returns a TurnResult with IsError set.
        // One turn. A '/' line is a slash command (deterministic dispatch). Plain text runs through the
        // conversational ROUTER: the model proposes a flow from the closed catalog, a deterministic gate
        // decides, and a confident match is run (after y/n confirmation by default) or falls back to
        // /ask. /chat is free conversation; /do is the classify-and-route path.
        public TurnResult Turn(string line)
        {
            var r = new TurnResult();
            line = (line ?? "").Trim();
            r.Standalone = line;
            if (line.Length == 0) { r.Text = ""; return r; }
            cancel = new Cancel();
            streamedThisTurn = false;

            // Resolve a pending router confirmation (a plain y/n answer to "Run the X flow?").
            if (pendingFlowId != null && line[0] != '/')
            {
                if (IsAffirmative(line))
                {
                    string id = pendingFlowId, args = pendingArgs, rd = pendingRedirect;
                    pendingFlowId = null; pendingArgs = null; pendingRedirect = null;
                    r.Intent = "flow:" + id;
                    Status("router: running '" + id + "'");
                    RunNamedFlow(id, args, r);
                    ApplyRedirect(r, rd, "flow:" + id, args);
                    return Done(r, line);
                }
                if (IsNegative(line))
                { pendingFlowId = null; pendingArgs = null; pendingRedirect = null; r.Intent = "chat"; r.Text = "Cancelled."; return Done(r, line); }
                pendingFlowId = null; pendingArgs = null; pendingRedirect = null; // anything else cancels, handled below
            }

            if (line[0] == '/')
            {
                RunSlash(line, r);
                if (r.Intent != "clear") Done(r, line);
                return r;
            }

            // Plain text is ungrounded chat. Acting is always explicit via a slash command; the model
            // never picks an action from plain text. A trailing "> path" still saves the reply.
            string redirect; string clean = ParseRedirect(line, out redirect);
            r.Intent = "chat"; r.Text = DoChat(clean);
            ApplyRedirect(r, redirect, "chat", clean);
            return Done(r, line);
        }

        private TurnResult Done(TurnResult r, string line)
        {
            r.Streamed = streamedThisTurn;
            Remember("you: " + line);
            Remember("icm: " + (r.IsError ? r.Text : Truncate(r.Text, 400)));
            return r;
        }

        // Generate freeform text, streaming to OnToken when a front end has wired it (the console);
        // otherwise a normal blocking call. Either way returns the full text.
        private string GenerateMaybeStream(string prompt, double temperature)
        {
            if (OnToken != null)
            {
                streamedThisTurn = true;
                return Ollama.GenerateStream(url, icm.Config.Models.Generate, prompt, temperature, GenTimeoutMs, OnToken, cancel);
            }
            return Ollama.Generate(url, icm.Config.Models.Generate, prompt, null, temperature, GenTimeoutMs, cancel);
        }

        private static bool IsAffirmative(string s)
        {
            string t = s.Trim().ToLowerInvariant();
            return t == "y" || t == "yes" || t == "yeah" || t == "yep" || t == "ok" || t == "okay" || t == "sure" || t == "run" || t == "do it" || t == "go";
        }

        private static bool IsNegative(string s)
        {
            string t = s.Trim().ToLowerInvariant();
            return t == "n" || t == "no" || t == "nope" || t == "cancel" || t == "stop";
        }

        // --- the conversational router: propose a flow from the closed catalog, gate it, act ---

        private class RouteResult { public string FlowId = ""; public string Args = ""; public string Confidence = "low"; }

        internal enum GateDecision { Match, Fallback }

        // Deterministic gate: a proposal only proceeds if it names an on-list flow with non-low
        // confidence. Pure + static so SelfTest can cover it without the model.
        internal static GateDecision Gate(string flowId, string confidence, List<string> validIds)
        {
            if (string.IsNullOrEmpty(flowId) || flowId == "none") return GateDecision.Fallback;
            if (!validIds.Contains(flowId)) return GateDecision.Fallback;
            if (confidence == "low") return GateDecision.Fallback;
            return GateDecision.Match;
        }

        // The explicit flow router (/route): the model proposes ONE flow from the closed catalog, the
        // deterministic gate decides, and a confident match runs (after y/n unless autorun=on). No
        // /ask fallback - a miss just tells the operator how to run a flow by name.
        private void DoRoute(string line, string redirect, TurnResult r)
        {
            Status("route: matching a flow");
            RouteResult rr = null;
            try { rr = RouteFlow(line); }
            catch (IcmError) { rr = null; }     // model down/failed -> report a miss

            var ids = new List<string>();
            foreach (FlowInfo fi in FlowCatalog()) ids.Add(fi.Id);

            if (rr == null || Gate(rr.FlowId, rr.Confidence, ids) == GateDecision.Fallback)
            { r.Intent = "route"; r.Text = "No flow matched. Run one by name with /flow <name>, see /flows, or rephrase."; return; }

            // Auto-run only when configured AND the model is highly confident.
            if (icm.Config.Router.AutoRunHigh() && rr.Confidence == "high")
            {
                r.Intent = "flow:" + rr.FlowId;
                Status("route: running '" + rr.FlowId + "' (high)");
                RunNamedFlow(rr.FlowId, rr.Args, r);
                if (!string.IsNullOrEmpty(redirect)) ApplyRedirect(r, redirect, "flow:" + rr.FlowId, rr.Args);
                else r.Text = "-> routed to `" + rr.FlowId + "` (high)\n\n" + r.Text;
                return;
            }

            // Otherwise propose and wait for confirmation (carrying the redirect to the y/n turn).
            pendingFlowId = rr.FlowId; pendingArgs = rr.Args; pendingRedirect = redirect;
            r.Intent = "route";
            string argNote = string.IsNullOrEmpty(rr.Args) ? "" : " with: " + rr.Args;
            string saveNote = string.IsNullOrEmpty(redirect) ? "" : " (saves to " + redirect + ")";
            r.Text = "This looks like the `" + rr.FlowId + "` flow (" + rr.Confidence + " confidence)" + argNote + saveNote +
                ".\nRun it? (y / n) - or type a slash command instead.";
        }

        private RouteResult RouteFlow(string request)
        {
            List<FlowInfo> flows = NarrowFlows(request, FlowCatalog(), RouteCandidateK);
            if (flows.Count == 0) return null;
            var ids = new List<string>();
            var lines = new List<string>();
            foreach (FlowInfo fi in flows) { ids.Add(fi.Id); lines.Add("- " + fi.Id + ": " + fi.WhenToUse); }
            ids.Add("none");

            object schema = Json.Schema(Json.Obj(
                "flow_id", Json.EnumProp(ids),
                "args", Json.StrProp(),
                "confidence", Json.EnumProp(new string[] { "high", "medium", "low" })), "flow_id", "confidence");
            string prompt =
                "Route the operator's request to ONE workflow, or 'none' if no workflow fits (a plain " +
                "question with no matching workflow is 'none'). Put the task/topic from their message in " +
                "args. Rate confidence honestly.\n\nWorkflows:\n" + string.Join("\n", lines.ToArray()) +
                "\n\nOperator: " + request + "\n\nReturn JSON {flow_id, args, confidence}.";
            Dictionary<string, object> v = Ollama.GenerateJson(url, icm.Config.DispatchModel(), prompt, schema, 0.1, DispatchTimeoutMs, cancel);
            var rr = new RouteResult();
            rr.FlowId = Json.GetStringOr(v, "flow_id", "none");
            rr.Args = Json.GetStringOr(v, "args", "");
            rr.Confidence = Json.GetStringOr(v, "confidence", "low");
            if (rr.Args.Length == 0) rr.Args = request;   // default the flow input to the raw line
            return rr;
        }

        // Embedder narrowing (the ICM "embedder" role): rank candidates by similarity to the query and
        // keep the top-k, so the constrained model pick chooses from a short, relevant list. Falls back
        // to all candidates when the embed seat is unset or Ollama is unreachable. No-op at small sizes.
        private List<Entry> NarrowEntries(string query, List<Entry> entries, int k)
        {
            if (entries.Count <= k) return entries;
            var cands = new List<Cand>();
            foreach (Entry e in entries)
                cands.Add(new Cand(e.Id, e.Title + ". " + e.Summary + " " + string.Join(" ", e.Keywords.ToArray())));
            List<string> top = Embedder.RankTopK(icm, url, icm.Config.Models.Embed, query, cands, k, status);
            if (top == null || top.Count == 0) return entries;
            var byId = new Dictionary<string, Entry>();
            foreach (Entry e in entries) byId[e.Id] = e;
            var outl = new List<Entry>();
            foreach (string id in top) { Entry e; if (byId.TryGetValue(id, out e)) outl.Add(e); }
            if (outl.Count == 0) return entries;
            Status("route: embedding-narrowed to " + outl.Count + " of " + entries.Count + " entries");
            return outl;
        }

        private List<FlowInfo> NarrowFlows(string query, List<FlowInfo> flows, int k)
        {
            if (flows.Count <= k) return flows;
            var cands = new List<Cand>();
            foreach (FlowInfo fi in flows) cands.Add(new Cand(fi.Id, fi.Name + ". " + fi.WhenToUse));
            List<string> top = Embedder.RankTopK(icm, url, icm.Config.Models.Embed, query, cands, k, status);
            if (top == null || top.Count == 0) return flows;
            var byId = new Dictionary<string, FlowInfo>();
            foreach (FlowInfo fi in flows) byId[fi.Id] = fi;
            var outl = new List<FlowInfo>();
            foreach (string id in top) { FlowInfo fi; if (byId.TryGetValue(id, out fi)) outl.Add(fi); }
            return outl.Count > 0 ? outl : flows;
        }

        private List<FlowInfo> FlowCatalog()
        {
            var outl = new List<FlowInfo>();
            string dir = icm.FlowsDirAbs();
            if (!System.IO.Directory.Exists(dir)) return outl;
            // Action chains (flows/<chain>/chain.json) - the routable workflows.
            string[] subs;
            try { subs = System.IO.Directory.GetDirectories(dir); } catch { subs = new string[0]; }
            System.Array.Sort(subs, System.StringComparer.OrdinalIgnoreCase);
            foreach (string sub in subs)
            {
                if (!Chain.IsChainDir(sub)) continue;
                try { Chain c = Chain.Load(sub); outl.Add(new FlowInfo { Id = System.IO.Path.GetFileName(sub), Name = c.Id, WhenToUse = c.Summary }); }
                catch (IcmError) { }
            }
            return outl;
        }

        // Split "/cmd the rest" into ("cmd", "the rest"); cmd is lowercased. Public for SelfTest.
        public static void ParseCommand(string line, out string cmd, out string rest)
        {
            string body = (line != null && line.StartsWith("/")) ? line.Substring(1) : (line ?? "");
            SplitFirst(body, out cmd, out rest);
            cmd = cmd.ToLowerInvariant();
        }

        private static void SplitFirst(string s, out string first, out string rest)
        {
            s = (s ?? "").TrimStart();
            int i = 0; while (i < s.Length && !char.IsWhiteSpace(s[i])) i++;
            first = s.Substring(0, i);
            rest = (i < s.Length) ? s.Substring(i).Trim() : "";
        }

        private void RunSlash(string line, TurnResult r)
        {
            string cmd, rest;
            ParseCommand(line, out cmd, out rest);
            string redirect; rest = ParseRedirect(rest, out redirect);
            string task = rest;
            r.Intent = cmd;
            Status("command: /" + cmd);
            try
            {
                switch (cmd)
                {
                    case "help": case "h": case "?": r.Text = Help(); break;
                    case "search": case "docs":
                        if (rest.Length == 0) { Usage(r, "/search [source] <query>   (source: a KB name or a path; -r for raw hits)"); break; }
                        r.Intent = "search"; r.Text = DoSearchKb(rest); break;
                    case "route":
                        if (rest.Length == 0) { Usage(r, "/route <request>"); break; }
                        DoRoute(rest, redirect, r); redirect = null; break;   // DoRoute carries the redirect itself
                    case "flow":
                    {
                        // Generic: run any authored flow by name. Domain shortcuts are instance-declared
                        // command aliases (icm.config.json), handled in default.
                        string name, input; SplitFirst(rest, out name, out input);
                        if (name.Length == 0) { Usage(r, "/flow <name> <input>"); break; }
                        RunNamedFlow(name, input, r); break;
                    }
                    case "do":
                        if (rest.Length == 0) { Usage(r, "/do <tool [arg] | shell command>"); break; }
                        DoExec(rest, r); break;
                    case "propose":
                        if (rest.Length == 0) { Usage(r, "/propose <description>"); break; }
                        DoPropose(rest, r); break;
                    case "ws":
                        DoWs(rest, r); break;
                    case "flows":
                    {
                        var sb = new StringBuilder();
                        var fl = FlowCatalog();
                        sb.Append("Authored flows (/route can match these, or run with /flow <name>):\n");
                        if (fl.Count == 0) sb.Append("  (none in flows/)");
                        else foreach (FlowInfo fi in fl) sb.Append("  " + fi.Id + " - " + fi.WhenToUse + "\n");
                        r.Text = sb.ToString().TrimEnd();
                        break;
                    }
                    case "note":
                        if (rest.Length == 0) { Usage(r, "/note <text>"); break; }
                        AppendNote(rest); r.Text = "noted."; break;
                    case "notes":
                    {
                        string notes = ReadNotes();
                        r.Text = notes.Length > 0 ? notes : "(no notes yet - use /note <text>, or redirect a write with '> path')";
                        break;
                    }
                    case "clear": case "reset":
                        history.Clear(); r.Intent = "clear"; r.Text = ""; break;
                    case "quit": case "exit": case "q":
                        r.Intent = Conventions.Intent.Quit; r.Text = "bye"; break;
                    default:
                        // Not a known command. Bare text is chat; slashes are strictly for commands.
                        r.Text = "If you are trying to use a slash command, type /help to see available commands.";
                        break;
                }
            }
            catch (IcmError e) { r.IsError = true; r.Text = "[error] " + e.Message; }

            ApplyRedirect(r, redirect, "/" + cmd, task);
        }

        private static void Usage(TurnResult r, string usage) { r.Text = "Usage: " + usage; r.IsError = true; }

        private Tool FindTool(string name) { return icm.FindTool(name); }

        // Run a declared command/script tool, mapping `rest` to argName (else the tool's stdin arg, else
        // its first required input). Used by /tool and tool-kind command aliases.
        private void RunToolByName(string toolName, string argName, string rest, TurnResult r)
        {
            Tool t = FindTool(toolName);
            if (t == null) { r.Text = "no such tool: " + toolName; r.IsError = true; return; }
            var args = new Dictionary<string, object>();
            string key = argName;
            if (string.IsNullOrEmpty(key)) key = t.StdinArg();
            if (string.IsNullOrEmpty(key)) key = FirstRequiredArg(t);
            if (!string.IsNullOrEmpty(key) && !string.IsNullOrEmpty(rest)) args[key] = rest;
            ToolRunResult rr = ToolRunner.Run(icm, t, args);
            r.Text = rr.Error != null ? rr.Error : (rr.Output.Length > 0 ? rr.Output : "(no output)");
            r.IsError = rr.Error != null || !rr.Ok;
        }

        private static string FirstRequiredArg(Tool t)
        {
            var schema = t.InputSchema() as Dictionary<string, object>;
            if (schema == null) return null;
            List<object> req = Json.GetArr(schema, "required");
            return req.Count > 0 && req[0] != null ? req[0].ToString() : null;
        }

        // Write a command/flow's text output to a workspace file ("> path"): code fences stripped so a
        // .cs/.ps1 lands clean, recorded in NOTES.md. Clears the streamed flag so the "Wrote" line shows
        // even when the body was streamed live.
        private void ApplyRedirect(TurnResult r, string redirect, string label, string task)
        {
            if (string.IsNullOrEmpty(redirect) || r.IsError || string.IsNullOrEmpty(r.Text)
                || r.Intent == "clear" || r.Intent == Conventions.Intent.Quit) return;
            try
            {
                string content = Markdown.StripFence(r.Text);
                icm.WriteFile(redirect, content);
                r.WrittenPath = icm.Resolve(redirect);
                AppendNote("wrote `" + redirect + "` (" + label + ": " + Truncate(task, 80) + ")");
                r.Text = "Wrote " + redirect + " (" + content.Length + " chars).";
                streamedThisTurn = false;
            }
            catch (IcmError e) { r.IsError = true; r.Text = "[error] writing " + redirect + ": " + e.Message; }
        }

        // Parse a trailing " > path" redirect off a command's argument. The target must look like a path
        // (has an extension or a separator, no spaces) so prose with " > " is not misread. Returns the
        // argument without the redirect; sets path (null if none). Public for SelfTest.
        public static string ParseRedirect(string rest, out string path)
        {
            path = null;
            if (rest == null) return "";
            int idx = rest.LastIndexOf(" > ", StringComparison.Ordinal);
            if (idx >= 0)
            {
                string p = rest.Substring(idx + 3).Trim();
                bool looksLikePath = p.Length > 0 && p.IndexOf(' ') < 0
                    && (p.IndexOf('.') >= 0 || p.IndexOf('\\') >= 0 || p.IndexOf('/') >= 0);
                if (looksLikePath) { path = p; return rest.Substring(0, idx).Trim(); }
            }
            return rest;
        }

        // --- session memory (NOTES.md in the instance) ---

        private void AppendNote(string text)
        {
            string existing = ReadNotes();
            if (existing.Length == 0) existing = "# " + icm.Config.Name + " - session notes\n";
            string stamp = DateTime.Now.ToString("yyyy-MM-dd HH:mm");
            try { icm.WriteFile(Conventions.NotesFile, existing.TrimEnd() + "\n- [" + stamp + "] " + text + "\n"); }
            catch (IcmError) { }
        }

        private string ReadNotes()
        {
            try { return icm.ReadFile(Conventions.NotesFile); } catch (IcmError) { return ""; }
        }

        private void RunNamedFlow(string name, string input, TurnResult r)
        {
            // Run the action chain at flows/<name>/chain.json on the ChainEngine.
            string chainDir = System.IO.Path.Combine(icm.FlowsDirAbs(), name);
            if (!Chain.IsChainDir(chainDir))
            { r.IsError = true; r.Text = "no flow '" + name + "' (expected flows/" + name + "/chain.json)"; return; }
            Chain c;
            try { c = Chain.Load(chainDir); }
            catch (IcmError e) { r.IsError = true; r.Text = "[error] loading chain '" + name + "': " + e.Message; return; }
            ChainResult cr = new ChainEngine(icm, this, status).Run(c, input, activeWorkspace);
            r.Text = cr.Text; r.IsError = cr.IsError;
        }

        // --- /search over knowledge-base DIRECTORIES, then ground an answer on the hits ---

        private const int SearchK = 6;
        private const int DoCommandTimeoutMs = 120000;

        // /search [source] <query>: ground an answer on a knowledge source. Source resolution: a
        // registered KB name, a path (absolute or instance-relative), or - if omitted - the default
        // KB(s) (else the instance kb/ dir). "-r" / "--hits" returns the raw locations instead.
        private string DoSearchKb(string rest)
        {
            bool hitsOnly = false;
            rest = StripFlag(rest, "-r", ref hitsOnly);
            rest = StripFlag(rest, "--hits", ref hitsOnly);
            rest = rest.Trim();
            if (rest.Length == 0) return "Usage: /search [source] <query>   (source: a KB name or a path; -r for raw hits)";

            KnowledgeRegistry reg = icm.Knowledge();
            string first, more; SplitFirst(rest, out first, out more);
            string dirAbs, key, query, label;

            KnowledgeBase kb = reg.Find(first);
            string adhoc;
            if (kb != null) { dirAbs = kb.Path; key = kb.Name; query = more; label = kb.Name; }
            else if ((adhoc = ResolveSearchPath(first)) != null) { dirAbs = adhoc; key = null; query = more; label = first; }
            else
            {
                List<KnowledgeBase> defs = reg.Defaults();
                if (defs.Count > 0) { dirAbs = defs[0].Path; key = defs[0].Name; label = defs[0].Name; }
                else { dirAbs = System.IO.Path.Combine(icm.Root, Conventions.KbDir); key = "kb"; label = "kb"; }
                query = rest;
            }
            if (query.Trim().Length == 0) return "Usage: /search [source] <query>";

            Status("search: '" + label + "' for " + Truncate(query, 60));
            // Dispatch tier: BM25 narrows to candidates; the library's manifest supplies summaries;
            // the model picks ONE (or it's deterministic when there's a single candidate); read + ground.
            List<string> ranked = KbIndex.Rank(icm, key, dirAbs, query, RouteCandidateK);
            if (ranked.Count == 0) return "(no matches for '" + query + "' in " + label + ")";
            Dictionary<string, Entry> man = Indexer.LoadManifestMap(dirAbs);

            if (hitsOnly)
            {
                var sbh = new StringBuilder();
                foreach (string rel in ranked)
                {
                    Entry e; bool has = man.TryGetValue(rel, out e);
                    string title = has ? e.Title : rel;
                    string sum = (has && e.Summary.Length > 0) ? "  -  " + e.Summary : "";
                    sbh.Append(rel + "  (" + title + ")" + sum + "\n");
                }
                return sbh.ToString().TrimEnd();
            }

            string picked = ranked.Count == 1 ? ranked[0] : PickDoc(query, ranked, man, label);
            if (string.IsNullOrEmpty(picked)) picked = ranked[0];

            string content;
            try { content = Indexer.StripMeta(System.IO.File.ReadAllText(System.IO.Path.Combine(dirAbs, picked.Replace('/', System.IO.Path.DirectorySeparatorChar)))); }
            catch (Exception e) { return "[error] reading " + picked + ": " + e.Message; }

            string system;
            try { system = icm.ReadFile(Conventions.SystemFile); } catch (IcmError) { system = ""; }
            var sb = new StringBuilder();
            if (system.Length > 0) sb.Append(system + "\n\n");
            string focus = WorkspaceFocus();
            if (focus.Length > 0) sb.Append(focus + "\n\n");
            sb.Append("Answer the question using ONLY the reference below, from '" + label + "/" + picked +
                "'. If it does not contain the answer, say so.\n\n");
            sb.Append("--- " + picked + " ---\n" + Truncate(content, 6000) + "\n--- END ---\n\nQuestion: " + query);
            Status("search: grounding on " + picked);
            return GenerateMaybeStream(sb.ToString(), 0.2);
        }

        // Model enum-pick of ONE doc from the BM25-narrowed candidates, by manifest summary. The
        // /search dispatch decision; falls back to the top candidate if the model is unavailable.
        private string PickDoc(string query, List<string> candidates, Dictionary<string, Entry> man, string label)
        {
            var ids = new List<string>(candidates);
            ids.Add("none");
            var lines = new List<string>();
            foreach (string rel in candidates)
            {
                Entry e; bool has = man.TryGetValue(rel, out e);
                string desc = (has && e.Summary.Length > 0) ? e.Summary : (has ? e.Title : rel);
                lines.Add("- " + rel + " : " + desc);
            }
            object schema = Json.Schema(Json.Obj("doc", Json.EnumProp(ids)), "doc");
            string prompt = "Pick the ONE document whose content best answers the question, or 'none'.\n\n" +
                "Documents in '" + label + "':\n" + string.Join("\n", lines.ToArray()) + "\n\nQuestion: " + query;
            try
            {
                Dictionary<string, object> v = Ollama.GenerateJson(url, icm.Config.DispatchModel(), prompt, schema, 0.1, DispatchTimeoutMs, cancel);
                string doc = Json.GetStringOr(v, "doc", "none");
                return doc == "none" ? null : doc;
            }
            catch (IcmError) { return candidates[0]; }
        }

        // Resolve a /search source token to an existing directory: an absolute path as-is, or an
        // instance-relative path through the sandbox. Returns null when it is not an existing dir.
        private string ResolveSearchPath(string token)
        {
            if (string.IsNullOrEmpty(token)) return null;
            try
            {
                if (System.IO.Path.IsPathRooted(token))
                    return System.IO.Directory.Exists(token) ? System.IO.Path.GetFullPath(token) : null;
                string p = icm.Resolve(token);
                return System.IO.Directory.Exists(p) ? p : null;
            }
            catch (IcmError) { return null; }
        }

        // Remove a whole-token flag (e.g. "-r") from a command argument; set `set` when found.
        private static string StripFlag(string s, string flag, ref bool set)
        {
            if (string.IsNullOrEmpty(s)) return s;
            string padded = " " + s + " ";
            string find = " " + flag + " ";
            int i = padded.IndexOf(find, StringComparison.Ordinal);
            if (i < 0) return s;
            set = true;
            return padded.Remove(i, find.Length - 1).Trim();
        }

        // --- /do: run a declared tool by name, or a pasted shell command; output enters context ---

        private void DoExec(string rest, TurnResult r)
        {
            string first, more; SplitFirst(rest, out first, out more);
            Tool t = FindTool(first);
            if (t != null) { r.Intent = "do:" + first; RunToolByName(first, null, more, r); return; }
            r.Intent = "do";
            Status("do: running a shell command");
            try { r.Text = RunPastedCommand(rest); }
            catch (Exception e) { r.IsError = true; r.Text = "[error] running command: " + e.Message; }
            // The output is remembered in history by Done(), so the next chat/search turn can use it.
        }

        // Run an operator-typed shell command via powershell, capturing stdout+stderr. The command is
        // passed as a base64 UTF-16 -EncodedCommand, which dodges quoting and the redirected-stdin BOM
        // entirely. Operator-authorized arbitrary execution: the model never composes a /do command.
        // Working dir is the active workspace (else the instance root).
        private string RunPastedCommand(string command)
        {
            var psi = new System.Diagnostics.ProcessStartInfo();
            psi.FileName = "powershell.exe";
            // Wrap so all streams merge to text (no CLIXML on a redirected stderr) and progress is silent.
            string wrapped = "$ProgressPreference='SilentlyContinue'; & {" + command + "} *>&1 | Out-String";
            byte[] cmdBytes = System.Text.Encoding.Unicode.GetBytes(wrapped);
            psi.Arguments = "-NoProfile -ExecutionPolicy Bypass -EncodedCommand " + System.Convert.ToBase64String(cmdBytes);
            psi.UseShellExecute = false;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.CreateNoWindow = true;
            string wd = WorkspaceDirAbs();
            psi.WorkingDirectory = wd != null ? wd : icm.Root;

            var sb = new StringBuilder();
            var proc = new System.Diagnostics.Process();
            proc.StartInfo = psi;
            proc.OutputDataReceived += delegate(object o, System.Diagnostics.DataReceivedEventArgs e) { if (e.Data != null) lock (sb) { sb.Append(e.Data); sb.Append("\n"); } };
            proc.ErrorDataReceived += delegate(object o, System.Diagnostics.DataReceivedEventArgs e) { if (e.Data != null) lock (sb) { sb.Append(e.Data); sb.Append("\n"); } };
            proc.Start();
            proc.BeginOutputReadLine();
            proc.BeginErrorReadLine();
            if (!proc.WaitForExit(DoCommandTimeoutMs))
            {
                try { proc.Kill(); } catch { }
                lock (sb) sb.Append("\n[timed out after " + (DoCommandTimeoutMs / 1000) + "s]");
            }
            else proc.WaitForExit(); // let async buffers flush
            string outp; lock (sb) outp = sb.ToString().TrimEnd();
            if (outp.Length == 0) outp = "(no output; exit " + SafeExit(proc) + ")";
            return Truncate(outp, 8000);
        }

        private static string SafeExit(System.Diagnostics.Process p) { try { return p.ExitCode.ToString(); } catch { return "?"; } }


        // --- /ws: switch or create the active workspace (the session focus). Non-destructive. ---

        private void DoWs(string rest, TurnResult r)
        {
            r.Intent = "ws";
            string sub, name; SplitFirst(rest, out sub, out name);
            sub = sub.ToLowerInvariant(); name = name.Trim();
            if (sub != "switch" && sub != "create") { r.Text = "Usage: /ws switch <name> | /ws create <name>"; r.IsError = true; return; }
            if (name.Length == 0) { Usage(r, "/ws " + sub + " <name>"); return; }
            if (name.IndexOf('/') >= 0 || name.IndexOf('\\') >= 0 || name.IndexOf("..") >= 0)
            { r.Text = "workspace name may not contain path separators or '..'"; r.IsError = true; return; }

            // The projects container is configurable (workspacesDir) and may sit outside the workdir
            // sandbox, so create/write directly (operator-authorized via the config) rather than via Resolve.
            string abs = System.IO.Path.Combine(icm.WorkspacesDirAbs(), name);
            if (sub == "switch")
            {
                if (!System.IO.Directory.Exists(abs)) { r.Text = "no workspace '" + name + "' (create it with /ws create " + name + ")"; r.IsError = true; return; }
                activeWorkspace = abs; r.Text = "active workspace: " + name;
            }
            else // create
            {
                if (System.IO.Directory.Exists(abs)) { r.Text = "workspace '" + name + "' already exists (use /ws switch " + name + ")"; r.IsError = true; return; }
                try
                {
                    System.IO.Directory.CreateDirectory(abs);
                    System.IO.File.WriteAllText(System.IO.Path.Combine(abs, "project.json"), Json.SerializePretty(Json.Obj("name", name)));
                }
                catch (Exception e) { r.Text = "[error] creating workspace: " + e.Message; r.IsError = true; return; }
                activeWorkspace = abs; r.Text = "created and switched to workspace: " + name;
            }
        }

        // Absolute path of the active workspace (set by /ws), or null when none is set.
        private string WorkspaceDirAbs() { return string.IsNullOrEmpty(activeWorkspace) ? null : activeWorkspace; }

        // The session-focus block injected into chat/search prompts: the active workspace's name,
        // path, project.json, and a shallow file list. Bounded; empty when no workspace is active.
        private string WorkspaceFocus()
        {
            string abs = WorkspaceDirAbs();
            if (abs == null) return "";
            string name = System.IO.Path.GetFileName(abs.TrimEnd('\\', '/'));
            var sb = new StringBuilder();
            sb.Append("Active workspace: " + name + " (" + abs + ")");
            try
            {
                string projPath = System.IO.Path.Combine(abs, "project.json");
                if (System.IO.File.Exists(projPath))
                    sb.Append("\nproject.json: " + Truncate(System.IO.File.ReadAllText(projPath).Replace('\n', ' '), 300));
            }
            catch { }
            try
            {
                var names = new List<string>();
                foreach (string d in System.IO.Directory.GetDirectories(abs)) names.Add(System.IO.Path.GetFileName(d) + "/");
                foreach (string f in System.IO.Directory.GetFiles(abs)) names.Add(System.IO.Path.GetFileName(f));
                if (names.Count > 0)
                {
                    names.Sort(StringComparer.OrdinalIgnoreCase);
                    if (names.Count > 40) names = names.GetRange(0, 40);
                    sb.Append("\nfiles: " + string.Join(", ", names.ToArray()));
                }
            }
            catch { }
            return sb.ToString();
        }

        // Casual conversation: the model talks the operator through planning, aware of the available
        // slash commands and the KB catalog so it can point at the exact command to run.
        // Plain-text conversation: UNGROUNDED chat. Not grounded in the KB - to ground an answer the
        // operator uses /search. The model is told it cannot act (the operator drives via slash
        // commands) and is given the active-workspace focus + recent history as the only context.
        private string DoChat(string line)
        {
            string system;
            try { system = icm.ReadFile(Conventions.SystemFile); } catch (IcmError) { system = ""; }
            var sb = new StringBuilder();
            if (system.Length > 0) sb.Append(system + "\n\n");
            sb.Append("You are the conversational assistant of the '" + icm.Config.Name + "' tool for: " + icm.Config.Domain + ". ");
            sb.Append("Chat to plan; you cannot run tools or edit files yourself - the operator acts by typing slash commands. ");
            sb.Append("When an action would help, name the exact command (/search, /route, /flow, /do, /recipes, /propose, /ws). Do not invent commands or facts.\n");
            string focus = WorkspaceFocus();
            if (focus.Length > 0) sb.Append("\n" + focus + "\n");
            string notes = ReadNotes();
            if (notes.Length > 0) sb.Append("\nProject notes (NOTES.md):\n" + Truncate(notes, 1500) + "\n");
            if (history.Count > 0) sb.Append("\nConversation so far:\n" + string.Join("\n", history.ToArray()) + "\n");
            sb.Append("\nOperator: " + line + "\n\nReply briefly and concretely.");
            return GenerateMaybeStream(sb.ToString(), 0.4);
        }

        private void Remember(string entry)
        {
            history.Add(entry);
            while (history.Count > MaxHistory) history.RemoveAt(0);
        }

        private static string Truncate(string s, int max)
        {
            if (s == null) return "";
            return s.Length <= max ? s : s.Substring(0, max) + " ...";
        }

        // Constrained route to a KB entry id (or null). The grounding step for `ask`.
        private string Route(string query)
        {
            if (icm.Manifest == null || icm.Manifest.Entries.Count == 0) return null;
            List<Entry> entries = NarrowEntries(query, icm.Manifest.Entries, RouteCandidateK);

            var ids = new List<string>();
            var lines = new List<string>();
            foreach (Entry e in entries)
            {
                ids.Add(e.Id);
                string grp = e.Group.Length > 0 ? " (" + e.Group + ")" : "";
                string kw = e.Keywords.Count > 0 ? "  [keywords: " + string.Join(", ", e.Keywords.ToArray()) + "]" : "";
                lines.Add("- " + e.Id + grp + " : " + e.Title + " - " + e.Summary + kw);
            }
            ids.Add("none");

            object schema = Json.Schema(Json.Obj("entry_id", Json.EnumProp(ids)), "entry_id");
            string prompt =
                "Pick the single KB entry whose content can answer the question, or 'none' if nothing " +
                "fits.\n\nIndex:\n" + string.Join("\n", lines.ToArray()) + "\n\nQuestion: " + query;
            Dictionary<string, object> v = Ollama.GenerateJson(url, icm.Config.DispatchModel(), prompt, schema, 0.1, DispatchTimeoutMs, cancel);
            string id = Json.GetStringOr(v, "entry_id", "none");
            return id == "none" ? null : id;
        }

        // --- Layer 3: capabilities (public wrappers so MCP and the flow engine reuse them) ---

        public string Ask(string query) { cancel = new Cancel(); return DoAsk(query); }
        public string RouteEntryId(string query) { cancel = new Cancel(); return Route(query); }

        // Constrained MULTI-pick: the model proposes up to maxK relevant entry ids (a JSON array
        // constrained to the manifest enum). The grounding step for generation that uses several
        // patterns/references at once. Stays on-thesis: the model proposes from a fixed set, the host
        // reads what it picked.
        public List<string> RouteMany(string query, int maxK) { cancel = new Cancel(); return RouteManyImpl(query, maxK); }

        private List<string> RouteManyImpl(string query, int maxK)
        {
            var ids = new List<string>();
            if (icm.Manifest == null || icm.Manifest.Entries.Count == 0) return ids;
            if (maxK < 1) maxK = 1;

            var enumIds = new List<string>();
            var lines = new List<string>();
            foreach (Entry e in NarrowEntries(query, icm.Manifest.Entries, RouteManyCandidateK))
            {
                enumIds.Add(e.Id);
                string grp = e.Group.Length > 0 ? " (" + e.Group + ")" : "";
                string kw = e.Keywords.Count > 0 ? "  [keywords: " + string.Join(", ", e.Keywords.ToArray()) + "]" : "";
                lines.Add("- " + e.Id + grp + " : " + e.Title + " - " + e.Summary + kw);
            }

            object itemSchema = Json.EnumProp(enumIds);
            object schema = Json.Schema(Json.Obj("entry_ids", Json.Obj("type", "array", "items", itemSchema)), "entry_ids");
            string prompt =
                "Select EVERY KB entry whose content would help with the task below - the patterns, " +
                "references, or snippets you would ground on while writing the answer. Return up to " +
                maxK + " entry ids, most relevant first, as a JSON array. Return an empty array if none apply.\n\n" +
                "Index:\n" + string.Join("\n", lines.ToArray()) + "\n\nTask: " + query;
            Dictionary<string, object> v = Ollama.GenerateJson(url, icm.Config.DispatchModel(), prompt, schema, 0.1, DispatchTimeoutMs, cancel);
            foreach (object o in Json.GetArr(v, "entry_ids"))
            {
                if (o == null) continue;
                string s = o.ToString().Trim();
                if (s.Length == 0 || s == "none") continue;
                if (!ids.Contains(s)) ids.Add(s);
                if (ids.Count >= maxK) break;
            }
            return ids;
        }

        // Used by flow generate/answer nodes too. Streams to OnToken when a front end wired it, so a
        // flow's generation is visible live in the console. (All bundled flows' result key is the final
        // generated text, so the console's "already streamed, don't reprint" stays correct.)
        public string Generate(string prompt, double temperature)
        {
            cancel = new Cancel();
            return GenerateMaybeStream(prompt, temperature);
        }

        private string DoAsk(string query)
        {
            Status("route: picking a KB entry");
            string id = Route(query);
            if (id == null) return "That isn't covered in this ICM's knowledge base.";
            Status("read: kb entry '" + id + "'");
            string entry = icm.ReadEntry(id);
            string system;
            try { system = icm.ReadFile(Conventions.SystemFile); } catch (IcmError) { system = ""; }
            string prompt =
                system + "\n\nAnswer the question using ONLY the entry text below. If it does not contain " +
                "the answer, say so.\n\n--- ENTRY TEXT ---\n" + entry + "\n--- END ---\n\nQuestion: " + query;
            Status("answer: generating (grounded)");
            return GenerateMaybeStream(prompt, 0.2);
        }

        // Run the oracle on a table: against `tsv` if given, else samples/<table>.txt on disk.
        public ValidateResult Validate(string table, string tsv)
        {
            var res = new ValidateResult();
            res.Table = table;
            if (table.Length == 0) { res.Ok = false; res.Problems.Add(new Problem(0, "(table)", "no table name given")); return res; }
            TableSchema schema = TableSchema.Load(icm.SchemaPath(table));
            string data = tsv != null ? tsv : icm.ReadAt(icm.SamplePath(table));
            Status("oracle: validating '" + table + "' against its schema");
            res.Problems = Oracle.ValidateTsv(schema, data, Oracle.BuildRefs(icm));
            res.Ok = res.Problems.Count == 0;
            return res;
        }

        // --- propose: model proposes a table row, the oracle gates it, bounded repair fixes it ---

        private void DoPropose(string query, TurnResult r)
        {
            string table = PickTable(query);
            if (table == null) { r.Text = "No table schemas to propose into (need schemas/<table>.json)."; r.IsError = true; return; }
            ProposeResult pr = ProposeRow(table, query);
            r.Text = FormatPropose(pr);
            r.IsError = !pr.Ok;
            if (pr.Ok) { r.ProposedTable = pr.Table; r.ProposedRow = pr.Row; }
        }

        public ProposeResult ProposeRow(string table, string request)
        {
            cancel = new Cancel();
            var res = new ProposeResult();
            res.Table = table;

            TableSchema schema;
            try { schema = TableSchema.Load(icm.SchemaPath(table)); }
            catch (IcmError e) { res.Error = e.Message; return res; }

            string header = TableHeader(table);
            if (header == null)
            {
                var names = new List<string>();
                foreach (ColSpec c in schema.Columns) names.Add(c.Name);
                header = string.Join("\t", names.ToArray());
            }
            res.Header = header;
            string[] cols = header.Split('\t');

            var props = new Dictionary<string, object>();
            foreach (string c in cols) props[c] = Json.StrProp();
            object genSchema = Json.Schema(props, cols);

            string basePrompt =
                "You are proposing exactly ONE new row for the tab-separated table '" + table +
                "' in the ICM domain: " + icm.Config.Domain + ".\n" +
                "Columns in order and their constraints:\n" + DescribeColumns(cols, schema) +
                ExampleBlock(table) +
                "Rules: give a value for EVERY column; numbers must be PLAIN digits only (no commas, " +
                "units, quotes, or thousands separators) and within any stated range; booleans as 0 or 1; " +
                "enum columns must be EXACTLY one of the listed values.\n" +
                "Request: " + request + "\nReturn JSON with one field per column name.";

            string prompt = basePrompt;
            for (int attempt = 0; attempt <= MaxProposeRepairs; attempt++)
            {
                Status("propose: generating row (attempt " + (attempt + 1) + ")");
                Dictionary<string, object> v;
                try { v = Ollama.GenerateJson(url, icm.Config.Models.Generate, prompt, genSchema, attempt == 0 ? 0.2 : 0.3, GenTimeoutMs, cancel); }
                catch (IcmError e) { res.Error = e.Message; return res; }

                var cells = new List<string>();
                foreach (string c in cols)
                {
                    string val = Json.GetStringOr(v, c, "");
                    val = val.Replace('\t', ' ').Replace('\n', ' ').Replace('\r', ' ').Trim();
                    cells.Add(val);
                }
                string row = string.Join("\t", cells.ToArray());
                res.Row = row; res.Attempts = attempt + 1;

                List<Problem> problems = Oracle.ValidateTsv(schema, header + "\n" + row, null);
                bool headerBad = false;
                foreach (Problem p in problems) if (p.Row == 0) headerBad = true;
                if (headerBad)
                {
                    res.Problems = problems;
                    res.Error = "schema/header mismatch for '" + table + "' (a declared column is missing from the table header)";
                    return res;
                }
                if (problems.Count == 0) { res.Ok = true; Status("propose: PASS"); return res; }

                res.Problems = problems;
                Status("propose: FAIL (" + problems.Count + " problem(s)), repairing");
                if (attempt == MaxProposeRepairs) break;

                var sb = new StringBuilder();
                sb.Append(basePrompt);
                sb.Append("\n\nYour previous row FAILED validation:\n");
                foreach (Problem p in problems) sb.Append("  " + p.ToString() + "\n");
                sb.Append("Previous row (tab-separated): " + row + "\nReturn a corrected JSON row.");
                prompt = sb.ToString();
            }
            return res;
        }

        // Pick which table the request targets: the only schema, or a constrained model pick.
        private string PickTable(string query)
        {
            List<string> tables = SchemaTables();
            if (tables.Count == 0) return null;
            if (tables.Count == 1) return tables[0];
            object schema = Json.Schema(Json.Obj("table", Json.EnumProp(tables)), "table");
            string prompt = "Pick which table this request adds a row to.\nTables: " +
                string.Join(", ", tables.ToArray()) + "\nRequest: " + query;
            Dictionary<string, object> v = Ollama.GenerateJson(url, icm.Config.DispatchModel(), prompt, schema, 0.1, DispatchTimeoutMs, cancel);
            return Json.GetStringOr(v, "table", tables[0]);
        }

        public List<string> SchemaTables()
        {
            var outl = new List<string>();
            string dir = icm.SchemasDirAbs();
            try
            {
                if (System.IO.Directory.Exists(dir))
                    foreach (string f in System.IO.Directory.GetFiles(dir, "*.json"))
                        outl.Add(System.IO.Path.GetFileNameWithoutExtension(f));
            }
            catch (System.IO.IOException e) { Status("could not list schemas: " + e.Message); }
            return outl;
        }

        // The authoritative header is the first line of samples/<table>.txt; null if unavailable.
        private string TableHeader(string table)
        {
            try
            {
                List<string> lines = Tsv.NonEmptyLines(icm.ReadAt(icm.SamplePath(table)));
                return lines.Count > 0 ? lines[0] : null;
            }
            catch (IcmError) { return null; }
        }

        private string ExampleBlock(string table)
        {
            try
            {
                List<string> lines = Tsv.NonEmptyLines(icm.ReadAt(icm.SamplePath(table)));
                if (lines.Count <= 1) return "";
                var sb = new StringBuilder("Existing example rows (tab-separated):\n");
                for (int i = 1; i < lines.Count && i <= 2; i++) sb.Append(lines[i] + "\n");
                return sb.ToString();
            }
            catch (IcmError) { return ""; }
        }

        private static string DescribeColumns(string[] cols, TableSchema schema)
        {
            var byName = new Dictionary<string, ColSpec>();
            foreach (ColSpec c in schema.Columns) byName[c.Name] = c;
            var sb = new StringBuilder();
            foreach (string c in cols)
            {
                ColSpec cs;
                if (byName.TryGetValue(c, out cs))
                {
                    sb.Append("- " + c + ": " + cs.CType);
                    if (cs.Required) sb.Append(", required");
                    if (cs.Min.HasValue || cs.Max.HasValue)
                        sb.Append(", range " + (cs.Min.HasValue ? cs.Min.Value.ToString() : "*") + ".." + (cs.Max.HasValue ? cs.Max.Value.ToString() : "*"));
                    if (cs.Values.Count > 0) sb.Append(", one of: " + string.Join("|", cs.Values.ToArray()));
                    sb.Append("\n");
                }
                else sb.Append("- " + c + ": string (free text)\n");
            }
            return sb.ToString();
        }

        public static string FormatPropose(ProposeResult pr)
        {
            if (pr.Ok)
                return "PASS - proposed row for '" + pr.Table + "' (validated in " + pr.Attempts + " attempt(s)):\n" + pr.Row;
            if (pr.Error != null && pr.Problems.Count == 0)
                return "[error] " + pr.Error;
            var sb = new StringBuilder();
            sb.Append("FAIL - no valid row for '" + pr.Table + "' after " + pr.Attempts + " attempt(s).\n");
            if (pr.Error != null) sb.Append(pr.Error + "\n");
            sb.Append("Last row: " + pr.Row + "\n");
            foreach (Problem p in pr.Problems) sb.Append("  " + p.ToString() + "\n");
            return sb.ToString();
        }

        public string Help()
        {
            var sb = new StringBuilder();
            sb.Append("This is the " + icm.Config.Name + " operator console (" + icm.Config.Domain + ").\n");
            sb.Append("Just type to chat (ungrounded). Use slash commands to act.\n\n");
            sb.Append("Generic commands (the harness):\n");
            sb.Append("  /search [source] <query> grounded answer from a knowledge base (a KB name, a path, or the default; -r for raw hits)\n");
            sb.Append("  /route <request>         let the model pick the best flow (you confirm)\n");
            sb.Append("  /flow <name> [input]     run a flow by name\n");
            sb.Append("  /do <tool|command>       run a declared tool, or a shell command you type\n");
            sb.Append("  /propose <description>   propose a table row, oracle-validated\n");
            sb.Append("  /ws switch|create <name> switch or create the active workspace\n");
            sb.Append("  /flows                   list the instance's flows\n");
            sb.Append("  /note <text>  /notes     add to / show NOTES.md (session memory)\n");
            sb.Append("  /clear   /help   /quit\n");
            sb.Append("\nAppend ' > path' to save a command's output to a file.\n");
            sb.Append("Plain text is ungrounded chat; slashes are for commands.");
            return sb.ToString();
        }
    }
}
