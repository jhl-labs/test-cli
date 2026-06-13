package cli

import (
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/jhl-labs/test-cli/internal/lang"
)

// runDoctor reports whether the toolchain for each detected language is present.
// It exits ExitEnvironment when a detected language has no usable toolchain, so
// CI can fail fast before attempting a run.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "check every supported language, not just detected ones")
	root, err := parseWithTarget(fs, args, ".")
	if err != nil {
		return ExitUsage
	}
	abs, _ := filepath.Abs(root)

	fmt.Fprintf(stdout, "test-cli doctor — %s\n\n", abs)
	missing := 0
	checked := 0
	for _, a := range lang.Registry {
		detected := a.Present(abs)
		if !detected && !*all {
			continue
		}
		checked++
		bin, ver := probe(a.Doctor)
		switch {
		case !detected:
			fmt.Fprintf(stdout, "  - %-22s not in project; %s\n", a.Title, toolStatus(bin, ver))
		case bin != "":
			fmt.Fprintf(stdout, "  ✓ %-22s %s\n", a.Title, toolStatus(bin, ver))
		default:
			missing++
			fmt.Fprintf(stdout, "  ✗ %-22s detected but no toolchain found (need one of: %v)\n", a.Title, a.Doctor)
		}
	}
	if checked == 0 {
		fmt.Fprintln(stdout, "  no supported languages detected (use --all to list every toolchain)")
	}
	if missing > 0 {
		fmt.Fprintf(stderr, "\n%d detected language(s) are missing a toolchain.\n", missing)
		return ExitEnvironment
	}
	fmt.Fprintln(stdout, "\nAll detected toolchains are available.")
	return ExitOK
}

func probe(bins []string) (bin, ver string) {
	for _, b := range bins {
		if p, err := exec.LookPath(b); err == nil {
			return b, probeVersion(p)
		}
	}
	return "", ""
}

func probeVersion(path string) string {
	for _, arg := range []string{"--version", "version", "-version"} {
		out, err := probeVersionCommand(path, arg)
		if err == nil && len(out) > 0 {
			return firstLine(string(out))
		}
	}
	return ""
}

func probeVersionCommand(path, arg string) ([]byte, error) {
	switch filepath.Base(path) {
	case "pytest":
		return exec.Command("pytest", arg).CombinedOutput()
	case "python3":
		return exec.Command("python3", arg).CombinedOutput()
	case "python":
		return exec.Command("python", arg).CombinedOutput()
	case "npx":
		return exec.Command("npx", arg).CombinedOutput()
	case "node":
		return exec.Command("node", arg).CombinedOutput()
	case "go":
		return exec.Command("go", arg).CombinedOutput()
	case "cargo":
		return exec.Command("cargo", arg).CombinedOutput()
	case "dotnet":
		return exec.Command("dotnet", arg).CombinedOutput()
	case "mvn":
		return exec.Command("mvn", arg).CombinedOutput()
	case "gradle":
		return exec.Command("gradle", arg).CombinedOutput()
	case "java":
		return exec.Command("java", arg).CombinedOutput()
	default:
		return nil, exec.ErrNotFound
	}
}

func toolStatus(bin, ver string) string {
	if bin == "" {
		return "no toolchain found"
	}
	if ver == "" {
		return bin + " present"
	}
	return ver
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}
