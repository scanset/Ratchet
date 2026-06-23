# Operating the console

`ratchet <dir>` opens the Ratchet console on a ratchet. You plan in plain language and act with slash
commands. The console never lets the weak local model decide what runs.

## How a turn is dispatched

- **Plain text** (no leading `/`) is **ungrounded chat** - the generate seat answers directly, with no
  knowledge base and no oracle. Use it to think out loud.
- **A line starting with `/`** is a command, dispatched **deterministically** by code (no classify
  step). An unrecognized `/command` prints a hint to type `/help`.

The model only chooses a workflow when you explicitly ask with `/route`, and even then a deterministic
gate keeps only a confident, on-list match and you confirm before it runs.

## Commands

| Command | What it does |
| --- | --- |
| (plain text) | ungrounded chat (generate seat; no grounding, no oracle) |
| `/search [source] <query>` | grounded answer from a knowledge base (a kb name, a path, or the default; `-r` for raw hits) |
| `/route <request>` | the model proposes a chain from the authored set; gate + `y/n` confirm, then run |
| `/flow <name> <input>` | run an action chain directly (`flows/<name>/chain.json`) |
| `/do <tool [arg]>` | run a declared tool by name |
| `/do <shell command>` | run a PowerShell command you paste; its output enters the session context |
| `/propose <description>` | propose a table row; the oracle gates it, with bounded repair |
| `/ws switch\|create <name>` | switch to / create the active workspace (the session focus) |
| `/flows` | list the ratchet's action chains (what `/route` can match) |
| `/note <text>` / `/notes` | append to / read session memory (`NOTES.md`) |
| `/help` | list commands; `/clear` reset history; `/quit` exit |

`/do` is the single "run something" verb: a first token that matches a declared tool runs that tool
(remaining text maps to its argument); otherwise the whole line runs as a PowerShell command. The
model never composes a `/do` command - you do.

## The conversational router (`/route`)

`/route <request>` matches your request against the authored chains: the embedder narrows the catalog,
the model proposes one (grammar-constrained to the real list), a deterministic gate keeps only an
on-list, confident pick, and it runs after a `y/n` confirm. `router.autorun` in `ratchet.json` sets the
mode: `confirm` (default), `on` (auto-run high-confidence), `off` (disabled). Nothing is improvised -
the model can only pick from chains that exist.

## Model seats

A turn uses up to two seats, set per ratchet in `ratchet.json`:

- **dispatch** - the small model for the one constrained route/classify call;
- **generate** - the model that writes text: ungrounded chat, the grounded `/search` answer, and every
  chain `generate` node.

They can be one model or two (a tiny dispatcher + a larger generator). Ungrounded chat and `ratchet gen`
hit the generate seat directly with **no oracle and no schema constraint**; the oracle and grammar
constraints apply only inside chains and the `/propose` path.

## Workspaces and session memory

`/ws create <name>` makes a workspace under `workspaces/` (or your configured `workspacesDir`) and
switches to it; `/ws switch <name>` activates an existing one. The active workspace is the **session
focus**: its path is injected into chains as `$workspace`, and a summary (name, `project.json`, file
list) is injected into chat. `/note <text>` and any saved output land in `NOTES.md`, which the console
reads back so a new session is not cold.

## The project lifecycle

The C# reference ratchet (`examples\dotnet`) turns the workspace into a buildable project. The
flow, using only generic verbs:

```
/do new_project Calc winforms                 # scaffold workspaces/Calc (a winforms project)
/ws switch Calc                               # make it the active workspace
/flow add_file src/Core/Tip.cs a static Tip(double bill, double pct) returning the tip
/flow edit_file src/Ui/MainForm.cs add bill + percent boxes and a Compute button that calls Tip
/do build_project Calc                        # (add_file/edit_file already build; this is a standalone build)
/do make_launcher Calc                        # writes workspaces/Calc/launch-Calc.cmd
```

`add_file` and `edit_file` are action chains: they generate the file, stage it, build the **whole**
project (the compile oracle), repair up to twice on failure, then record the change in the project's
on-disk memory (`project.json` / `PROJECT.md`). Each run leaves a step trace in `runs/<id>/`.

**Launching the result.** A locally-built `dist/<name>.exe` is unsigned, so Smart App Control blocks
running it directly. `/do make_launcher <name>` writes a `launch-<name>.cmd` into the workspace; run
that from your own terminal (or double-click it) and it loads the exe **in-memory** inside trusted
PowerShell - the SAC-safe path. Launching is a domain capability (the `make_launcher` tool), not a
host command, so it goes through the generic `/do`.

## One-shot (non-interactive)

Outside the console:

```
ratchet flow <dir> <name> [--ws <ws>] [input...]   run a chain; --ws sets the active $workspace
ratchet open <dir>                     load + summarize (resolved seats, Ollama URL, tools)
ratchet flows <dir>                    list the chains
ratchet validate-flow <dir> [name]     lint chain(s)
ratchet doctor <dir>                   preflight: validate the tools the ratchet declares it needs
ratchet gen <dir> <prompt...>          one raw generate call
```
