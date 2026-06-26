# Security & trust model

Ratchet (the `ratchet` host) is a local tool you point at an **instance** directory. Read this before
running an instance you did not write.

## The one rule: only open instances you trust

An instance's `tools/manifest.json` declares **command/script tools** - scripts and external programs
the host runs (with the instance folder as the working directory; a bare script is dispatched to its
interpreter by host OS - PowerShell on Windows, bash on Linux/macOS). Opening such an instance and using
a capability that invokes a tool **runs that code on your machine**. This is by design - it is how the
host does real work - but it means:

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

`/do <command>` executes a command **you paste** through the host's shell (PowerShell on Windows,
bash/sh on Linux/macOS), with your privileges. This is operator-authorized arbitrary execution: the
model never composes or triggers a `/do` command, and its output is captured into the session for you
to read - it is not handed back to the model to act on. Only paste commands you understand, same as any
shell.

## Windows: Smart App Control and built artifacts

On Windows the committed binaries are unsigned. The cross-platform Go build
(`bins\windows-amd64\ratchet.exe`) is a **native** exe - run it directly; if Smart App Control blocks
it, code-signing (or a SAC exception) is the fix. The legacy C# build is a **managed .NET assembly**
(`bins\csharp\ratchet.exe`), which the `run-cli.ps1` / `ratchet.cmd` launchers load in-memory inside
the Microsoft-signed PowerShell - SAC permits that, so it is the SAC-friendly Windows option (see
[Build the legacy C# host](docs/how-to/build-csharp-host.md)). Likewise, the `Windows/dotnet4-x`
ratchet builds **unsigned** app executables; its `make_launcher` tool writes a `.cmd` that runs them
the same in-memory way. This is your own local code on your own machine - do not disable SAC or weaken
code-integrity policy. (On Linux/macOS there is no such gate - the binary is a normal executable;
`chmod +x` if needed.)

## Ollama

Ollama is local and unauthenticated over plain HTTP. Keep it bound to localhost. Pointing `ollama_url`
at a remote host sends prompts unencrypted - only do so on a trusted network.

## Supply chain

The host is a single static Go binary built **only from the Go standard library** - it declares no
third-party modules (an empty dependency graph), so there is no package-manager dependency to
compromise. It has no runtime dependency but a local Ollama; the binary itself needs no runtime.
Prebuilt binaries are committed for convenience; you can rebuild them from source with `make build`
(needs the Go toolchain) and verify the deterministic core with `ratchet selftest`. The original C#
host is kept under `csharp_src/` for reference.

## Reporting a vulnerability

If you find a security issue in the host itself (not in an untrusted instance you chose to open),
please report it privately via the repository's GitHub Security Advisories ("Report a vulnerability"),
or open a minimal issue without exploit details and ask for a private channel. Please do not file
public exploit details before a fix is available.

## No warranty

This software is provided under the Apache License 2.0, "as is", without warranty of any kind. You are
responsible for what you run.
