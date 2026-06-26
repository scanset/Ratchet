// Package version carries the build's version string. Override at build time with
// -ldflags "-X github.com/scanset/Ratchet/internal/version.Version=v0.2.0".
package version

// Version is the host version reported by `ratchet version` and the MCP serverInfo.
// "dev" until stamped by the release build.
var Version = "dev"
