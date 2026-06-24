# Security & trust model

Ratchet (the `ratchet` host) is a local tool you point at an **instance** directory. Read this before
running an instance you did not write.

## The one rule: only open instances you trust

An instance's `tools/manifest.json` declares **command/script tools** - PowerShell scripts and
external programs the host runs (with the instance folder as the working directory). Opening such an
instance and using a capability that invokes a tool **runs that code on your machine**. This is by
design - it is how the host does real work - but it means:

> Treat opening an instance like running its scripts, because that is what it is. Only open instances
> from a source you trust, and skim `ratchet.json` + the `tools/` folder first.

This applies equally when a **frontier model drives the host over MCP**: a client calling `tools/call`
runs the instance's declared scripts. The instance's tools define the blast radius - not the model.
The model fills arguments into authored commands; it cannot invent new ones.

## What limits the damage

- **The local model never picks or runs tools on its own.** It only proposes into constrained slots
  (an enum, a row, a draft); a deterministic Oracle or an authored chain decides what runs. There is
  no open tool-calling loop for the local model.
- **The action graph is closed and authored.** A chain's nodes and edges are fixed on disk and
  lint-checked; at a model-chosen branch the model picks from a declared enum and nothing else. It
  navigates a graph it cannot modify, so even a confused or adversarially-prompted model can only do
  what the authored chains and tools already permit (bounded nondeterminism).
- **Tools are declared, not invented.** A tool's command line is authored in `tools/manifest.json`;
  the model only fills declared arguments. Arguments are passed as an argv array (not a shell string),
  so argument values present no shell-injection surface.
- **File I/O is sandboxed to the instance root.** The host's reads/writes resolve through a
  path-escape guard that rejects absolute paths and `..`, so its own file operations cannot wander
  outside the opened folder. (This guards the *host's* I/O. A declared external tool is a separate
  process bounded only by OS permissions - see the trust rule above.)
- **No network egress except Ollama.** The host talks only to the configured `ollama_url` (default
  `http://localhost:11434`, local plaintext HTTP). It does not phone home. A declared tool may reach
  the network when you run it (e.g. a docs-fetching build script) - that is the tool's traffic, on
  demand, and visible in its command.

## `/do` runs what you type

`/do <command>` executes a PowerShell command **you paste**, with your privileges. This is
operator-authorized arbitrary execution: the model never composes or triggers a `/do` command, and its
output is captured into the session for you to read - it is not handed back to the model to act on.
Only paste commands you understand, same as any shell.

## Smart App Control and built artifacts

The committed `ratchet.exe` is unsigned, so Smart App Control blocks running it
directly. The `.cmd` / `run-cli.ps1` launchers load the program's bytes in-memory inside the
Microsoft-signed PowerShell, which SAC permits. Likewise, a C# instance builds **unsigned** app
executables; the `make_launcher` tool writes a `.cmd` that runs them the same in-memory way. This is
your own local code on your own machine - use the launchers; do not disable SAC or weaken
code-integrity policy to run it. SAC still guards everything else.

## Ollama

Ollama is local and unauthenticated over plain HTTP. Keep it bound to localhost. Pointing `ollama_url`
at a remote host sends prompts unencrypted - only do so on a trusted network.

## Supply chain

The host builds with the in-box .NET Framework C# compiler - **no SDK, NuGet, or MSBuild** - so there
is no package-manager dependency to compromise. Its only runtime dependencies are the .NET Framework
(already on Windows) and a local Ollama. Prebuilt binaries are committed for convenience; you can
rebuild them from source with `build.ps1` and verify the deterministic core with
`.\ratchet.cmd selftest`.

## Reporting a vulnerability

If you find a security issue in the host itself (not in an untrusted instance you chose to open),
please report it privately via the repository's GitHub Security Advisories ("Report a vulnerability"),
or open a minimal issue without exploit details and ask for a private channel. Please do not file
public exploit details before a fix is available.

## No warranty

This software is provided under the MIT License, "as is", without warranty of any kind. You are
responsible for what you run.
