package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jhl-labs/test-cli/internal/config"
	"github.com/jhl-labs/test-cli/internal/lang"
)

func runDetect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
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

	type entry struct {
		Language string   `json:"language"`
		Title    string   `json:"title"`
		Detected bool     `json:"detected"`
		Tool     string   `json:"tool,omitempty"`
		Ready    bool     `json:"ready"`
		Issues   []string `json:"issues,omitempty"`
	}
	var entries []entry
	for _, a := range lang.Registry {
		override := cfg.Commands[a.Name]
		e := entry{Language: a.Name, Title: a.Title, Detected: a.Present(abs) || containsString(cfg.Languages, a.Name) || len(override) > 0}
		if e.Detected {
			state := inspectToolchain(abs, a, override)
			e.Tool, e.Ready, e.Issues = state.Tool, state.Ready, state.Missing
		}
		entries = append(entries, e)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"root": abs, "languages": entries})
		return ExitOK
	}

	fmt.Fprintf(stdout, "root: %s\n\n", abs)
	any := false
	for _, e := range entries {
		mark := "—"
		status := "not detected"
		if e.Detected {
			any = true
			if e.Ready {
				mark, status = "✓", "ready ("+e.Tool+")"
			} else {
				mark, status = "!", "detected, missing "+strings.Join(e.Issues, "; ")
			}
		}
		fmt.Fprintf(stdout, "  %s %-22s %s\n", mark, e.Title, status)
	}
	if !any {
		fmt.Fprintln(stdout, "\nNo supported languages detected.")
	}
	return ExitOK
}
