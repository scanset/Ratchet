# The Gemini hack: let a frontier model write your prompts

Ratchet's thesis is "one engine, two callers": a capable frontier model plans, the local model
executes inside the Oracle's guardrails. [MCP](mcp.md) automates that. This is the **manual** version -
no setup, works with any browser-based frontier model (Gemini in Chrome, but also ChatGPT, Claude,
etc.): point the frontier model at Ratchet's own docs and have it author ready-to-paste prompts for
your local console.

## Why it works

A frontier model is great at "what's the workflow for X?" and terrible to run locally. The local model
is the opposite. Ratchet already splits those roles. Here you do the hand-off by copy-paste: the
frontier model reads how Ratchet works and emits `/flow`, `/do`, `/search` lines; you paste them into
`ratchet <dir>`; the local model fills the slots and the Oracle verifies. You get frontier-quality
planning with local, private, verified execution.

## Steps (Gemini in Chrome)

1. **Open the repo in Chrome.** Paste the absolute path of your Ratchet directory into the address bar
   as a file URL, e.g.:

   ```
   file:///C:/path/to/ratchet/
   ```

   Chrome shows a navigable listing. Open `ROBOTS.md` from it (or open the file directly).

2. **Have Gemini read the docs.** With the doc tab active, open Gemini in Chrome and ask it to read:
   - `ROBOTS.md` (the agent reference),
   - `docs/architecture.md`, `docs/authoring-flows.md`, `docs/authoring-tools.md`,
     `docs/context-binding.md`,
   - `examples/dotnet/Tests/WINFORMS_TEST_LOG.md` (real input/output on the current commands - the
     fastest way for it to learn the command shapes; see the per-file notes below, and treat
     `COMPLEX_TEST_LOG.md` as historical).

3. **Give it the ratchet's actual capabilities.** To write prompts for a specific ratchet, also open
   (or paste) its routing + capability files so Gemini knows what really exists:
   - `examples/dotnet/kb/manifest.json` (what the knowledge base covers),
   - `examples/dotnet/flows/*/chain.json` (the chains, by `summary`),
   - `examples/dotnet/tools/manifest.json` (the tools and their arguments).

4. **Ask it to write prompts for your goal.** Then paste its output into the Ratchet console.

## What each file gives a frontier model (and why)

A few files are enough for a frontier model to map your environment, language limits, and
process-driven architecture, then emit exact commands:

- **`examples/dotnet/Tests/WINFORMS_TEST_LOG.md`** - the skeleton key. Real console traces of the
  dispatcher in action: the exact inputs and outputs, which prove Ratchet keeps **no chat tape** -
  state lives on disk (`project.json` + the source tree + `runs/`), so each command is discrete and
  self-contained, and the model writes ordered commands rather than a conversation. It also shows the
  concrete **current** command syntax.
  > `COMPLEX_TEST_LOG.md` beside it is a **pre-rework** transcript - it shows the *retired* `/new`,
  > `/add`, `/build` verbs and an `out/` layout. Read it for the multi-file project shape, but use the
  > current verbs (below), not its commands.
- **`SYSTEM.md`** (reached via the ratchet's `kb/manifest.json`) - the absolute boundaries for code
  generation: pre-Roslyn **C# 5** (explicit bans on string interpolation, expression-bodied members,
  null-conditional `?.`, tuples). Tailor the `add_file`/`edit_file` prompt strings to avoid C# 6+ so
  the local `csc` oracle does not trip.
- **`STRUCTURE.md`** - the workspace layout: how the automation under `tools/` and the sequential
  orchestration chains under `flows/` align with the rest of the ratchet.
- **`flows/*/chain.json` + `tools/manifest.json`** - the chains and tools that actually exist, so the
  model writes commands that resolve instead of inventing them.

**The current "start a new app" command set** (use these, not the retired `/new`//`add`//`build`):

```
/do new_project <name> [console|winforms]
/ws switch <name>
/flow add_file <path> <description>
/flow edit_file <path> <description>
/do build_project <name>
/do make_launcher <name>      # then run the launch-<name>.cmd it writes
```

## The prompt to give Gemini

> You are helping me drive a local tool called Ratchet from its terminal console. I've shared its
> ROBOTS.md, its docs/, its Tests/ transcripts, and one ratchet's kb/flows/tools manifests. Using
> ONLY the chains and tools that actually exist in those manifests, write the exact console commands I
> should paste, in order, to accomplish: **<your goal>**. Prefer `/flow <name> <input>` for authored
> chains, `/do <tool> <arg>` for tools, and `/search <query>` for grounded lookups. For a buildable
> app, use the project lifecycle (`/do new_project ...`, `/ws switch ...`, `/flow add_file ...`,
> `/flow edit_file ...`, `/do make_launcher ...`). Keep each command on its own line, ready to paste.
> Don't invent commands, chains, or tools that aren't in the manifests.

## If Chrome can't read the local files

Some Chrome/Gemini configurations restrict reading `file://` pages. Fallback: just **paste the
contents** of `ROBOTS.md`, the relevant `docs/`, the Tests transcripts, and the ratchet manifests
into the chat, then give the prompt above. The hack is the docs + manifests as context; the browser is
just a convenient way to feed them.

## Notes

- This is read-only planning on the frontier side - the frontier model never touches your machine. It
  emits text; you decide what to paste; the Oracle still gates whatever runs.
- It generalizes: the same paste-the-docs trick works with any capable model. For an automated version
  where the frontier model calls Ratchet directly, use [MCP](mcp.md).
- Keep the ratchet manifests in front of it. Without them the frontier model will guess capabilities;
  with them it writes commands that actually exist.
