package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	return r
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

func TestWriteHTMLProducesHeatmap(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
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
	if _, err := os.Stat(filepath.Join(dir, "coverage", "index.html")); err != nil {
		t.Errorf("coverage index missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "coverage", "a.go.html")); err != nil {
		t.Errorf("per-file heatmap missing: %v", err)
	}
}

func TestWriteStdout(t *testing.T) {
	var b strings.Builder
	WriteStdout(sampleReport(), &b)
	out := b.String()
	if !strings.Contains(out, "result: FAIL") {
		t.Errorf("stdout missing FAIL result:\n%s", out)
	}
	if !strings.Contains(out, "TestBad") {
		t.Errorf("stdout missing failing test name:\n%s", out)
	}
}
