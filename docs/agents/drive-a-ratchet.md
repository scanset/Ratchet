# Drive a ratchet (agent playbook)

You are an AI agent USING a ratchet to produce something - verified code, a built project, a grounded
answer. (To CHANGE the ratchet itself - its flows, tools, specs - see
[Iterate on a ratchet](iterate-on-a-ratchet.md).) This page is how to pick your seat and the loop.

## Pick your seat

| You are... | Use | What you can run |
|---|---|---|
| A cloud model with MCP wired up | [Drive over MCP](../how-to/drive-over-mcp.md) | the ratchet's **tools** - you orchestrate, calling compile / scaffold / build yourself |
| A browser cloud model, no setup | [Frontier prompting](frontier-prompting.md) | anything - you write `/flow` + `/do` commands a human pastes |
| At the console or a terminal yourself | the console, or `ratchet flow` | anything |

The split that matters: **MCP exposes tools, not flows.** Over MCP you ARE the chain - you call the
ratchet's tools and sequence them. To run the ratchet's *flows* (`add_file`, `compose`, and the like),
use the console - directly, or via the frontier-prompting hand-off.

## First: learn what THIS ratchet can do

Never guess capabilities - read them:

- `tools/manifest.json` - the tools and their arguments.
- `flows/*/chain.json` - the chains, by `summary`.
- `kb/manifest.json` - what you can `/search`.
- the ratchet's `Tests/` folder, if it has one - real transcripts of the exact command shapes.

From a terminal, `ratchet open <ratchet>` summarizes the resolved model seats, tools, and Ollama URL.

## The command vocabulary

- `/search [source] <query>` - a grounded answer from the knowledge base.
- `/flow <name> <input>` - run a chain (the model fills slots, the Oracle checks each step). CLI form:
  `ratchet flow <ratchet> <name> [--ws <p>] "<input>"`.
- `/do <tool> [arg]` - run a declared tool.
- `/ws switch|create <name>` - set the active workspace (the session focus, `$workspace`).
- `/route <request>` - let the local model pick a chain (you confirm before it runs).

## Build a project (the lifecycle)

The exact tool and flow names come from THIS ratchet's manifests; the shape is the same everywhere:

1. **Scaffold a workspace** - a `new_project`-style tool (`/do new_project <name>`), then
   `/ws switch <name>`.
2. **Add the code** - file by file with a write flow (`/flow add_file <path> <desc>`,
   `/flow edit_file <path> <desc>`), or all at once from specs with `compose`
   ([Compose a system](compose-a-system.md)).
3. **Build it** - a `build_project`-style tool (`/do build_project <name>`).
4. **Run it** - the ratchet's launcher tool if it has one (for example `make_launcher`), then run what it
   writes.

## The loop

1. Read the result and the run state in `<ratchet>/runs/<id>/` - each step's prompt, output, and the
   oracle's diagnostics.
2. If a step failed the Oracle, the chain already repaired up to its limit. If it still failed, fix the
   INPUT (a clearer request, a constraint the model kept missing) and re-run that step.
3. If it builds but does the wrong thing - the Oracle checks that the code links, not that it is correct -
   give one corrective prompt (`/flow edit_file <path> <what to fix>`). You are the behavior check.

## Keep it honest

- Only drive ratchets you trust: running a tool or flow runs that ratchet's scripts on the machine.
- The local model never picks actions; it fills slots. You (or your commands) pick the steps.
- "Oracle passed" means "won't break," not "is correct." Read the output before you trust it.
