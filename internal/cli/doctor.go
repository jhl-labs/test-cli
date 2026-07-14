package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jhl-labs/test-cli/internal/config"
	"github.com/jhl-labs/test-cli/internal/lang"
)

const capabilityProbeTimeout = 30 * time.Second

type toolchainState struct {
	Ready   bool
	Tool    string
	Version string
	Missing []string
}

// runDoctor verifies every capability used by each detected language's default
// command (or the executable of a configured override). It exits
// ExitEnvironment when CI cannot produce the promised test/coverage artifacts.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "check every supported language, not just detected ones")
	root, err := parseWithTarget(fs, args, ".")
	if err != nil {
		return ExitUsage
	}
	abs, _ := filepath.Abs(root)
	cfg, err := config.Load(abs)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}

	fmt.Fprintf(stdout, "test-cli doctor — %s\n\n", abs)
	missing := 0
	checked := 0
	for _, a := range lang.Registry {
		override := cfg.Commands[a.Name]
		detected := a.Present(abs) || containsString(cfg.Languages, a.Name) || len(override) > 0
		if !detected && !*all {
			continue
		}
		checked++
		state := inspectToolchain(abs, a, override)
		switch {
		case !detected:
			fmt.Fprintf(stdout, "  - %-22s not in project; %s\n", a.Title, readinessText(state))
		case state.Ready:
			fmt.Fprintf(stdout, "  ✓ %-22s %s\n", a.Title, readinessText(state))
		default:
			missing++
			fmt.Fprintf(stdout, "  ✗ %-22s missing: %s\n", a.Title, strings.Join(state.Missing, "; "))
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

func inspectToolchain(root string, a *lang.Adapter, override [][]string) toolchainState {
	probes := a.CapabilityProbes(root)
	if len(override) > 0 {
		probes = configuredCommandProbes(override, root)
	}
	state := toolchainState{}
	if len(probes) == 0 {
		state.Missing = []string{"no executable command configured"}
		return state
	}
	labels := make([]string, 0, len(probes))
	for _, probe := range probes {
		labels = append(labels, probe.Label)
		out, err := executeCapabilityProbe(root, probe)
		if err != nil {
			detail := diagnosticLine(out)
			if detail == "" {
				detail = err.Error()
			}
			state.Missing = append(state.Missing, probe.Label+" ("+detail+")")
			continue
		}
		if state.Version == "" {
			state.Version = informativeLine(out)
		}
	}
	state.Tool = strings.Join(labels, ", ")
	state.Ready = len(probes) > 0 && len(state.Missing) == 0
	return state
}

func configuredCommandProbes(commands [][]string, root string) []lang.Probe {
	seen := map[string]bool{}
	var probes []lang.Probe
	for _, argv := range commands {
		rendered := (lang.Command{Args: argv}).Render(filepath.Join(root, ".test-cli-doctor"), root)
		if len(rendered) == 0 || strings.TrimSpace(rendered[0]) == "" || seen[rendered[0]] {
			continue
		}
		seen[rendered[0]] = true
		probes = append(probes, lang.Probe{Label: "configured command " + rendered[0], Args: []string{rendered[0]}, ExecutableOnly: true})
	}
	return probes
}

func executeCapabilityProbe(root string, probe lang.Probe) (string, error) {
	if len(probe.Files) > 0 {
		return executeFileCapabilityProbe(root, probe)
	}
	if len(probe.Args) == 0 {
		return "", fmt.Errorf("empty capability probe")
	}
	if probe.ExecutableOnly {
		_, err := resolveExecutable(root, probe.Args[0])
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), capabilityProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, probe.Args[0], probe.Args[1:]...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timed out after %s", capabilityProbeTimeout)
	}
	if err != nil {
		return string(out), err
	}
	if probe.Contains != "" && !strings.Contains(string(out), probe.Contains) {
		return string(out), fmt.Errorf("required capability %q not reported", probe.Contains)
	}
	return string(out), nil
}

func executeFileCapabilityProbe(root string, probe lang.Probe) (string, error) {
	matched := false
	for _, pattern := range probe.Files {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			matched = true
			data, err := os.ReadFile(path)
			if err == nil && (probe.Contains == "" || strings.Contains(string(data), probe.Contains)) {
				return filepath.Base(path), nil
			}
		}
	}
	if !matched {
		return "", fmt.Errorf("configuration file not found")
	}
	return "", fmt.Errorf("required capability %q not configured", probe.Contains)
}

func resolveExecutable(root, name string) (string, error) {
	if filepath.IsAbs(name) {
		if info, err := os.Stat(name); err == nil && usableExecutable(info) {
			return name, nil
		}
		return "", fmt.Errorf("executable %q not found", name)
	}
	if strings.ContainsAny(name, `/\\`) {
		candidate := filepath.Join(root, filepath.FromSlash(name))
		if info, err := os.Stat(candidate); err == nil && usableExecutable(info) {
			return candidate, nil
		}
		return "", fmt.Errorf("executable %q not found", name)
	}
	return exec.LookPath(name)
}

func usableExecutable(info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

func readinessText(state toolchainState) string {
	if !state.Ready {
		if len(state.Missing) == 0 {
			return "unavailable"
		}
		return "unavailable (" + strings.Join(state.Missing, "; ") + ")"
	}
	if state.Version != "" {
		return state.Tool + " — " + state.Version
	}
	return state.Tool + " ready"
}

func informativeLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Trim(line, "-=") == "" {
			continue
		}
		return line
	}
	return ""
}

func diagnosticLine(s string) string {
	lines := strings.Split(s, "\n")
	for _, marker := range []string{"Cannot find module", "no such command", "not found", "Could not resolve", "required capability"} {
		for _, line := range lines {
			if strings.Contains(line, marker) {
				return strings.TrimSpace(line)
			}
		}
	}
	return informativeLine(s)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
