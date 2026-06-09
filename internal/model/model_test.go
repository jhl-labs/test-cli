package model

import "testing"

func TestNormalizeRollups(t *testing.T) {
	r := &Report{
		Test: TestReport{Suites: []TestSuite{{
			Name:     "s1",
			Language: "go",
			Cases: []TestCase{
				{Name: "a", Status: StatusPassed, DurationMs: 10},
				{Name: "b", Status: StatusFailed, DurationMs: 5},
				{Name: "c", Status: StatusSkipped},
			},
		}}},
		Coverage: CoverageReport{Files: []FileCoverage{
			{Path: "b.go", Lines: Metric{Covered: 1, Total: 4}},
			{Path: "a.go", Lines: Metric{Covered: 3, Total: 4}},
		}},
	}
	r.Normalize()

	if r.Schema != SchemaID {
		t.Errorf("schema = %q", r.Schema)
	}
	ts := r.Test.Summary
	if ts.Total != 3 || ts.Passed != 1 || ts.Failed != 1 || ts.Skipped != 1 {
		t.Errorf("summary = %+v", ts)
	}
	if ts.Passing() {
		t.Error("expected not passing")
	}
	if ts.DurationMs != 15 {
		t.Errorf("duration = %v, want 15", ts.DurationMs)
	}
	cs := r.Coverage.Summary
	if cs.Lines.Covered != 4 || cs.Lines.Total != 8 {
		t.Errorf("coverage = %d/%d", cs.Lines.Covered, cs.Lines.Total)
	}
	if cs.Lines.Pct != 50 {
		t.Errorf("pct = %v, want 50", cs.Lines.Pct)
	}
	// Files sorted by path.
	if r.Coverage.Files[0].Path != "a.go" {
		t.Errorf("files not sorted: %s first", r.Coverage.Files[0].Path)
	}
}

func TestPassRate(t *testing.T) {
	s := TestSummary{Total: 10, Passed: 8, Failed: 0, Skipped: 2}
	if got := s.PassRate(); got != 1.0 {
		t.Errorf("PassRate = %v, want 1.0 (8 of 8 executed)", got)
	}
}
