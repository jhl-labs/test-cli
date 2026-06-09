// Package config loads optional project configuration from .test-cli.json (or
// .test-cli.yaml when it is actually JSON-compatible). Configuration is
// entirely optional: with no file present the CLI uses built-in defaults for
// every supported language. Keeping the format to stdlib JSON means the binary
// has zero third-party dependencies.
package config

import (
	"bytes"
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
	// Commands overrides the default test command for a language, e.g.
	// {"typescript": [["npx","vitest","run","--coverage"]]}. Each command is an
	// argv slice; {out} and {root} placeholders are supported.
	Commands map[string][][]string `json:"commands"`
	// Path is the file this config was loaded from (empty if defaults).
	Path string `json:"-"`
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
			if data, err := os.ReadFile(p); err == nil {
				loaded, err := parse(data)
				if err != nil {
					return cfg, fmt.Errorf("parse %s: %w", p, err)
				}
				merge(&cfg, loaded)
				cfg.Path = p
				return cfg, nil
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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		// Retry leniently (unknown fields are tolerated for forward-compat).
		var c2 Config
		if err2 := json.Unmarshal(data, &c2); err2 != nil {
			return Config{}, errors.Join(err, err2)
		}
		return c2, nil
	}
	return c, nil
}

func merge(base *Config, override Config) {
	if override.OutputDir != "" {
		base.OutputDir = override.OutputDir
	}
	if len(override.Formats) > 0 {
		base.Formats = override.Formats
	}
	if len(override.Languages) > 0 {
		base.Languages = override.Languages
	}
	if override.FailUnder > 0 {
		base.FailUnder = override.FailUnder
	}
	if len(override.Commands) > 0 {
		base.Commands = override.Commands
	}
}
