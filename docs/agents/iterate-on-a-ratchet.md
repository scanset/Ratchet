# Iterate on a ratchet (agent playbook)

You are an AI agent changing a ratchet - its flows, tools, specs, or kb. A ratchet is data the engine
loads; new flows and tools are picked up at runtime, so you almost never rebuild the engine. This page is
the loop and the index to the task playbooks.

## The loop

1. **Orient.** Read what exists, not what you assume:
   - `<ratchet>/flows/*/chain.json` (the chains, by `summary`),
   - `<ratchet>/tools/manifest.json` (the tools + their args),
   - `<ratchet>/kb/manifest.json` (the kb topics).
2. **Change one thing** - a node, a prompt, a tool, a spec.
3. **Verify** (commands below). Run it; never assume.
4. **Read the run state.** `<ratchet>/runs/<id>/` holds each step's prompt + output + the oracle's
   diagnostics - this is how you see what the model actually got and returned.
5. **Fix and repeat from 3.**

## Verify commands (terminal)

```
ratchet validate-flow <ratchet>            # lint every chain (kinds, fields, unknown tools, reachability)
ratchet validate-flow <ratchet> <name>     # lint one chain
ratchet doctor <ratchet>                    # preflight the declared toolchain (requirements)
ratchet flow <ratchet> <chain> --ws <p> ""  # run a chain end to end
```

`validate-flow` and `doctor` need no model; running a flow needs Ollama. Tools have no CLI verb - run them
from the console with `/do <tool> [arg]`, or run `tools/<name>.ps1` from the ratchet root.

## Pick your task

| Task | Playbook |
|---|---|
| Add or change a flow (chain) | [Edit a flow](edit-a-flow.md) |
| Add or change a tool | [Edit a tool](edit-a-tool.md) |
| Build a system from specs (compose) | [Compose a system](compose-a-system.md) |
| Drive the ratchet to build something (use it, not change it) | [Drive a ratchet](drive-a-ratchet.md) |

Format references (the contracts): [Author flows](../how-to/author-flows.md),
[Author tools](../how-to/author-tools.md), [Compose from specs](../how-to/compose-from-specs.md).

## Start a new ratchet

Copy `RatchetBox/template` - it ships the lifecycle (`add_file`/`edit_file`) and composition
(`compose`/`add_unit`) flows working. Then: set `name`/`domain`/`models` in `ratchet.json`, implement the
`CHANGE_ME` tools (`build_project`, `new_project`, `project_api`), fill the `<LANGUAGE>` placeholders in
the prompts, and run `validate-flow` + `doctor`. Reference build:
[Build a ratchet](../how-to/build-a-ratchet.md).

## The one rule

Keep domain logic in the ratchet, never in the engine (`src/`). A new capability is a new flow or tool,
not a host change. See [AGENTS.md](../../AGENTS.md).
