// Package cli implements the command-line interface. Dispatch is a simple
// switch (no third-party framework), mirroring the conventions of the sibling
// security-cli so the two tools feel identical to operators and AI agents.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jhl-labs/test-cli/internal/version"
)

// Exit codes. These are a stable contract for CI and AI agents.
const (
	ExitOK          = 0 // success, tests green, coverage gate satisfied
	ExitTestFailure = 1 // tests failed or coverage below --fail-under
	ExitUsage       = 2 // bad arguments
	ExitRunFailure  = 3 // could not run/parse (no artifacts, parse error)
	ExitEnvironment = 4 // missing toolchain / environment problem
)

// Run is the entry point. It returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return ExitOK
	}

	switch args[0] {
	case "-h", "--help", "help":
		printHelp(stdout)
		return ExitOK
	case "-v", "--version", "version":
		fmt.Fprintln(stdout, version.String())
		return ExitOK
	case "run", "scan", "diagnose":
		// scan/diagnose are accepted aliases so the GitHub Action's default
		// `diagnose` command works unchanged.
		return runRun(args[1:], stdout, stderr)
	case "ingest":
		return runIngest(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "detect":
		return runDetect(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "generate-skill":
		return runGenerateSkill(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "test-cli: unknown command %q\n\n", args[0])
		printHelp(stderr)
		return ExitUsage
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, strings.TrimLeft(`
test-cli `+version.Short()+` — standardized test & coverage reports across languages

USAGE
  test-cli <command> [target] [flags]

COMMANDS
  run             Detect languages, run tests with coverage, write reports
  ingest          Build reports from existing artifacts (JUnit/Cobertura/LCOV/JaCoCo/go)
  report          Re-render reports from a previously generated report.json
  detect          Report which languages/toolchains are detected
  doctor          Check that required test toolchains are installed
  generate-skill  Emit an AI-agent skill describing how to drive test-cli
  version         Print version information
  help            Show this help

COMMON FLAGS
  -o, --output-dir DIR   Output directory for reports (default reports/test)
      --format FORMAT     Report format, repeatable: json,junit,cobertura,markdown,html,stdout
      --lang LANG         Restrict to a language, repeatable: python,typescript,go,rust,csharp,java
      --profile NAME      Preset: default | ci | release
      --fail-under PCT     Fail if total line coverage < PCT
      --no-run            Do not execute tests; only ingest existing artifacts
      --quiet             Suppress progress logging

EXIT CODES
  0 success   1 test failure / coverage gate   2 usage   3 run failure   4 environment

EXAMPLES
  test-cli run . --format stdout,json,html
  test-cli run . --lang go --fail-under 80
  test-cli ingest --tests junit.xml --coverage coverage.xml -o reports/test
  test-cli generate-skill --out .claude/skills

Supported languages: python, typescript, go, rust, csharp, java
`, "\n"))
}
