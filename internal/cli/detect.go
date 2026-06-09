package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

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

	type entry struct {
		Language string `json:"language"`
		Title    string `json:"title"`
		Detected bool   `json:"detected"`
		Tool     string `json:"tool,omitempty"`
		Ready    bool   `json:"ready"`
	}
	var entries []entry
	for _, a := range lang.Registry {
		e := entry{Language: a.Name, Title: a.Title, Detected: a.Present(abs)}
		if e.Detected {
			if bin := firstFound(a.Doctor); bin != "" {
				e.Tool, e.Ready = bin, true
			}
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
				mark, status = "!", "detected, toolchain missing"
			}
		}
		fmt.Fprintf(stdout, "  %s %-22s %s\n", mark, e.Title, status)
	}
	if !any {
		fmt.Fprintln(stdout, "\nNo supported languages detected.")
	}
	return ExitOK
}

func firstFound(bins []string) string {
	for _, b := range bins {
		if _, err := exec.LookPath(b); err == nil {
			return b
		}
	}
	return ""
}
