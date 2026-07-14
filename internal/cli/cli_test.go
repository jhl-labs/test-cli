package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jhl-labs/test-cli/internal/lang"
	"github.com/jhl-labs/test-cli/internal/model"
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
	code := Run([]string{"ingest", dir, "--tests", junit, "--coverage", cobertura, "-o", out, "--format", "json"}, &stdout, &stderr)
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
	code := Run([]string{"ingest", dir, "--tests", junit, "-o", out, "--format", "json"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
}

func TestAnalyzeStaticOnlyProducesQualityReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_test.go"), []byte("package app\nimport \"testing\"\nfunc TestValue(t *testing.T) { time.Sleep(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "qa")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"analyze", dir, "-o", out, "--format", "json,html"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("analyze exit = %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep model.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Schema != model.SchemaID || rep.Quality.Static.SourceFiles != 1 || rep.Quality.Static.TestFiles != 1 {
		t.Errorf("unexpected static report: %+v", rep.Quality.Static)
	}
	if _, err := os.Stat(filepath.Join(out, "insights.html")); err != nil {
		t.Errorf("insights page missing: %v", err)
	}
}

func TestQualityGate(t *testing.T) {
	dir := t.TempDir()
	junit := filepath.Join(dir, "junit.xml")
	if err := os.WriteFile(junit, []byte(`<testsuite name="s"><testcase name="ok"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", dir, "--tests", junit, "-o", filepath.Join(dir, "out"), "--format", "json", "--fail-quality", "80"}, &stdout, &stderr)
	if code != ExitTestFailure {
		t.Fatalf("quality gate exit = %d, want %d; stderr: %s", code, ExitTestFailure, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("quality score")) {
		t.Errorf("missing quality gate message: %s", stderr.String())
	}
}

func TestCoverageGateFailsWhenCoverageIsUnavailable(t *testing.T) {
	rep := &model.Report{Test: model.TestReport{Summary: model.TestSummary{Total: 1, Passed: 1}}}
	var stderr bytes.Buffer
	if code := gateExitCode(rep, 80, 0, 0, false, false, &stderr); code != ExitTestFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitTestFailure, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("coverage is unavailable")) {
		t.Fatalf("missing unavailable-coverage diagnostic: %s", stderr.String())
	}
}

func TestQualityGateFromProjectConfig(t *testing.T) {
	dir := t.TempDir()
	junit := filepath.Join(dir, "junit.xml")
	if err := os.WriteFile(junit, []byte(`<testsuite name="s"><testcase name="ok"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(`{"failQuality":80}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", dir, "--tests", junit, "-o", filepath.Join(dir, "out"), "--format", "json"}, &stdout, &stderr)
	if code != ExitTestFailure || !bytes.Contains(stderr.Bytes(), []byte("quality score")) {
		t.Fatalf("configured quality gate exit = %d; stderr: %s", code, stderr.String())
	}
}

func TestBaselineRegressionGateAndComparisonReport(t *testing.T) {
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "baseline.json")
	baseline := model.Report{
		Schema:      model.SchemaID,
		GeneratedAt: time.Unix(100, 0).UTC(),
		Test:        model.TestReport{Suites: []model.TestSuite{{Name: "s", Cases: []model.TestCase{{Name: "ok", Status: model.StatusPassed}}}}},
		Coverage:    model.CoverageReport{Files: []model.FileCoverage{{Path: "x.py", Lines: model.Metric{Covered: 10, Total: 10}, LineHits: []model.LineHit{{Line: 1, Hits: 1}}}}},
	}
	baseline.Normalize()
	data, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baselinePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	junit := filepath.Join(dir, "junit.xml")
	coverage := filepath.Join(dir, "coverage.xml")
	if err := os.WriteFile(junit, []byte(`<testsuite name="s"><testcase name="ok"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coverage, []byte(`<coverage><packages><package><classes><class filename="x.py"><lines><line number="1" hits="0"/><line number="2" hits="0"/></lines></class></classes></package></packages></coverage>`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", dir, "--tests", junit, "--coverage", coverage, "--baseline", baselinePath, "--fail-on-regression", "-o", out, "--format", "json"}, &stdout, &stderr)
	if code != ExitTestFailure || !bytes.Contains(stderr.Bytes(), []byte("regressed against")) {
		t.Fatalf("regression gate exit = %d; stderr: %s", code, stderr.String())
	}
	reportData, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var current model.Report
	if err := json.Unmarshal(reportData, &current); err != nil {
		t.Fatal(err)
	}
	if current.Quality.Comparison == nil || !current.Quality.Comparison.Regressed {
		t.Errorf("comparison missing from report: %+v", current.Quality.Comparison)
	}
}

func TestRegressionGateRequiresBaseline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"analyze", t.TempDir(), "--fail-on-regression"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr: %s", code, stderr.String())
	}
}

func TestHistoryFlakyGateAndReport(t *testing.T) {
	dir := t.TempDir()
	historyPaths := make([]string, 0, 2)
	historyNames := []string{"history-1.json", "history-2.json"}
	for i, status := range []string{model.StatusPassed, model.StatusFailed} {
		historical := model.Report{
			Schema:      model.SchemaID,
			GeneratedAt: time.Unix(int64(100+i), 0).UTC(),
			Test:        model.TestReport{Suites: []model.TestSuite{{Name: "s", Cases: []model.TestCase{{Name: "sometimes", Status: status}}}}},
		}
		historical.Normalize()
		data, err := json.Marshal(historical)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, historyNames[i])
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		historyPaths = append(historyPaths, path)
	}
	junit := filepath.Join(dir, "junit.xml")
	if err := os.WriteFile(junit, []byte(`<testsuite name="s"><testcase name="sometimes"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", dir, "--tests", junit, "--history", historyPaths[0], "--history", historyPaths[1], "--fail-on-flaky", "-o", out, "--format", "json"}, &stdout, &stderr)
	if code != ExitTestFailure || !bytes.Contains(stderr.Bytes(), []byte("flaky test candidate")) {
		t.Fatalf("flaky gate exit = %d; stderr: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var current model.Report
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	if current.Quality.History == nil || current.Quality.History.Runs != 3 || len(current.Quality.History.FlakyTests) != 1 {
		t.Errorf("history missing from report: %+v", current.Quality.History)
	}
}

func TestFlakyGateRequiresTwoHistoricalReports(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"analyze", t.TempDir(), "--history", "one.json", "--fail-on-flaky"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr: %s", code, stderr.String())
	}
}

func TestFlakyGateRejectsDuplicateHistoryPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"analyze", t.TempDir(), "--history", "same.json", "--history", "same.json", "--fail-on-flaky"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr: %s", code, stderr.String())
	}
}

func TestChangedLineCoverageGateAndReport(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runCLIGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def value():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, dir, "add", "app.py")
	runCLIGit(t, dir, "-c", "user.name=test-cli", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def value(flag):\n    if flag:\n        return 2\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	junit := filepath.Join(dir, "junit.xml")
	coverage := filepath.Join(dir, "coverage.xml")
	if err := os.WriteFile(junit, []byte(`<testsuite name="s"><testcase name="ok"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coverage, []byte(`<coverage><packages><package><classes><class filename="app.py"><lines><line number="1" hits="1"/><line number="2" hits="0"/><line number="3" hits="0"/><line number="4" hits="1"/></lines></class></classes></package></packages></coverage>`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", dir, "--tests", junit, "--coverage", coverage, "--diff-base", "HEAD", "--fail-diff-coverage", "80", "-o", out, "--format", "json"}, &stdout, &stderr)
	if code != ExitTestFailure || !bytes.Contains(stderr.Bytes(), []byte("changed-line coverage")) {
		t.Fatalf("diff coverage gate exit = %d; stderr: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep model.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Quality.Changes == nil || rep.Quality.Changes.CoveragePct >= 80 || rep.Quality.Changes.CoverableLines == 0 {
		t.Errorf("changed-line analysis missing: %+v", rep.Quality.Changes)
	}
}

func TestDiffCoverageGateRequiresBase(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"analyze", t.TempDir(), "--fail-diff-coverage", "80"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr: %s", code, stderr.String())
	}
}

func TestDiffCoverageGateFailsWhenChangedSourceLacksLineEvidence(t *testing.T) {
	rep := &model.Report{Quality: model.QualityReport{Changes: &model.ChangeAnalysis{SourceFilesChanged: 1, FilesWithoutCoverage: 1}}}
	var stderr bytes.Buffer
	if code := gateExitCode(rep, 0, 0, 80, false, false, &stderr); code != ExitTestFailure || !bytes.Contains(stderr.Bytes(), []byte("lack line-level coverage")) {
		t.Fatalf("exit = %d; stderr: %s", code, stderr.String())
	}
}

func TestConfiguredDiffCoverageThresholdIsValidated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(`{"diffBase":"HEAD","failDiffCoverage":120}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"analyze", dir}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr: %s", code, stderr.String())
	}
}

func runCLIGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
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

func TestDoctorUsesConfiguredCommandCapabilities(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{
		"languages": []string{"typescript"},
		"commands":  map[string][][]string{"typescript": {{executable}}},
	})
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"doctor", dir}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("doctor exit = %d; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("configured command")) {
		t.Fatalf("configured capability not reported: %s", stdout.String())
	}
}

func TestInspectToolchainReportsEveryMissingCapability(t *testing.T) {
	a := &lang.Adapter{Checks: []lang.Probe{
		{Label: "first plugin", Args: []string{"test-cli-definitely-missing-first"}},
		{Label: "second plugin", Args: []string{"test-cli-definitely-missing-second"}},
	}}
	state := inspectToolchain(t.TempDir(), a, nil)
	if state.Ready || len(state.Missing) != 2 {
		t.Fatalf("toolchain state = %+v", state)
	}
}

func TestCapabilityProbeAndReadinessHelpers(t *testing.T) {
	root := t.TempDir()
	if out, err := executeCapabilityProbe(root, lang.Probe{Label: "Go", Args: []string{"go", "version"}, Contains: "go version"}); err != nil || !bytes.Contains([]byte(out), []byte("go version")) {
		t.Fatalf("successful probe: out=%q err=%v", out, err)
	}
	if _, err := executeCapabilityProbe(root, lang.Probe{}); err == nil {
		t.Error("empty probe should fail")
	}
	if _, err := executeCapabilityProbe(root, lang.Probe{Args: []string{"go", "version"}, Contains: "definitely-absent-capability"}); err == nil {
		t.Error("missing output capability should fail")
	}
	if _, err := executeCapabilityProbe(root, lang.Probe{Args: []string{"go"}, ExecutableOnly: true}); err != nil {
		t.Fatalf("executable-only probe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "project.toml"), []byte("[report.junit]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := executeCapabilityProbe(root, lang.Probe{Files: []string{"*.toml"}, Contains: "[report.junit]"}); err != nil || out != "project.toml" {
		t.Fatalf("file probe: out=%q err=%v", out, err)
	}
	if _, err := executeCapabilityProbe(root, lang.Probe{Files: []string{"*.toml"}, Contains: "missing-setting"}); err == nil {
		t.Error("missing file capability should fail")
	}
	if _, err := executeCapabilityProbe(root, lang.Probe{Files: []string{"*.props"}, Contains: "package"}); err == nil {
		t.Error("missing configuration file should fail")
	}

	script := filepath.Join(root, "tool")
	if err := os.WriteFile(script, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveExecutable(root, "./tool"); err != nil || got != script {
		t.Fatalf("relative executable = %q, %v", got, err)
	}
	if got, err := resolveExecutable(root, script); err != nil || got != script {
		t.Fatalf("absolute executable = %q, %v", got, err)
	}
	if _, err := resolveExecutable(root, root); err == nil {
		t.Error("directory should not be executable")
	}
	if _, err := resolveExecutable(root, "./missing"); err == nil {
		t.Error("missing relative executable should fail")
	}

	if got := readinessText(toolchainState{Ready: true, Tool: "Go", Version: "go1"}); got != "Go — go1" {
		t.Fatalf("version readiness = %q", got)
	}
	if got := readinessText(toolchainState{Ready: true, Tool: "node"}); got != "node ready" {
		t.Fatalf("ready text = %q", got)
	}
	if got := readinessText(toolchainState{}); got != "unavailable" {
		t.Fatalf("unavailable text = %q", got)
	}
	if got := readinessText(toolchainState{Missing: []string{"plugin"}}); got != "unavailable (plugin)" {
		t.Fatalf("missing text = %q", got)
	}
	if got := informativeLine("\n-----\nGradle 9\n"); got != "Gradle 9" {
		t.Fatalf("informative line = %q", got)
	}
	if got := diagnosticLine("header\nError: Cannot find module 'x'\n"); got != "Error: Cannot find module 'x'" {
		t.Fatalf("diagnostic line = %q", got)
	}
}

func TestConfiguredCommandProbesDeduplicateExecutables(t *testing.T) {
	probes := configuredCommandProbes([][]string{{"node", "a.js"}, {"node", "b.js"}, {}, {"{root}/tool"}}, "/project")
	if len(probes) != 2 || probes[0].Args[0] != "node" || probes[1].Args[0] != "/project/tool" {
		t.Fatalf("configured probes = %+v", probes)
	}
}

func TestDetectJSONReportsCapabilityIssues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".test-cli.json"), []byte(`{"languages":["typescript"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"detect", dir, "--json"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("detect exit = %d; stderr=%s", code, stderr.String())
	}
	var payload struct {
		Languages []struct {
			Language string   `json:"language"`
			Ready    bool     `json:"ready"`
			Issues   []string `json:"issues"`
		} `json:"languages"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range payload.Languages {
		if entry.Language == "typescript" {
			found = true
			if entry.Ready || len(entry.Issues) == 0 {
				t.Fatalf("TypeScript detection = %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("TypeScript detect entry missing")
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
	if code := Run([]string{"ingest", dir, "--tests", junit, "-o", out, "--format", "json"}, &b, &b); code != ExitOK {
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

func TestReportDoesNotTrustEmbeddedSourceRoot(t *testing.T) {
	dir := t.TempDir()
	sourceRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "secret.go"), []byte("package secret\nconst token = \"do-not-copy\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := model.Report{
		Schema: model.SchemaID, Root: sourceRoot,
		Coverage: model.CoverageReport{Files: []model.FileCoverage{{Path: "secret.go", LineHits: []model.LineHit{{Line: 1, Hits: 1}}}}},
	}
	rep.Normalize()
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "external-report.json")
	if err := os.WriteFile(reportPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	withoutSource := filepath.Join(dir, "without-source")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"report", "--in", reportPath, "-o", withoutSource, "--format", "html"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("safe re-render exit = %d: %s", code, stderr.String())
	}
	heatmap := readCoverageHeatmap(t, withoutSource)
	if bytes.Contains(heatmap, []byte("do-not-copy")) {
		t.Fatal("embedded report root authorized an unexpected source read")
	}

	withSource := filepath.Join(dir, "with-source")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"report", "--in", reportPath, "--source-root", sourceRoot, "-o", withSource, "--format", "html"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("trusted re-render exit = %d: %s", code, stderr.String())
	}
	if !bytes.Contains(readCoverageHeatmap(t, withSource), []byte("do-not-copy")) {
		t.Fatal("explicit source root did not enable source heatmap rendering")
	}
}

func TestReportDiffRequiresExplicitSourceRoot(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"schema":"test-cli/report@2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"report", "--in", reportPath, "--diff-base", "HEAD"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr: %s", code, stderr.String())
	}
}

func readCoverageHeatmap(t *testing.T, outDir string) []byte {
	t.Helper()
	coverageDir := filepath.Join(outDir, "coverage")
	entries, err := os.ReadDir(coverageDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "index.html" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(coverageDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	t.Fatal("per-file coverage heatmap is missing")
	return nil
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
