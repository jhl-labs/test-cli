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

func TestNormalizeMergesDuplicateCoverageWithoutDoubleCounting(t *testing.T) {
	r := &Report{Coverage: CoverageReport{Files: []FileCoverage{
		{Path: "./src/a.go", Language: "go", Lines: Metric{Covered: 1, Total: 2}, Branches: Metric{Covered: 1, Total: 2}, LineHits: []LineHit{{Line: 1, Hits: 1}, {Line: 2, Hits: 0}}},
		{Path: "src/a.go", Language: "go", Lines: Metric{Covered: 1, Total: 2}, Branches: Metric{Covered: 1, Total: 2}, LineHits: []LineHit{{Line: 2, Hits: 3}, {Line: 3, Hits: 0}}},
	}}}
	r.Normalize()

	if len(r.Coverage.Files) != 1 {
		t.Fatalf("files = %d, want one merged file", len(r.Coverage.Files))
	}
	f := r.Coverage.Files[0]
	if f.Lines.Covered != 2 || f.Lines.Total != 3 {
		t.Errorf("merged lines = %d/%d, want 2/3", f.Lines.Covered, f.Lines.Total)
	}
	if f.Branches.Covered != 1 || f.Branches.Total != 2 {
		t.Errorf("duplicate branches were summed: %d/%d", f.Branches.Covered, f.Branches.Total)
	}
	if r.Coverage.Summary.Lines.Total != 3 {
		t.Errorf("summary double counted lines: %+v", r.Coverage.Summary.Lines)
	}
}

func TestNormalizeRejectsInvalidPassingEvidence(t *testing.T) {
	r := &Report{
		Languages: []string{"go", "", "go", " python "},
		Test: TestReport{Suites: []TestSuite{{
			Name:  "invalid",
			Cases: []TestCase{{Name: "missing-status", DurationMs: -1}},
		}}},
		Coverage: CoverageReport{Files: []FileCoverage{{
			Path:     "././app.go",
			Branches: Metric{Covered: 4, Total: 2},
			LineHits: []LineHit{{Line: 0, Hits: 10}, {Line: 1, Hits: -5}, {Line: 2, Hits: 2}},
		}}},
	}

	r.Normalize()

	if r.Test.Summary.Errors != 1 || r.Test.Summary.Passing() {
		t.Fatalf("invalid status was treated as passing: %+v", r.Test.Summary)
	}
	if got := r.Test.Suites[0].Cases[0]; got.Status != StatusError || got.DurationMs != 0 || got.Message == "" {
		t.Fatalf("invalid test case was not normalized: %+v", got)
	}
	if len(r.Languages) != 2 || r.Languages[0] != "go" || r.Languages[1] != "python" {
		t.Fatalf("languages = %v", r.Languages)
	}
	f := r.Coverage.Files[0]
	if f.Path != "app.go" || f.Lines.Covered != 1 || f.Lines.Total != 1 {
		t.Fatalf("line coverage was not sanitized: %+v", f)
	}
	if f.Branches.Covered != 2 || f.Branches.Total != 2 || f.Branches.Pct != 100 {
		t.Fatalf("branch metric was not clamped: %+v", f.Branches)
	}
}
