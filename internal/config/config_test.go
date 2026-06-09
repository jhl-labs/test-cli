package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != "" {
		t.Errorf("expected empty Path for defaults, got %q", cfg.Path)
	}
	if cfg.OutputDir != "reports/test" {
		t.Errorf("default outputDir = %q", cfg.OutputDir)
	}
	if len(cfg.Formats) == 0 {
		t.Error("default formats empty")
	}
}

func TestLoadAndMergeOverrides(t *testing.T) {
	dir := t.TempDir()
	js := `{
	  "outputDir": "out/reports",
	  "formats": ["json","html"],
	  "failUnder": 85,
	  "languages": ["go"],
	  "commands": {"typescript": [["npx","vitest","run"]]}
	}`
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutputDir != "out/reports" {
		t.Errorf("outputDir = %q", cfg.OutputDir)
	}
	if cfg.FailUnder != 85 {
		t.Errorf("failUnder = %v", cfg.FailUnder)
	}
	if len(cfg.Formats) != 2 || cfg.Formats[0] != "json" {
		t.Errorf("formats = %v", cfg.Formats)
	}
	if got := cfg.Commands["typescript"]; len(got) != 1 || got[0][0] != "npx" {
		t.Errorf("command override = %v", cfg.Commands["typescript"])
	}
	if cfg.Path == "" {
		t.Error("Path should be set when a file is loaded")
	}
}

func TestLoadWalksUpToParent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(`{"failUnder":50}`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(child)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FailUnder != 50 {
		t.Errorf("config not discovered from parent: failUnder = %v", cfg.FailUnder)
	}
}

func TestLoadToleratesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	// Forward-compat: an unknown key must not fail the load.
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(`{"futureKey":true,"failUnder":10}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unknown field should be tolerated: %v", err)
	}
	if cfg.FailUnder != 10 {
		t.Errorf("failUnder = %v", cfg.FailUnder)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("expected error for malformed JSON")
	}
}
