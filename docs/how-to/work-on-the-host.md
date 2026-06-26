# Work on the host (the engine, `go_src/`)

The host is the domain-agnostic engine (`ratchet`), a single static Go binary. Edit it only for generic
*mechanism* - never domain logic. A new capability is almost always a chain or tool in a **ratchet**,
not a host change (see [Build a ratchet](build-a-ratchet.md) and the boundary rule in
[AGENTS.md](../../AGENTS.md)).

## Build & verify (after any `go_src/` change)

The repo-root `Makefile` is the entry point; it `cd`s into `go_src/` (the Go module) and writes binaries
to `bins/<os>-<arch>/`.

```
make build                                       # -> bins/<host-os>-<host-arch>/ratchet
make test                                        # the Go unit tests (go test ./...)
make vet                                         # go vet ./...
ratchet selftest                                 # deterministic core, model-free
ratchet validate-flow ../RatchetBox/Linux/go     # lint a ratchet's chains (clone RatchetBox alongside)
ratchet doctor ../RatchetBox/Linux/go            # preflight a ratchet's declared toolchain
make cross                                       # build every shipped target (linux/windows/darwin/freebsd)
```

`make smoke` runs the two model-free Linux smokes - `scripts/linux/project-smoke.sh` (the deterministic
CLI verbs) and `scripts/linux/mcp-smoke.sh` (the MCP handshake + a real `go_build` tool call). Only chat
/ generation / search need a running Ollama. The committed binaries under `bins/` are prebuilt - rebuild
(`make build`) after editing `go_src/` or your change won't show; the `ratchet` on your PATH is usually a
symlink to `bins/linux-amd64/ratchet`, so a rebuild updates it.

## Repo layout

Module `github.com/scanset/Ratchet`, rooted in `go_src/`. `internal/` is Go's enforced-private
boundary: these packages are the engine's implementation, not a public API.

```
go_src/cmd/ratchet/      entrypoint: os.Exit(cli.Run(...))
go_src/internal/
  conventions/           file/dir names + tool/action-kind constants
  jsonx/ pathutil/ meta/  dynamic-JSON helpers, path resolution, <!--icm--> metadata
  model/                 pure data: Config types live in config/; Chain, Manifest, TableSchema, Results
  config/ instance/      ratchet.json loader; the sandboxed instance (path-escape guard)
  oracle/                the deterministic TSV validator + namespace check + Tsv
  chain/                 ChainEngine (run loop) + ChainLint; the Generator interface (breaks the
                         dispatch<->chain cycle)
  ollama/                generate / stream / embed / tags, the token meter, the cancel handle
  search/                BM25 (Search), KbIndex (+ .index cache), Indexer, Embedder
  tool/                  ToolRunner (platform-aware exec) + Doctor
  dispatch/              the Dispatcher: slash commands, router gate, propose/repair, /search, /do, /ws
  mcp/                   the MCP server (tools/list + tools/call)
  cli/                   verb handlers (Run), the ConsoleChat REPL, the runtime selftest
  version/               build version string (ldflags-injectable)
csharp_src/              the ORIGINAL C# host, kept for reference (built by scripts/windows/build.ps1
                         -> bins/csharp/). Do not edit it for new behavior.
```

## Hard constraints (these bite)

- **Pure Go, `CGO_ENABLED=0`.** This is what lets one host cross-compile to every target with no C
  toolchains (`make cross`). Do not add a dependency that needs cgo.
- **No import cycles.** Go forbids them (the old C# host was one flat namespace). Shared types live in
  `internal/model`, constants in `internal/conventions`; leaf packages depend inward. When two packages
  would need each other (dispatch and chain), define an interface in one and have the other satisfy it
  (see `chain.Generator`).
- **Tool execution branches on the host OS.** `internal/tool/platform.go` selects the interpreter: a
  bare `.ps1` -> pwsh (or powershell on Windows), `.sh` -> bash/sh, `.py` -> python; `/do` runs through
  the host shell. A tool's `command` argv runs as-is. Go's `os/exec` takes argv directly, so there is no
  Windows command-line quoting to maintain.
- **Faithful-first port.** Behavior matches the C# original; the C# `SelfTest` cases are carried over as
  Go tests and as the in-process `ratchet selftest`. Keep generated files (manifest.json) byte-stable by
  using typed structs (field order) rather than maps (sorted keys).
- **Run `gofmt` and `go vet` clean** before you call it done (`make fmt`, `make vet`).

## Troubleshooting

- **`contacting Ollama ... (timeout?)`** - Ollama not running or models missing: `ollama pull
  qwen3-coder` + `ollama pull nomic-embed-text`, confirm with `ollama list`, or override `OLLAMA_URL`.
  On WSL reaching Ollama on the Windows host, `localhost` does not work - point `OLLAMA_URL` at the
  default gateway (`http://$(ip route show default | awk '{print $3}'):11434`). Verify the model-free
  core first with `ratchet selftest`.
- **Generated code won't build** - expected; the oracle catches it and the repair loop feeds the error
  back. An Oracle pass means "won't break," not "behavior-correct."
- **Edited `go_src/` but no change** - rebuild (`make build`); the `bins/` binaries are prebuilt.
- **`go: command not found`** - install the [Go toolchain](https://go.dev/dl) and put it on PATH (the
  Makefile uses `go`).
- **A target won't cross-compile** - confirm the build is still pure Go (`CGO_ENABLED=0`); a new cgo
  dependency breaks cross-compilation.
- **MCP client sees no tools / garbled handshake** - launch `ratchet mcp <dir>`; keep stdout for
  protocol only (logs go to stderr).
- **On Windows**, the Go build is a native exe - run `bins\windows-amd64\ratchet.exe` directly. If
  Smart App Control blocks the unsigned binary, build and use the legacy C# host (a managed assembly the
  launcher loads in-memory) - see [Build the legacy C# host](build-csharp-host.md).
