// Package config loads optional project configuration from .test-cli.json (or
// .test-cli.yaml when it is actually JSON-compatible). Configuration is
// entirely optional: with no file present the CLI uses built-in defaults for
// every supported language. Keeping the format to stdlib JSON means the binary
// has zero third-party dependencies.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the on-disk project configuration.
type Config struct {
	// OutputDir is where reports are written (default: reports/test).
	OutputDir string `json:"outputDir"`
	// Formats is the default set of report formats.
	Formats []string `json:"formats"`
	// Languages restricts which detected languages are run. Empty means all
	// detected languages.
	Languages []string `json:"languages"`
	// FailUnder fails the run when total line coverage is below this percentage
	// (0 disables the gate).
	FailUnder float64 `json:"failUnder"`
	// FailQuality fails the run when the standardized QA score is below this
	// value (0 disables the gate).
	FailQuality int `json:"failQuality"`
	// Baseline is an optional report.json used for regression comparison.
	Baseline string `json:"baseline"`
	// FailOnRegression fails when an explicit baseline comparison regresses.
	FailOnRegression bool `json:"failOnRegression"`
	// History lists prior report.json files used for trend and flaky analysis.
	History []string `json:"history"`
	// FailOnFlaky fails when repeated pass/fail evidence identifies flaky tests.
	FailOnFlaky bool `json:"failOnFlaky"`
	// DiffBase is an optional Git ref used for changed-line coverage analysis.
	DiffBase string `json:"diffBase"`
	// FailDiffCoverage fails when changed executable-line coverage is below the
	// configured percentage (0 disables the gate).
	FailDiffCoverage float64 `json:"failDiffCoverage"`
	// RiskChurnDays sets the git history window (in days) used for the
	// risk-weighted coverage analysis (0 keeps the built-in 90-day default).
	RiskChurnDays int `json:"riskChurnDays"`
	// Commands overrides the default test command for a language, e.g.
	// {"typescript": [["npx","vitest","run","--coverage"]]}. Each command is an
	// argv slice; {out} and {root} placeholders are supported.
	Commands map[string][][]string `json:"commands"`
	// Path is the file this config was loaded from (empty if defaults).
	Path string `json:"-"`

	formatsExplicit bool
}

// Default returns the built-in configuration used when no file is present.
func Default() Config {
	return Config{
		OutputDir: "reports/test",
		Formats:   []string{"stdout", "json", "junit", "cobertura", "html"},
	}
}

// candidateNames are searched in order in each directory.
var candidateNames = []string{".test-cli.json", "test-cli.json", ".test-cli.yaml", ".test-cli.yml"}

// Load discovers and loads configuration starting at dir and walking up to the
// filesystem root. It returns the default config (with Path empty) when nothing
// is found.
func Load(dir string) (Config, error) {
	cfg := Default()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return cfg, err
	}
	for {
		for _, name := range candidateNames {
			p := filepath.Join(abs, name)
			data, readErr := os.ReadFile(p)
			if readErr == nil {
				loaded, err := parse(data)
				if err != nil {
					return cfg, fmt.Errorf("parse %s: %w", p, err)
				}
				merge(&cfg, loaded)
				cfg.Path = p
				return cfg, nil
			}
			if !errors.Is(readErr, os.ErrNotExist) {
				return cfg, fmt.Errorf("read %s: %w", p, readErr)
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return cfg, nil
		}
		abs = parent
	}
}

func parse(data []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Config{}, err
	}
	_, c.formatsExplicit = fields["formats"]
	return c, nil
}

// HasExplicitFormats reports whether the loaded file contained a formats key.
// CLI profiles use this to avoid replacing a project's deliberate selection.
func (c Config) HasExplicitFormats() bool { return c.formatsExplicit }

func merge(base *Config, override Config) {
	if override.OutputDir != "" {
		base.OutputDir = override.OutputDir
	}
	if len(override.Formats) > 0 {
		base.Formats = override.Formats
	}
	base.formatsExplicit = override.formatsExplicit
	if len(override.Languages) > 0 {
		base.Languages = override.Languages
	}
	if override.FailUnder > 0 {
		base.FailUnder = override.FailUnder
	}
	if override.FailQuality > 0 {
		base.FailQuality = override.FailQuality
	}
	if override.Baseline != "" {
		base.Baseline = override.Baseline
	}
	if override.FailOnRegression {
		base.FailOnRegression = true
	}
	if len(override.History) > 0 {
		base.History = override.History
	}
	if override.FailOnFlaky {
		base.FailOnFlaky = true
	}
	if override.DiffBase != "" {
		base.DiffBase = override.DiffBase
	}
	if override.FailDiffCoverage > 0 {
		base.FailDiffCoverage = override.FailDiffCoverage
	}
	if override.RiskChurnDays > 0 {
		base.RiskChurnDays = override.RiskChurnDays
	}
	if len(override.Commands) > 0 {
		base.Commands = override.Commands
	}
}
