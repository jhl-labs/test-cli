package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhl-labs/test-cli/internal/analysis"
	"github.com/jhl-labs/test-cli/internal/ingest"
	"github.com/jhl-labs/test-cli/internal/model"
)

func sampleReport() *model.Report {
	r := &model.Report{
		ToolVersion: "v0.0.0-test",
		GeneratedAt: time.Unix(0, 0).UTC(),
		Root:        "/repo",
		Languages:   []string{"go"},
		Test: model.TestReport{Suites: []model.TestSuite{{
			Name: "pkg", Language: "go",
			Cases: []model.TestCase{
				{Name: "TestOK", Status: model.StatusPassed, DurationMs: 1},
				{Name: "TestBad", Status: model.StatusFailed, Message: "boom", Detail: "stack trace"},
			},
		}}},
		Coverage: model.CoverageReport{Files: []model.FileCoverage{{
			Path:  "a.go",
			Lines: model.Metric{Covered: 1, Total: 2},
			LineHits: []model.LineHit{
				{Line: 1, Hits: 3},
				{Line: 2, Hits: 0},
			},
		}}},
	}
	r.Normalize()
	analysis.Evaluate(r, r.Root)
	return r
}

func TestRelPathDoesNotTreatSiblingAsChild(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	inside := filepath.Join(root, "pkg", "file.go")
	sibling := filepath.Join(filepath.Dir(root), "project-old", "file.go")
	if got := relPath(root, inside); got != "pkg/file.go" {
		t.Fatalf("inside path = %q", got)
	}
	if got := relPath(root, sibling); got != sibling {
		t.Fatalf("sibling path = %q, want unchanged %q", got, sibling)
	}
}

func TestWriteJSONAndCobertura(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	if _, err := Write(r, FormatJSON, dir, "/repo"); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, err := Write(r, FormatCobertura, dir, "/repo"); err != nil {
		t.Fatalf("cobertura: %v", err)
	}
	if _, err := Write(r, FormatJUnit, dir, "/repo"); err != nil {
		t.Fatalf("junit: %v", err)
	}
	for _, name := range []string{"report.json", "coverage.cobertura.xml", "junit.xml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestXMLReportsPreserveSourceMetadataAndBranchCoverage(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	r.Test.Suites[0].File = "pkg/a_test.go"
	r.Coverage.Files[0].Branches = model.Metric{Covered: 1, Total: 2}
	r.Normalize()
	if _, err := Write(r, FormatJUnit, dir, "/repo"); err != nil {
		t.Fatal(err)
	}
	junit, err := os.ReadFile(filepath.Join(dir, "junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(junit), `file="pkg/a_test.go"`) {
		t.Fatalf("JUnit suite file was not preserved:\n%s", junit)
	}

	if _, err := Write(r, FormatCobertura, dir, "/repo"); err != nil {
		t.Fatal(err)
	}
	cobertura, err := os.ReadFile(filepath.Join(dir, "coverage.cobertura.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cobertura), `timestamp="0"`) || !strings.Contains(string(cobertura), `branches-valid="2"`) {
		t.Fatalf("Cobertura metadata missing:\n%s", cobertura)
	}
	files, err := ingest.ParseCobertura(cobertura, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Branches.Covered != 1 || files[0].Branches.Total != 2 {
		t.Fatalf("Cobertura branch coverage did not round-trip: %+v", files)
	}
}

func TestWriteHTMLProducesHeatmap(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	r.Quality.Comparison = &model.QualityComparison{
		BaselineGeneratedAt: time.Unix(10, 0).UTC(),
		Regressed:           true,
		Score:               model.IntDelta{Before: 70, After: r.Quality.Score, Delta: r.Quality.Score - 70},
		LineCoveragePct:     model.FloatDelta{Before: 75, After: 50, Delta: -25},
		CoverageRegressions: []model.CoverageChange{{Path: "a.go", BeforePct: 75, AfterPct: 50, DeltaPct: -25}},
	}
	r.Quality.History = sampleHistory()
	r.Quality.Changes = sampleChanges()
	paths, err := Write(r, FormatHTML, dir, "/repo")
	if err != nil {
		t.Fatalf("html: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("expected dashboard + coverage pages, got %v", paths)
	}
	idx, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(idx), "Test &amp; Coverage Report") {
		t.Error("dashboard missing title")
	}
	if !strings.Contains(string(idx), "QA diagnostics") {
		t.Error("dashboard missing QA diagnostics")
	}
	insights, err := os.ReadFile(filepath.Join(dir, "insights.html"))
	if err != nil {
		t.Fatalf("read insights: %v", err)
	}
	if !strings.Contains(string(insights), "Quality dimensions") || !strings.Contains(string(insights), "Test duration heatmap") {
		t.Error("insights page missing quality or duration visualization")
	}
	if !strings.Contains(string(insights), "Baseline comparison") || !strings.Contains(string(insights), "Coverage regressions") {
		t.Error("insights page missing baseline comparison")
	}
	if !strings.Contains(string(insights), "Run history") || !strings.Contains(string(insights), "Flaky candidates") || !strings.Contains(string(insights), "history-chart") {
		t.Error("insights page missing history/flaky visualizations")
	}
	if !strings.Contains(string(insights), "Changed-code coverage") || !strings.Contains(string(insights), "change-map") || !strings.Contains(string(insights), "#L2") {
		t.Error("insights page missing changed-code coverage map or line link")
	}
	coverageIndex, err := os.ReadFile(filepath.Join(dir, "coverage", "index.html"))
	if err != nil {
		t.Fatalf("read coverage index: %v", err)
	}
	if !strings.Contains(string(coverageIndex), "Coverage treemap") || !strings.Contains(string(coverageIndex), "Directory coverage tree") {
		t.Error("coverage index missing map/tree visualizations")
	}
	for name, data := range map[string][]byte{"dashboard": idx, "insights": insights, "coverage": coverageIndex} {
		if strings.Contains(string(data), "ZgotmplZ") {
			t.Errorf("%s contains an unsafe template replacement", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "coverage", "index.html")); err != nil {
		t.Errorf("coverage index missing: %v", err)
	}
	heatmapName := slugFor("a.go")
	if _, err := os.Stat(filepath.Join(dir, "coverage", heatmapName)); err != nil {
		t.Errorf("per-file heatmap missing: %v", err)
	}
	coverageFile, err := os.ReadFile(filepath.Join(dir, "coverage", heatmapName))
	if err != nil || !strings.Contains(string(coverageFile), `id="L2"`) {
		t.Errorf("per-file heatmap missing line anchors: %v", err)
	}
}

func TestWriteHTMLRemovesStaleCoveragePages(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "coverage", "stale.html")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old source"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &model.Report{ToolVersion: "test", GeneratedAt: time.Now()}
	r.Normalize()
	analysis.Evaluate(r, "")
	if _, err := Write(r, FormatHTML, dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale coverage page remains: %v", err)
	}
}

func TestReadSourceStaysWithinRepositoryRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(parent, "secret.go")
	if err := os.WriteFile(secret, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lines := readSource(root, "../secret.go"); len(lines) != 0 {
		t.Fatalf("read source outside root: %v", lines)
	}
	private := filepath.Join(root, ".env")
	if err := os.WriteFile(private, []byte("TOKEN=do-not-publish\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if lines := readSource(root, ".env"); len(lines) != 0 {
		t.Fatalf("non-source file was exposed through a coverage path: %v", lines)
	}
	link := filepath.Join(root, "linked.go")
	if err := os.Symlink(secret, link); err == nil {
		if lines := readSource(root, "linked.go"); len(lines) != 0 {
			t.Fatalf("followed source symlink outside root: %v", lines)
		}
	}
	inside := filepath.Join(root, "internal", "app.go")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lines := readSource(root, "example.test/project/internal/app.go"); len(lines) == 0 || lines[0] != "package app" {
		t.Fatalf("repository source was not resolved: %v", lines)
	}
}

func TestSlugForIsBoundedAndCollisionResistant(t *testing.T) {
	a := slugFor("src/a/b.go")
	b := slugFor("src/a__b.go")
	if a == b {
		t.Fatalf("different coverage paths collided: %q", a)
	}
	long := slugFor(strings.Repeat("directory/", 100) + "file.go")
	if len(long) > 120 || !strings.HasSuffix(long, ".html") {
		t.Fatalf("invalid bounded slug (%d bytes): %q", len(long), long)
	}
}

func TestMarkdownIncludesBaselineComparison(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	r.Quality.Comparison = &model.QualityComparison{
		BaselineGeneratedAt: time.Unix(10, 0).UTC(),
		Regressed:           true,
		Score:               model.IntDelta{Before: 80, After: 60, Delta: -20},
		LineCoveragePct:     model.FloatDelta{Before: 75, After: 50, Delta: -25},
		NewFailures:         []model.TestChange{{Suite: "pkg", Name: "TestBad", PreviousStatus: model.StatusPassed, CurrentStatus: model.StatusFailed}},
	}
	r.Quality.History = sampleHistory()
	r.Quality.Changes = sampleChanges()
	if _, err := Write(r, FormatMarkdown, dir, "/repo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Baseline comparison") || !strings.Contains(string(data), "New failures") {
		t.Errorf("markdown missing comparison:\n%s", data)
	}
	if !strings.Contains(string(data), "Run history and flakiness") || !strings.Contains(string(data), "Flaky candidates") {
		t.Errorf("markdown missing history analysis:\n%s", data)
	}
	if !strings.Contains(string(data), "Changed-code coverage") || !strings.Contains(string(data), "L2") {
		t.Errorf("markdown missing changed-line analysis:\n%s", data)
	}
}

func TestWriteStdout(t *testing.T) {
	var b strings.Builder
	r := sampleReport()
	r.Quality.History = sampleHistory()
	r.Quality.Changes = sampleChanges()
	WriteStdout(r, &b)
	out := b.String()
	if !strings.Contains(out, "result: FAIL") {
		t.Errorf("stdout missing FAIL result:\n%s", out)
	}
	if !strings.Contains(out, "TestBad") {
		t.Errorf("stdout missing failing test name:\n%s", out)
	}
	if !strings.Contains(out, "history: 3 runs (1 flaky") {
		t.Errorf("stdout missing history summary:\n%s", out)
	}
	if !strings.Contains(out, "changes: 50.0% vs main") {
		t.Errorf("stdout missing changed-line summary:\n%s", out)
	}
}

func TestMarkdownEscapesTableContentAndTruncatesUTF8Safely(t *testing.T) {
	if got := truncate("가나다", 2); got != "가나…" {
		t.Fatalf("UTF-8 truncate = %q", got)
	}
	if got := markdownCell("a|b\n<script>"); got != `a\|b &lt;script&gt;` {
		t.Fatalf("markdown cell = %q", got)
	}
	if got := markdownInline("a`b"); got != "a&#96;b" {
		t.Fatalf("markdown inline = %q", got)
	}
}

func sampleHistory() *model.QualityHistory {
	return &model.QualityHistory{
		Runs:            3,
		QualityScore:    model.TrendMetric{First: 80, Last: 60, Delta: -20, Slope: -10, Direction: "decreasing"},
		LineCoveragePct: model.TrendMetric{First: 75, Last: 50, Delta: -25, Slope: -12.5, Direction: "decreasing"},
		TestDurationMs:  model.TrendMetric{First: 100, Last: 300, Delta: 200, Slope: 100, Direction: "increasing"},
		Failures:        model.TrendMetric{First: 0, Last: 1, Delta: 1, Slope: 0.5, Direction: "increasing"},
		Samples: []model.HistorySample{
			{GeneratedAt: time.Unix(10, 0).UTC(), QualityScore: 80, LineCoveragePct: 75, Tests: 2, DurationMs: 100},
			{GeneratedAt: time.Unix(20, 0).UTC(), QualityScore: 70, LineCoveragePct: 60, Tests: 2, Failures: 1, DurationMs: 200},
			{GeneratedAt: time.Unix(30, 0).UTC(), QualityScore: 60, LineCoveragePct: 50, Tests: 2, Failures: 1, DurationMs: 300},
		},
		FlakyTests: []model.FlakyTest{{Language: "go", Suite: "pkg", Name: "TestBad", Observations: 3, Passed: 2, Failed: 1, FlakeRatePct: 33.3, RecentStatuses: []string{model.StatusPassed, model.StatusFailed, model.StatusPassed}}},
	}
}

func sampleChanges() *model.ChangeAnalysis {
	return &model.ChangeAnalysis{
		Base: "main", FilesChanged: 2, SourceFilesChanged: 1, ChangedLines: 3,
		CoverableLines: 2, CoveredLines: 1, UncoveredLines: 1, UnmappedLines: 1, CoveragePct: 50,
		Files: []model.ChangedFileCoverage{{Path: "a.go", CoveragePath: "a.go", Language: "go", ChangedLines: 3, CoverableLines: 2, CoveredLines: 1, UncoveredLines: 1, UnmappedLines: 1, CoveragePct: 50, CoverageReported: true, UncoveredLineNumbers: []int{2}}},
	}
}

func TestMarkdownIncludesRiskHotspots(t *testing.T) {
	r := sampleReport()
	r.Risk = &model.RiskAnalysis{Base: "90 days", Files: []model.RiskFile{{Path: "hot.go", Churn: 7, Complexity: 120, CoveragePct: 12.5, RiskScore: 0.81}}}
	dir := t.TempDir()
	if _, err := Write(r, FormatMarkdown, dir, "/repo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "Risk hotspots") || !strings.Contains(out, "hot.go") || !strings.Contains(out, "0.81") {
		t.Errorf("markdown missing risk section:\n%s", out)
	}
	r.Risk = nil
	if _, err := Write(r, FormatMarkdown, dir, "/repo"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "report.md"))
	if strings.Contains(string(data), "Risk hotspots") {
		t.Error("risk section should be omitted when nil")
	}
}

func TestHTMLIncludesRiskHotspots(t *testing.T) {
	r := sampleReport()
	r.Risk = &model.RiskAnalysis{Base: "90 days", Files: []model.RiskFile{{Path: "hot.go", Churn: 7, Complexity: 120, CoveragePct: 12.5, RiskScore: 0.81}}}
	dir := t.TempDir()
	if _, err := Write(r, FormatHTML, dir, "/repo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "insights.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Risk hotspots") || !strings.Contains(string(data), "hot.go") {
		t.Errorf("html missing risk section")
	}
}
