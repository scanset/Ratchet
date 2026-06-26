// Command ratchet is the cross-platform Ratchet host: the domain-agnostic engine that runs a
// self-hosted model as a constrained proposer behind a deterministic oracle. All domain logic lives
// in external ratchets it loads; this binary is the harness. Go port of the original C# host
// (preserved under src.bak/). The verb surface lives in internal/cli.
package main

import (
	"os"

	"github.com/scanset/Ratchet/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
