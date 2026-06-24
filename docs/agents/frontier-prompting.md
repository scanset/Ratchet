# Let a frontier model write your prompts (the Gemini hack)

You don't need MCP or any setup to get frontier-quality help driving Ratchet. The trick: show a capable
cloud model (Gemini in Chrome, ChatGPT, Claude - any browser model) how Ratchet works, and ask it to
write the exact console commands you should paste. It plans; your local model does the work; the Oracle
checks every step. You get the best of both, by copy-paste.

This is the manual cousin of [driving over MCP](../how-to/drive-over-mcp.md). MCP lets the cloud model
call the ratchet's tools directly; this page lets it write `/flow`, `/do`, and `/search` commands you run
in the console - which is how you reach the flows (`add_file`, `compose`). Same idea, you are just the wire.

## Why it works

A cloud model is great at "what's the workflow for X?" but you may not want to run it locally. Your local
model is the opposite: private and on your machine, but weaker at planning. Ratchet already splits those
jobs. Here you do the hand-off yourself - the cloud model reads how Ratchet works and writes the commands,
you paste them, and the local model fills the slots while the Oracle verifies. Frontier-quality planning,
local verified execution.

## Steps

1. **Show it the docs.** Open this repo's `AGENTS.md` and a couple of docs in your browser, or just paste
   their contents into the chat:
   - `AGENTS.md` - the agent's map of the project.
   - `docs/concepts/architecture.md` and `docs/concepts/context-binding.md` - how it works.
   - `docs/how-to/author-flows.md` and `docs/how-to/author-tools.md` - the flow and tool formats.
2. **Show it the ratchet you want to drive.** Point it at that ratchet's capability files so it only uses
   things that exist:
   - `kb/manifest.json` - what the knowledge base covers.
   - `flows/*/chain.json` - the chains (read each `summary`).
   - `tools/manifest.json` - the tools and their arguments.
   - the ratchet's `Tests/` folder, if it has one - real console transcripts are the fastest way for it to
     learn the exact command shapes.
3. **Ask for your commands.** Give it your goal and the prompt below, then paste its output into the
   console.

## What each file gives it (and why)

- **The `Tests/` transcripts** - real input and output. They show the current command syntax and prove
  Ratchet keeps no chat history: state lives on disk (`project.json`, the source tree, `runs/`), so each
  command stands on its own. This is the single best file for teaching command shapes.
- **The ratchet's `SYSTEM.md`** (if it has one) - the hard limits the oracle enforces, such as a language
  version. Tell the model to stay inside them so the build does not trip.
- **`flows/*/chain.json` + `tools/manifest.json`** - the chains and tools that actually exist, so the
  model writes commands that resolve instead of inventing them.

## The prompt to paste in

> You are helping me drive a local tool called Ratchet from its terminal console. I've shared its
> `AGENTS.md`, some of its docs, and one ratchet's `kb`/`flows`/`tools` manifests (plus any `Tests/`
> transcripts). Using ONLY the chains and tools that exist in those files, write the exact console
> commands I should paste, in order, to do this: **<your goal>**. Use `/flow <name> <input>` for chains,
> `/do <tool> <arg>` for tools, and `/search <query>` for grounded lookups. For a buildable app, follow
> the project lifecycle the ratchet defines (scaffold a project, add or edit files, build it). Put each
> command on its own line, ready to paste. Don't invent commands, chains, or tools that aren't in the
> files I shared.

## If your browser can't read local files

Some setups block reading `file://` pages. No problem - just paste the CONTENTS of `AGENTS.md`, the docs,
the manifests, and any `Tests/` transcripts into the chat, then give the prompt above. The trick is the
docs and manifests as context; the browser is only a convenient way to feed them.

## Good to know

- This is read-only on the cloud side. The cloud model never touches your machine: it writes text, you
  choose what to paste, and the Oracle still gates whatever runs.
- It works with any capable model, not just Gemini. For the automated version where the cloud model calls
  the ratchet directly, use [MCP](../how-to/drive-over-mcp.md).
- Keep the manifests in front of it. Without them it guesses what exists; with them it writes commands
  that actually resolve.
