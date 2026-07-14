package analysis

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jhl-labs/test-cli/internal/model"
)

func TestEvaluateBuildsStandardDiagnostics(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "service.ts"), "export function value() {\n  return 1\n}\n")
	write(t, filepath.Join(root, "tests", "service.test.ts"), `
describe.only("service", () => {
  it.skip("later", () => {})
  it("waits", async () => { await new Promise(r => setTimeout(r, 50)) })
})
`)

	r := &model.Report{
		Test: model.TestReport{Suites: []model.TestSuite{{Name: "service", Language: "typescript", Cases: []model.TestCase{
			{Name: "fast-a", Status: model.StatusPassed, DurationMs: 10},
			{Name: "fast-b", Status: model.StatusPassed, DurationMs: 10},
			{Name: "slow", Status: model.StatusPassed, DurationMs: 5000},
			{Name: "later", Status: model.StatusSkipped},
		}}}},
		Coverage: model.CoverageReport{Files: []model.FileCoverage{
			{Path: "src/service.ts", Language: "typescript", Lines: model.Metric{Covered: 30, Total: 100}, Branches: model.Metric{Covered: 1, Total: 4}},
			{Path: "src/ok.ts", Language: "typescript", Lines: model.Metric{Covered: 10, Total: 10}},
		}},
	}
	r.Normalize()
	Evaluate(r, root)

	if r.Quality.Score <= 0 || len(r.Quality.Dimensions) != 5 {
		t.Fatalf("quality score/dimensions not built: %+v", r.Quality)
	}
	if r.Quality.Static.SourceFiles != 1 || r.Quality.Static.TestFiles != 1 {
		t.Errorf("static inventory = %+v", r.Quality.Static)
	}
	for _, rule := range []string{"focused-test", "disabled-test", "hard-wait"} {
		if !hasSmell(r.Quality.Static.Smells, rule) {
			t.Errorf("missing smell %q: %+v", rule, r.Quality.Static.Smells)
		}
	}
	for _, id := range []string{"TEST-004", "COV-002", "COV-004", "COV-005", "PERF-001", "STATIC-003"} {
		if !hasFinding(r.Quality.Findings, id) {
			t.Errorf("missing finding %q: %+v", id, r.Quality.Findings)
		}
	}
	if len(r.Quality.CoverageHotspots) == 0 || r.Quality.CoverageHotspots[0].Risk != "high" {
		t.Errorf("coverage hotspots = %+v", r.Quality.CoverageHotspots)
	}
	if len(r.Quality.SlowTests) == 0 || r.Quality.SlowTests[0].Name != "slow" {
		t.Errorf("slow tests = %+v", r.Quality.SlowTests)
	}
}

func TestEvaluateDoesNotClaimFlakiness(t *testing.T) {
	r := &model.Report{Test: model.TestReport{Suites: []model.TestSuite{{Name: "s", Cases: []model.TestCase{{Name: "sometimes", Status: model.StatusFailed}}}}}}
	r.Normalize()
	Evaluate(r, t.TempDir())
	for _, f := range r.Quality.Findings {
		if f.Category == "flakiness" {
			t.Fatalf("single-run analysis must not infer flakiness: %+v", f)
		}
	}
}

func TestStaticSmellsIgnoreQuotedSourceExamples(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "example_test.go"), "package example\nvar fixture = `\ntime.Sleep(10)\nt.Skip(\"example\")\n`\n")
	r := &model.Report{}
	r.Normalize()
	Evaluate(r, root)
	if hasSmell(r.Quality.Static.Smells, "hard-wait") {
		t.Fatalf("quoted fixture was treated as executable code: %+v", r.Quality.Static.Smells)
	}
}

func TestSourceClassificationExcludesNonExecutableToolingFiles(t *testing.T) {
	for _, path := range []string{"build.gradle.kts", "settings.gradle.kts", "build.rs", "module-info.java", "package-info.java", "types.d.ts", "vite.config.ts", "eslint.config.js", "webpack.prod.config.cjs"} {
		if got := sourceLanguage(path); got != "" {
			t.Errorf("sourceLanguage(%q) = %q, want tooling/declaration exclusion", path, got)
		}
	}
	if !isTestFile("conftest.py", "python") {
		t.Error("pytest conftest.py must be classified as test support")
	}
	for path, want := range map[string]string{"src/config.ts": "typescript", "src/app.ts": "typescript", "src/builder.rs": "rust", "src/App.kt": "java"} {
		if got := sourceLanguage(path); got != want {
			t.Errorf("sourceLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestStaticSmellsIgnoreConditionalGoSkip(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "guard_test.go"), `package example

import "testing"

func TestGuarded(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
}

func TestDisabled(t *testing.T) {
	t.Skip("not implemented")
}
`)
	r := &model.Report{}
	r.Normalize()
	Evaluate(r, root)
	var disabled []model.StaticSmell
	for _, smell := range r.Quality.Static.Smells {
		if smell.Rule == "disabled-test" {
			disabled = append(disabled, smell)
		}
	}
	if len(disabled) != 1 || disabled[0].Line != 12 {
		t.Fatalf("disabled-test smells = %+v, want only unconditional skip", disabled)
	}
}

func TestAllSkippedTestsHaveZeroReliability(t *testing.T) {
	r := &model.Report{Test: model.TestReport{Suites: []model.TestSuite{{Name: "s", Cases: []model.TestCase{{Name: "disabled", Status: model.StatusSkipped}}}}}}
	r.Normalize()
	Evaluate(r, t.TempDir())
	if r.Quality.Dimensions[0].Score != 0 {
		t.Errorf("reliability = %d, want 0", r.Quality.Dimensions[0].Score)
	}
	if !hasFinding(r.Quality.Findings, "TEST-001") {
		t.Errorf("missing no-executable-tests finding: %+v", r.Quality.Findings)
	}
}

func TestEvaluatePreservesStoredStaticDataWhenRootUnavailable(t *testing.T) {
	r := &model.Report{Quality: model.QualityReport{
		Static:     model.StaticAnalysis{SourceFiles: 7, TestFiles: 3},
		Comparison: &model.QualityComparison{Regressed: true},
		History:    &model.QualityHistory{Runs: 3, FlakyTests: []model.FlakyTest{{Name: "sometimes"}}},
		Findings: []model.QualityFinding{
			{ID: "REG-001", Severity: "critical", Category: "regression", Title: "previous regression"},
			{ID: "FLAKY-001", Severity: "high", Category: "flakiness", Title: "previous flaky evidence"},
		},
	}}
	r.Normalize()
	Evaluate(r, filepath.Join(t.TempDir(), "missing"))
	if r.Quality.Static.SourceFiles != 7 || r.Quality.Static.TestFiles != 3 {
		t.Errorf("stored static analysis was lost: %+v", r.Quality.Static)
	}
	if r.Quality.Comparison == nil || r.Quality.History == nil || !hasFinding(r.Quality.Findings, "REG-001") || !hasFinding(r.Quality.Findings, "FLAKY-001") {
		t.Errorf("stored comparison/history was lost: %+v", r.Quality)
	}
}

func TestAnalyzeHistoryFindsFlakyTestsAndTrends(t *testing.T) {
	h1 := qualityRun(time.Unix(100, 0), model.StatusPassed, 90, 1000)
	h2 := qualityRun(time.Unix(200, 0), model.StatusFailed, 80, 2000)
	current := qualityRun(time.Unix(300, 0), model.StatusPassed, 70, 3000)

	AnalyzeHistory(current, []*model.Report{h2, h1})

	history := current.Quality.History
	if history == nil || history.Runs != 3 || len(history.Samples) != 3 {
		t.Fatalf("history missing: %+v", history)
	}
	if len(history.FlakyTests) != 1 || history.FlakyTests[0].Name != "sometimes" || history.FlakyTests[0].Observations != 3 {
		t.Errorf("flaky candidates = %+v", history.FlakyTests)
	}
	if history.LineCoveragePct.Direction != "decreasing" || history.TestDurationMs.Direction != "increasing" {
		t.Errorf("trends = coverage %+v duration %+v", history.LineCoveragePct, history.TestDurationMs)
	}
	for _, id := range []string{"FLAKY-001", "TREND-001", "TREND-003"} {
		if !hasFinding(current.Quality.Findings, id) {
			t.Errorf("missing history finding %s: %+v", id, current.Quality.Findings)
		}
	}
}

func TestAnalyzeHistoryRequiresThreeObservationsForFlakyLabel(t *testing.T) {
	previous := qualityRun(time.Unix(100, 0), model.StatusFailed, 80, 1000)
	current := qualityRun(time.Unix(200, 0), model.StatusPassed, 80, 1000)
	AnalyzeHistory(current, []*model.Report{previous})
	if len(current.Quality.History.FlakyTests) != 0 || hasFinding(current.Quality.Findings, "FLAKY-001") {
		t.Fatalf("two observations must not be called flaky: %+v", current.Quality.History)
	}
}

func TestAnalyzeHistoryDoesNotCollapseDifferentStatusesWithSameTimestamp(t *testing.T) {
	h1 := qualityRun(time.Time{}, model.StatusFailed, 80, 1000)
	h2 := qualityRun(time.Time{}, model.StatusError, 80, 1000)
	current := qualityRun(time.Time{}, model.StatusPassed, 80, 1000)
	AnalyzeHistory(current, []*model.Report{h1, h2})
	if current.Quality.History.Runs != 3 {
		t.Fatalf("different observations were collapsed: %+v", current.Quality.History)
	}
	if len(current.Quality.History.FlakyTests) != 1 {
		t.Fatalf("flaky evidence was lost: %+v", current.Quality.History.FlakyTests)
	}
}

func TestParseGitPatchCollectsOnlyCurrentLineRanges(t *testing.T) {
	patch := []byte("diff --git a/src/a.go b/src/a.go\n--- a/src/a.go\n+++ b/src/a.go\n@@ -8,0 +9,2 @@\n+x\n+y\n@@ -20 +21 @@\n-old\n+new\ndiff --git a/old.go b/old.go\n--- a/old.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n")
	got := parseGitPatch(patch)
	if !reflect.DeepEqual(got["src/a.go"], []int{9, 10, 21}) {
		t.Fatalf("changed lines = %v", got)
	}
	if _, exists := got["old.go"]; exists {
		t.Fatalf("deleted file should not have current lines: %v", got)
	}
}

func TestAnalyzeChangesBuildsPatchCoverageAndMissingEvidence(t *testing.T) {
	r := &model.Report{Coverage: model.CoverageReport{Files: []model.FileCoverage{{
		Path: "example.test/project/src/service.go", LineHits: []model.LineHit{{Line: 10, Hits: 2}, {Line: 11, Hits: 0}, {Line: 99, Hits: 1}},
	}}}}
	r.Normalize()
	Evaluate(r, "")
	AnalyzeChanges(r, "main", map[string][]int{
		"src/service.go":      {10, 11, 12},
		"src/new.go":          {1, 2},
		"src/service_test.go": {1, 2, 3},
		"README.md":           {1},
	})

	c := r.Quality.Changes
	if c == nil || c.FilesChanged != 4 || c.SourceFilesChanged != 2 || c.ChangedLines != 5 {
		t.Fatalf("change summary = %+v", c)
	}
	if c.CoverableLines != 2 || c.CoveredLines != 1 || c.UncoveredLines != 1 || c.UnmappedLines != 3 || c.CoveragePct != 50 {
		t.Errorf("patch coverage = %+v", c)
	}
	if c.FilesWithoutCoverage != 1 || len(c.Files) != 2 || c.Files[0].Path != "src/new.go" {
		t.Errorf("file evidence = %+v", c.Files)
	}
	if !hasFinding(r.Quality.Findings, "CHG-001") || !hasFinding(r.Quality.Findings, "CHG-002") {
		t.Errorf("missing change findings: %+v", r.Quality.Findings)
	}
	Evaluate(r, "")
	if r.Quality.Changes == nil || !hasFinding(r.Quality.Findings, "CHG-001") {
		t.Errorf("stored change analysis was not preserved: %+v", r.Quality)
	}
}

func TestAnalyzeChangesDoesNotRequireCoverageForDeclarationOnlyGoFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "types.go"), "package example\n\ntype Result struct { Value int }\n")
	r := &model.Report{Root: root}
	r.Normalize()
	Evaluate(r, "")
	AnalyzeChanges(r, "HEAD", map[string][]int{"types.go": {1, 2, 3}})
	if r.Quality.Changes.FilesWithoutCoverage != 0 || len(r.Quality.Changes.Files) != 1 || r.Quality.Changes.Files[0].CoverageRequired {
		t.Fatalf("declaration-only file treated as missing coverage: %+v", r.Quality.Changes)
	}
	if hasFinding(r.Quality.Findings, "CHG-001") {
		t.Fatalf("declaration-only file produced missing-coverage finding: %+v", r.Quality.Findings)
	}
}

func TestChangedFileCoverageRejectsAmbiguousSuffixMatch(t *testing.T) {
	files := []model.FileCoverage{
		{Path: "module-a/src/service.go", LineHits: []model.LineHit{{Line: 1, Hits: 1}}},
		{Path: "module-b/src/service.go", LineHits: []model.LineHit{{Line: 1, Hits: 0}}},
	}
	if _, matched := changedFileCoverage("src/service.go", files); matched {
		t.Fatal("ambiguous suffix coverage match must not select an arbitrary file")
	}
	exact := append(files, model.FileCoverage{Path: "src/service.go"})
	if got, matched := changedFileCoverage("src/service.go", exact); !matched || got.Path != "src/service.go" {
		t.Fatalf("exact coverage path was not preferred: %+v, matched=%v", got, matched)
	}
}

func TestGitChangedLinesIncludesTrackedAndUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	write(t, filepath.Join(root, "app.go"), "package app\n\nfunc Value() int { return 1 }\n")
	runGit(t, root, "add", "app.go")
	runGit(t, root, "-c", "user.name=test-cli", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	write(t, filepath.Join(root, "app.go"), "package app\n\nfunc Value() int {\n\treturn 2\n}\n")
	write(t, filepath.Join(root, "new.go"), "package app\n\nfunc Added() {}\n")

	changed, err := GitChangedLines(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed["app.go"]) == 0 || !reflect.DeepEqual(changed["new.go"], []int{1, 2, 3}) {
		t.Errorf("Git changes = %+v", changed)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func qualityRun(at time.Time, status string, coveragePct int, durationMs float64) *model.Report {
	r := &model.Report{
		GeneratedAt: at.UTC(),
		Test:        model.TestReport{Suites: []model.TestSuite{{Name: "suite", Language: "go", Cases: []model.TestCase{{Name: "sometimes", Status: status, DurationMs: durationMs}}}}},
		Coverage:    model.CoverageReport{Files: []model.FileCoverage{{Path: "service.go", Language: "go", Lines: model.Metric{Covered: coveragePct, Total: 100}}}},
	}
	r.Normalize()
	Evaluate(r, "")
	return r
}

func TestCompareFindsRegressionsAndImprovements(t *testing.T) {
	baseline := &model.Report{
		GeneratedAt: time.Unix(100, 0).UTC(),
		Test: model.TestReport{Suites: []model.TestSuite{{Name: "suite", Language: "go", Cases: []model.TestCase{
			{Name: "new-failure", Status: model.StatusPassed},
			{Name: "resolved", Status: model.StatusFailed},
		}}}},
		Coverage: model.CoverageReport{Files: []model.FileCoverage{
			{Path: "a.go", Lines: model.Metric{Covered: 80, Total: 100}, Branches: model.Metric{Covered: 8, Total: 10}},
			{Path: "b.go", Lines: model.Metric{Covered: 50, Total: 100}, Branches: model.Metric{Covered: 5, Total: 10}},
		}},
	}
	current := &model.Report{
		Test: model.TestReport{Suites: []model.TestSuite{{Name: "suite", Language: "go", Cases: []model.TestCase{
			{Name: "new-failure", Status: model.StatusFailed},
			{Name: "resolved", Status: model.StatusPassed},
		}}}},
		Coverage: model.CoverageReport{Files: []model.FileCoverage{
			{Path: "a.go", Lines: model.Metric{Covered: 60, Total: 100}, Branches: model.Metric{Covered: 4, Total: 10}},
			{Path: "b.go", Lines: model.Metric{Covered: 70, Total: 100}, Branches: model.Metric{Covered: 5, Total: 10}},
		}},
	}
	baseline.Normalize()
	current.Normalize()
	Evaluate(baseline, "")
	Evaluate(current, "")
	Compare(current, baseline)

	c := current.Quality.Comparison
	if c == nil || !c.Regressed {
		t.Fatalf("comparison missing or not regressed: %+v", c)
	}
	if !c.BaselineGeneratedAt.Equal(baseline.GeneratedAt) {
		t.Errorf("baseline timestamp = %v", c.BaselineGeneratedAt)
	}
	if len(c.NewFailures) != 1 || c.NewFailures[0].Name != "new-failure" {
		t.Errorf("new failures = %+v", c.NewFailures)
	}
	if len(c.ResolvedFailures) != 1 || c.ResolvedFailures[0].Name != "resolved" {
		t.Errorf("resolved failures = %+v", c.ResolvedFailures)
	}
	if len(c.CoverageRegressions) != 1 || len(c.CoverageImprovements) != 1 {
		t.Errorf("coverage changes = regressions %+v improvements %+v", c.CoverageRegressions, c.CoverageImprovements)
	}
	if !hasFinding(current.Quality.Findings, "REG-001") || !hasFinding(current.Quality.Findings, "REG-006") {
		t.Errorf("missing regression findings: %+v", current.Quality.Findings)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasFinding(findings []model.QualityFinding, id string) bool {
	for _, f := range findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func hasSmell(smells []model.StaticSmell, rule string) bool {
	for _, smell := range smells {
		if smell.Rule == rule {
			return true
		}
	}
	return false
}
