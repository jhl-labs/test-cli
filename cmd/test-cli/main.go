// Command test-cli runs standardized tests and coverage across many languages
// and renders normalized reports. See `test-cli help` for usage.
package main

import (
	"os"

	"github.com/jhl-labs/test-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
