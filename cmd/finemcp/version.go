package main

import (
	"runtime/debug"
	"strings"
)

// Build information. Populated at build-time via -ldflags by goreleaser.
// When installed via "go install", these fall back to values embedded by the
// Go toolchain in the binary's build info (module version, VCS metadata).
var (
	// Version is the semantic version (e.g. "v0.1.0").
	Version = "dev"
	// Commit is the short git commit SHA.
	Commit = "unknown"
	// Date is the build timestamp in RFC3339 format.
	Date = "unknown"
	// BuiltBy indicates what built this binary.
	BuiltBy = "go install"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	// Only fill in from build info when ldflags were not injected.
	if Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}

	// Walk VCS settings embedded by the Go toolchain.
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "unknown" && len(s.Value) >= 7 {
				Commit = s.Value[:7]
			}
		case "vcs.time":
			if Date == "unknown" {
				Date = strings.TrimSuffix(s.Value, "Z") + "Z" // normalise
			}
		}
	}
}
