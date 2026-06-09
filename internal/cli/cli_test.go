package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseWithTargetFlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	profile := fs.String("profile", "default", "")
	var langs stringList
	fs.Var(&langs, "lang", "")

	// Flags appear BOTH before and after the positional target.
	target, err := parseWithTarget(fs, []string{"--lang", "go", "./proj", "--profile", "ci"}, ".")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if target != "./proj" {
		t.Errorf("target = %q, want ./proj", target)
	}
	if *profile != "ci" {
		t.Errorf("profile = %q, want ci (flag after positional was dropped)", *profile)
	}
	if len(langs) != 1 || langs[0] != "go" {
		t.Errorf("langs = %v, want [go]", langs)
	}
}

func TestRunIngestExitCodeAndReport(t *testing.T) {
	dir := t.TempDir()
	junit := filepath.Join(dir, "junit.xml")
	cobertura := filepath.Join(dir, "cov.xml")
	out := filepath.Join(dir, "out")

	if err := os.WriteFile(junit, []byte(
		`<testsuites><testsuite name="s"><testcase name="ok" time="0.1"/>`+
			`<testcase name="bad" time="0.1"><failure message="boom">trace</failure></testcase></testsuite></testsuites>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cobertura, []byte(
		`<coverage><packages><package name="p"><classes><class filename="x.py">`+
			`<lines><line number="1" hits="1"/><line number="2" hits="0"/></lines></class></classes></package></packages></coverage>`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", "--tests", junit, "--coverage", cobertura, "-o", out, "--format", "json"}, &stdout, &stderr)
	if code != ExitTestFailure {
		t.Fatalf("exit = %d, want %d (a test failed)\nstderr: %s", code, ExitTestFailure, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "report.json")); err != nil {
		t.Errorf("report.json not written: %v", err)
	}
}

func TestRunGreenExitOK(t *testing.T) {
	dir := t.TempDir()
	junit := filepath.Join(dir, "junit.xml")
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(junit, []byte(
		`<testsuites><testsuite name="s"><testcase name="ok" time="0.1"/></testsuite></testsuites>`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", "--tests", junit, "-o", out, "--format", "json"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
}

func TestVersionAndHelp(t *testing.T) {
	var b bytes.Buffer
	if code := Run([]string{"version"}, &b, &b); code != ExitOK {
		t.Errorf("version exit = %d", code)
	}
	b.Reset()
	if code := Run([]string{"--help"}, &b, &b); code != ExitOK {
		t.Errorf("help exit = %d", code)
	}
	b.Reset()
	if code := Run([]string{"bogus"}, &b, &b); code != ExitUsage {
		t.Errorf("unknown command exit = %d, want %d", code, ExitUsage)
	}
}
