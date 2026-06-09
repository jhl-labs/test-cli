// Package version exposes build metadata injected at link time via -ldflags.
package version

import "fmt"

// These values are overridden at build time with:
//
//	-X github.com/jhl-labs/test-cli/internal/version.Version=v1.2.3
//	-X github.com/jhl-labs/test-cli/internal/version.Commit=abc1234
//	-X github.com/jhl-labs/test-cli/internal/version.Date=2026-06-09T00:00:00Z
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Short returns just the semantic version, e.g. "v1.2.3" or "dev".
func Short() string { return Version }

// Long returns the version with the embedded commit, e.g. "v1.2.3 (abc1234)".
func Long() string {
	if Commit == "" || Commit == "unknown" {
		return Version
	}
	return fmt.Sprintf("%s (%s)", Version, Commit)
}

// String returns the full human-readable build banner.
func String() string {
	return fmt.Sprintf("test-cli %s\ncommit: %s\nbuilt:  %s", Version, Commit, Date)
}
