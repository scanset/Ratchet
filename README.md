<p align="center"><img src="ratchet.png" alt="Ratchet" width="320"></p>

# Ratchet

**Ratchet** is a harness for self-hosted language models. It binds context from a knowledge base and defined workflows to make it more reliable.
The result is a self-hosted model that gives grounded answers instead of guesses and runs multi-step workflows
it could not handle alone, all on your machine.

## What you get

`ratchet.exe` - the terminal console and CLI. Drive it yourself, or let a frontier model like Claude
drive it over **MCP over stdio**. Either way your self-hosted model does the generating, so when Claude drives
you spend its tokens on planning, not on writing the code or running the workflow.

Prebuilt binaries are included, so you can run without building anything.

**Windows only** Ratchet builds with the C# compiler that ships in-box with the .NET
Framework. 

## How it works

Ratchet connects to your self-hosted model through [Ollama](https://ollama.com). You work with three things:

- **Slash commands** drive the console: chat, search the knowledge base, run a tool, or run a flow.
- **Tools** are deterministic scripts the host runs: compile, parse, scaffold, build.
- **Flows** are recipes: a fixed chain of steps where the model fills one slot at a time and a tool
  checks each step before the next.

## Getting started

**1. Launch Ollama and pull your models.** Ratchet uses three model seats: **generation**, **dispatch**,
and **embedding**. We recommend:

```
ollama pull qwen3-coder        # generation and dispatch
ollama pull nomic-embed-text   # embedding
```

Set the seats per ratchet in `ratchet.json`, or override the URL with `OLLAMA_URL` (default
`http://localhost:11434`). `selftest`, `validate`, and script tools need no model.

**2. Verify the core** (no model):

```
.\ratchet.cmd selftest
```

**3. Get some ratchets.** Ratchet ships no bundled ratchets - clone the companion collection:

```
git clone https://github.com/CurtisSlone/RatchetBox
```

[RatchetBox](https://github.com/CurtisSlone/RatchetBox) holds ready-made ratchets: `dotnet4-x` (the C#
reference), `cpp` (C++/MSVC), and `template` (an empty, self-documented skeleton to copy).

**4. Open the console** on one (needs Ollama):

```
.\ratchet.cmd ..\RatchetBox\dotnet4-x      # or any path to a ratchet directory
```

**5. Use it.** Inside the console:
- Type plain text for ordinary (ungrounded) chat.
- `/search <question>` - a grounded answer from the knowledge base.
- `/flows` lists the action chains; `/help` lists every command.
- `/flow csharp a method that reverses a string` - generates C#, compiles it (the oracle), repairs.
- Build a runnable app: `/do new_project Calc winforms` → `/ws switch Calc` →
  `/flow add_file src/Core/Greeter.cs a Greeter class` → `/do make_launcher Calc`, then run the
  `launch-Calc.cmd` it writes.
- `/note <text>` jots session memory; `/route <request>` lets the model pick a chain (you confirm).

See **[Use the console](docs/how-to/use-the-console.md)** for the full command set and the project lifecycle. Prefer
one-shot? `.\ratchet.cmd flow ..\RatchetBox\dotnet4-x csharp "a string reverser"`.

**Want to see real input/output first?** The `dotnet4-x` ratchet's `Tests\` folder (in
[RatchetBox](https://github.com/CurtisSlone/RatchetBox)) holds transcripts of building projects with
this tool - each recording the exact commands sent, the code the self-hosted model generated, the build/oracle
results, and the per-turn self-hosted model token counts. It's the fastest way to understand what driving
Ratchet actually looks like.


## Quick reference

- **What can I type in the console?** See - [Use the console](docs/how-to/use-the-console.md).
- **How do I build my own ratchet?** See - [Build a ratchet](docs/how-to/build-a-ratchet.md).
- **How do I write a flow?** See - [Author flows](docs/how-to/author-flows.md).
- **How do I add a tool?** See - [Author tools](docs/how-to/author-tools.md).
- **How do I build a whole system from specs?** See - [Compose from specs](docs/how-to/compose-from-specs.md).
- **Why is multi-file composition reliable (and where does it break)?** See - [Composition](docs/concepts/composition.md).
- **How do I drive it from Claude over MCP?** See - [Drive over MCP](docs/how-to/drive-over-mcp.md).
- **How do I get a frontier model to write my prompts?** See - [Frontier prompting](docs/agents/frontier-prompting.md).
- **How does it actually work?** See - [Architecture](docs/concepts/architecture.md).
- **How does each step get just the right context?** See - [Context Binding](docs/concepts/context-binding.md).
- **What does a term mean?** See - [Vocabulary](docs/Terms.md).
- **Is it safe to open a ratchet I didn't write?** See - [Security](SECURITY.md).
- **I'm an AI agent. Where do I start?** See - [AGENTS.md](AGENTS.md).

## License

MIT - see [LICENSE](LICENSE).
