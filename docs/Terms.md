# Vocabulary

- **Ratchet** - the engine (`ratchet`). It runs ratchets.
- **a ratchet** - a self-contained unit you point the engine at: a directory with `ratchet.json`
  plus `flows/`, `tools/`, `kb/`. Run it with `ratchet <dir>`. Ready-made ratchets live in the
  companion [RatchetBox](https://github.com/scanset/RatchetBox) repo.
- **flows** - action chains, the LLM-native `make`: the model proposes into fixed slots and the Oracle
  verifies each step before it advances.
- **tools** - deterministic scripts/oracles a flow invokes (or you do, via `/do`).
- **the Oracle** - the deterministic check that gates each step (a compiler, parser, validator); its
  exit code accepts or rejects. A pass means "won't break," not "is correct."
- **Context Binding** - each chain node sees ONLY its declared, scoped inputs - a prior output, a fixed
  `ref`, or a `search` hit - never a cumulative chat tape.
- **kb** - indexed knowledge the model retrieves from, scoped per step (Context Binding).
- **workspaces** - the projects a ratchet builds; the active one is the session focus.
- **compose** - a flow that builds a multi-file program from a folder of `.spec` files: plan the units,
  then build each one in dependency order. See [Composition](concepts/composition.md).
- **a spec** - a `.spec` file: a short STRUCTURED PROMPT (`name`/`intent`/`behavior`/`constraints`)
  describing ONE unit. It is the input to `compose`, not a parsed format.
- **a unit** - one piece of a composed system (a data type, a component, or the entry); `compose` builds
  one per spec.
- **project_api** - a ratchet tool that emits the public API of the units already built, so the next unit
  calls them with the exact names and signatures (this is what closes multi-reference drift).
