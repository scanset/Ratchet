<p align="center"><img src="ratchet.png" alt="Ratchet" width="320"></p>

# Ratchet

**Ratchet** is a harness for self-hosted language models. It binds context from a knowledge base and
defined workflows to make it more reliable. The result is a self-hosted model that gives grounded
answers instead of guesses and runs multi-step workflows it could not handle alone, all on your
machine.

## What you get

`ratchet` - the terminal console and CLI. Drive it yourself, or let a frontier model like Claude drive
it over **MCP over stdio**. Either way your self-hosted model does the generating, so when Claude
drives you spend its tokens on planning, not on writing the code or running the workflow.

**Cross-platform.** Ratchet is a single static Go binary - no runtime, no dependencies. Prebuilt
binaries for every platform are committed under [`bins/`](bins/) (`linux-amd64`, `linux-arm64`,
`windows-amd64`, `darwin-arm64`, and more), so you can run without building anything. Build from
source with `make build` (needs the [Go toolchain](https://go.dev/dl)); `make cross` builds all
targets at once.

## How it works

Ratchet connects to your self-hosted model through [Ollama](https://ollama.com). You work with three
things:

- **Slash commands** drive the console: chat, search the knowledge base, run a tool, or run a flow.
- **Tools** are deterministic scripts the host runs: compile, parse, scaffold, build. The host picks
  the interpreter per OS (PowerShell on Windows, bash on Linux/macOS), so a ratchet runs wherever its
  declared tools exist.
- **Flows** are recipes: a fixed chain of steps where the model fills one slot at a time and a tool
  checks each step before the next.

## Getting started

**1. Get the binary.** Use the prebuilt one for your platform from [`bins/`](bins/), or build it:

```
make build            # -> bins/<os>-<arch>/ratchet  (needs the Go toolchain)
```

Put it on your PATH if you like (`ln -s "$PWD/bins/linux-amd64/ratchet" ~/.local/bin/ratchet`), or run
it by path. On Windows, run `bins\windows-amd64\ratchet.exe` directly; if Smart App Control blocks the
unsigned binary, build the legacy C# host instead - a managed .NET assembly the launcher loads
in-memory (see [Build the legacy C# host](docs/how-to/build-csharp-host.md)).

**2. Launch Ollama and pull your models.** Ratchet uses three model seats: **generation**,
**dispatch**, and **embedding**. We recommend:

```
ollama pull qwen3-coder        # generation and dispatch
ollama pull nomic-embed-text   # embedding
```

Set the seats per ratchet in `ratchet.json`, or override the URL with `OLLAMA_URL` (default
`http://localhost:11434`). `selftest`, `validate`, and script tools need no model.

> **On WSL talking to Ollama on the Windows host:** `localhost` does not reach it; the host is the
> default gateway. Set `export OLLAMA_URL="http://$(ip route show default | awk '{print $3}'):11434"`.

**3. Verify the core** (no model):

```
ratchet selftest
```

**4. Get some ratchets.** Ratchet ships no bundled ratchets - clone the companion collection:

```
git clone https://github.com/scanset/RatchetBox
```

[RatchetBox](https://github.com/scanset/RatchetBox) holds ready-made ratchets, grouped by the
platform their toolchain targets: `Linux/go` (Go, verified with `go build` - the cross-platform
reference), and under `Windows/`: `dotnet4-x` (C# / in-box csc), `cpp` (C++ / MSVC), and `template`
(an empty, self-documented skeleton to copy).

**5. Open the console** on one (needs Ollama):

```
ratchet ../RatchetBox/Linux/go            # or any path to a ratchet directory
```

**6. Use it.** Inside the console:
- Type plain text for ordinary (ungrounded) chat.
- `/search <question>` - a grounded answer from the knowledge base.
- `/flows` lists the action chains; `/help` lists every command.
- `/flow go a function that reverses a UTF-8 string` - generates Go, verifies it with `go build` (the
  oracle), repairs once if it fails.
- `/note <text>` jots session memory; `/route <request>` lets the model pick a chain (you confirm).

Prefer one-shot? `ratchet flow ../RatchetBox/Linux/go go "a string reverser"`.

On Windows, the `Windows/dotnet4-x` ratchet adds a full app lifecycle - e.g. `/do new_project Calc
winforms` -> `/ws switch Calc` -> `/flow add_file src/Core/Greeter.cs a Greeter class` -> `/do
make_launcher Calc` builds a runnable WinForms app. See **[Use the console](docs/how-to/use-the-console.md)**
for the full command set and lifecycle.

**Want to see real input/output first?** `Windows/dotnet4-x` and `Windows/cpp` carry a `transcripts/`
folder (in [RatchetBox](https://github.com/scanset/RatchetBox)) with end-to-end build transcripts -
the exact commands sent, the code the self-hosted model generated, the build/oracle results, and the
per-turn token counts. The fastest way to understand what driving Ratchet looks like.

## Quick reference

- **What can I type in the console?** See - [Use the console](docs/how-to/use-the-console.md).
- **How do I build my own ratchet?** See - [Build a ratchet](docs/how-to/build-a-ratchet.md).
- **How do I write a flow?** See - [Author flows](docs/how-to/author-flows.md).
- **How do I add a tool?** See - [Author tools](docs/how-to/author-tools.md).
- **How do the manifests (tools / flows / kb) get built?** See - [Build the manifests](docs/how-to/build-manifests.md).
- **How does the knowledge base work, and how do I build one?** See - [Knowledge bases](docs/how-to/knowledge-bases.md).
- **How do I build a whole system from specs?** See - [Compose from specs](docs/how-to/compose-from-specs.md).
- **Why is multi-file composition reliable (and where does it break)?** See - [Composition](docs/concepts/composition.md).
- **How do I drive it from Claude over MCP?** See - [Drive over MCP](docs/how-to/drive-over-mcp.md).
- **How do I get a frontier model to write my prompts?** See - [Frontier prompting](docs/agents/frontier-prompting.md).
- **How does it actually work?** See - [Architecture](docs/concepts/architecture.md).
- **How does each step get just the right context?** See - [Context Binding](docs/concepts/context-binding.md).
- **How is the engine built and laid out?** See - [Work on the host](docs/how-to/work-on-the-host.md).
- **How do I build the original Windows C# host?** See - [Build the legacy C# host](docs/how-to/build-csharp-host.md).
- **What does a term mean?** See - [Vocabulary](docs/Terms.md).
- **Is it safe to open a ratchet I didn't write?** See - [Security](SECURITY.md).
- **I'm an AI agent. Where do I start?** See - [AGENTS.md](AGENTS.md).

## License

Apache 2.0 - see [LICENSE](LICENSE) and [NOTICE](NOTICE).
