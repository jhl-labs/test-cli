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

func TestGenerateSkillStdout(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"generate-skill", "--stdout"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("name: test-runner")) {
		t.Errorf("skill missing frontmatter name:\n%s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("report.json")) {
		t.Error("skill should reference report.json")
	}
}

func TestGenerateSkillWritesFile(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"generate-skill", "--out", dir, "--name", "qa"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("exit = %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "qa", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md not written: %v", err)
	}
}

func TestDetectJSONOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"detect", dir, "--json"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"languages"`)) {
		t.Errorf("detect --json missing languages key:\n%s", out.String())
	}
}

func TestDoctorEmptyDir(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	// No languages detected -> not an environment failure.
	if code := Run([]string{"doctor", dir}, &out, &errb); code != ExitOK {
		t.Errorf("doctor exit = %d, want 0\n%s", code, out.String())
	}
}

func TestReportReRender(t *testing.T) {
	dir := t.TempDir()
	// First produce a report.json via ingest.
	junit := filepath.Join(dir, "junit.xml")
	if err := os.WriteFile(junit, []byte(
		`<testsuites><testsuite name="s"><testcase name="ok" time="0.1"/></testsuite></testsuites>`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	var b bytes.Buffer
	if code := Run([]string{"ingest", "--tests", junit, "-o", out, "--format", "json"}, &b, &b); code != ExitOK {
		t.Fatalf("ingest exit = %d: %s", code, b.String())
	}
	// Now re-render from the saved report.json into markdown.
	b.Reset()
	code := Run([]string{"report", "--in", filepath.Join(out, "report.json"), "-o", out, "--format", "markdown"}, &b, &b)
	if code != ExitOK {
		t.Fatalf("report exit = %d: %s", code, b.String())
	}
	if _, err := os.Stat(filepath.Join(out, "report.md")); err != nil {
		t.Errorf("report.md not produced: %v", err)
	}
}

// TestOutputDirRelativeToCWD locks in that a relative --output-dir is resolved
// against the current working directory, not the (sub)directory target. This
// was caught by the test-cli-action smoke test.
func TestOutputDirRelativeToCWD(t *testing.T) {
	tmp := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.MkdirAll(filepath.Join(tmp, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "junit.xml"), []byte(
		`<testsuites><testsuite name="s"><testcase name="ok" time="0.1"/></testsuite></testsuites>`), 0o644); err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	// Target is the subdirectory "project"; output-dir is relative.
	code := Run([]string{"ingest", "project", "--tests", "junit.xml", "-o", "reports/test", "--format", "json"}, &b, &b)
	if code != ExitOK {
		t.Fatalf("exit = %d: %s", code, b.String())
	}
	// Report must land under CWD, not under the target subdirectory.
	if _, err := os.Stat(filepath.Join(tmp, "reports", "test", "report.json")); err != nil {
		t.Errorf("report not written relative to CWD: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "project", "reports", "test", "report.json")); err == nil {
		t.Error("report incorrectly written relative to the target directory")
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
