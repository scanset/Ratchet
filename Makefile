# Ratchet (Go) — build, cross-compile, test.
#
# The Go sources live under go_src/ (the Go module root, sibling of csharp_src/). Recipes cd into it
# and write outputs to the repo-root bins/<os>-<arch>/. Cross-compilation is pure-Go (CGO_ENABLED=0):
# one host builds every target, no C toolchains. See PLANS.md.

GO      ?= go
SRC     := go_src
# PKG is relative to $(SRC); BINROOT (outputs) lands at the repo root, beside go_src/.
# NOTE: no trailing inline comments on these - Make would capture the spaces into the value.
PKG     := ./cmd/ratchet
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/scanset/Ratchet/internal/version.Version=$(VERSION)
GOFLAGS := -trimpath
BIN     := ratchet
BINROOT := $(CURDIR)/bins

# Targets to ship. Add/remove freely; `make list-targets` shows all combos.
PLATFORMS := \
	linux/amd64 linux/arm64 linux/arm linux/386 linux/riscv64 linux/ppc64le linux/s390x \
	windows/amd64 windows/arm64 windows/386 \
	darwin/amd64 darwin/arm64 \
	freebsd/amd64 freebsd/arm64

.PHONY: all build cross test vet fmt tidy clean smoke list-targets

## build: native build into bins/<host-os>-<host-arch>/
build:
	@os=$$(cd $(SRC) && $(GO) env GOOS); arch=$$(cd $(SRC) && $(GO) env GOARCH); \
	out="$(BINROOT)/$$os-$$arch/$(BIN)"; [ "$$os" = "windows" ] && out="$$out.exe"; \
	mkdir -p "$$(dirname "$$out")"; \
	( cd $(SRC) && CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o "$$out" $(PKG) ); \
	echo "built $$out"

## cross: build every PLATFORMS target into bins/<os>-<arch>/
cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out="$(BINROOT)/$$os-$$arch/$(BIN)"; [ "$$os" = "windows" ] && out="$$out.exe"; \
		mkdir -p "$$(dirname "$$out")"; \
		( cd $(SRC) && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o "$$out" $(PKG) ) \
			&& echo "  ok  $$out" || echo "  FAIL $$p"; \
	done

## test: run the test suite (the deterministic-core self tests live here as the port lands)
test:
	cd $(SRC) && CGO_ENABLED=0 $(GO) test ./...

## smoke: model-free Linux smoke tests against the Go ratchet (needs the binary built + go on PATH)
smoke: build
	./scripts/linux/project-smoke.sh
	./scripts/linux/mcp-smoke.sh

vet:
	cd $(SRC) && $(GO) vet ./...

fmt:
	cd $(SRC) && $(GO) fmt ./...

tidy:
	cd $(SRC) && $(GO) mod tidy

## clean: remove built binaries but keep the committed bins/ tree (.gitkeep placeholders)
clean:
	find bins -type f ! -name '.gitkeep' -delete

list-targets:
	@cd $(SRC) && $(GO) tool dist list
