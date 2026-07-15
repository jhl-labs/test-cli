// Package model defines the normalized, language-agnostic schema for test and
// coverage results. Every supported language (python, typescript, go, rust,
// csharp, java) is ingested from its native tool output and projected onto
// these structures so that downstream reports are identical regardless of the
// source toolchain.
package model

import (
	"math"
	"sort"
	"strings"
	"time"
)

// SchemaID identifies the version of the normalized JSON schema. Consumers
// (AI agents, dashboards) should branch on this when the shape changes.
const SchemaID = "test-cli/report@2"

// Status values for an individual test case.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
	StatusError   = "error"
)

// Report is the top-level normalized document emitted as report.json. It is the
// single source of truth that every other format (JUnit, Cobertura, Markdown,
// HTML) is rendered from.
type Report struct {
	Schema      string         `json:"schema"`
	ToolVersion string         `json:"toolVersion"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Root        string         `json:"root"`
	Languages   []string       `json:"languages"`
	Test        TestReport     `json:"test"`
	Coverage    CoverageReport `json:"coverage"`
	Quality     QualityReport  `json:"quality"`
	Messages    []string       `json:"messages,omitempty"`
}

// TestReport aggregates all test suites discovered across all languages.
type TestReport struct {
	Summary TestSummary `json:"summary"`
	Suites  []TestSuite `json:"suites"`
}

// TestSummary holds rolled-up counts. It is used both at the report level and
// per-suite.
type TestSummary struct {
	Total      int     `json:"total"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	Skipped    int     `json:"skipped"`
	Errors     int     `json:"errors"`
	DurationMs float64 `json:"durationMs"`
}

// Passing reports whether the run is green (no failures or errors).
func (s TestSummary) Passing() bool { return s.Failed == 0 && s.Errors == 0 }

// PassRate returns the fraction of executed (non-skipped) tests that passed.
func (s TestSummary) PassRate() float64 {
	executed := s.Total - s.Skipped
	if executed <= 0 {
		return 1
	}
	return float64(s.Passed) / float64(executed)
}

// TestSuite is a group of test cases, typically one source file or test class.
type TestSuite struct {
	Name       string      `json:"name"`
	Language   string      `json:"language"`
	File       string      `json:"file,omitempty"`
	DurationMs float64     `json:"durationMs"`
	Summary    TestSummary `json:"summary"`
	Cases      []TestCase  `json:"cases"`
}

// TestCase is a single executed test.
type TestCase struct {
	Name       string  `json:"name"`
	Classname  string  `json:"classname,omitempty"`
	Status     string  `json:"status"`
	DurationMs float64 `json:"durationMs"`
	Message    string  `json:"message,omitempty"`
	Detail     string  `json:"detail,omitempty"`
}

// CoverageReport aggregates line and branch coverage across all files.
type CoverageReport struct {
	Summary CoverageSummary `json:"summary"`
	Files   []FileCoverage  `json:"files"`
}

// CoverageSummary holds rolled-up coverage metrics.
type CoverageSummary struct {
	Lines    Metric `json:"lines"`
	Branches Metric `json:"branches"`
}

// Metric is a covered/total pair with a derived percentage.
type Metric struct {
	Covered int     `json:"covered"`
	Total   int     `json:"total"`
	Pct     float64 `json:"pct"`
}

// Recompute refreshes Pct from Covered/Total.
func (m *Metric) Recompute() {
	if m.Total < 0 {
		m.Total = 0
	}
	if m.Covered < 0 {
		m.Covered = 0
	}
	if m.Covered > m.Total {
		m.Covered = m.Total
	}
	if m.Total <= 0 {
		m.Pct = 0
		return
	}
	m.Pct = float64(m.Covered) / float64(m.Total) * 100
}

// FileCoverage holds per-file coverage including per-line hit counts used to
// render the code-cov style heatmap.
type FileCoverage struct {
	Path     string    `json:"path"`
	Language string    `json:"language,omitempty"`
	Lines    Metric    `json:"lines"`
	Branches Metric    `json:"branches"`
	LineHits []LineHit `json:"lineHits,omitempty"`
}

// LineHit records the execution count for a single source line. Hits < 0 means
// the line is not instrumented (not coverable) and is rendered neutral.
type LineHit struct {
	Line int `json:"line"`
	Hits int `json:"hits"`
}

// Normalize recomputes every derived field (summaries, percentages) bottom-up
// so callers can build a Report by appending raw suites/files and finalize in
// one pass. It also sorts suites and files deterministically for stable output.
func (r *Report) Normalize() {
	if r.Schema == "" {
		r.Schema = SchemaID
	}

	// Test rollups.
	var ts TestSummary
	for i := range r.Test.Suites {
		s := &r.Test.Suites[i]
		var ss TestSummary
		for j := range s.Cases {
			c := &s.Cases[j]
			if c.DurationMs < 0 || math.IsNaN(c.DurationMs) || math.IsInf(c.DurationMs, 0) {
				c.DurationMs = 0
			}
			ss.Total++
			ss.DurationMs += c.DurationMs
			switch c.Status {
			case StatusPassed:
				ss.Passed++
			case StatusFailed:
				ss.Failed++
			case StatusSkipped:
				ss.Skipped++
			case StatusError:
				ss.Errors++
			default:
				invalid := c.Status
				c.Status = StatusError
				ss.Errors++
				if c.Message == "" {
					if invalid == "" {
						c.Message = "test status was not reported"
					} else {
						c.Message = "unknown test status: " + invalid
					}
				}
			}
		}
		if s.DurationMs < 0 || math.IsNaN(s.DurationMs) || math.IsInf(s.DurationMs, 0) {
			s.DurationMs = 0
		}
		if s.DurationMs == 0 {
			s.DurationMs = ss.DurationMs
		}
		s.Summary = ss
		ts.Total += ss.Total
		ts.Passed += ss.Passed
		ts.Failed += ss.Failed
		ts.Skipped += ss.Skipped
		ts.Errors += ss.Errors
		ts.DurationMs += s.DurationMs
	}
	r.Test.Summary = ts

	// Coverage rollups.
	r.Coverage.Files = mergeCoverageFiles(r.Coverage.Files)
	var cs CoverageSummary
	for i := range r.Coverage.Files {
		f := &r.Coverage.Files[i]
		f.Lines.Recompute()
		f.Branches.Recompute()
		cs.Lines.Covered += f.Lines.Covered
		cs.Lines.Total += f.Lines.Total
		cs.Branches.Covered += f.Branches.Covered
		cs.Branches.Total += f.Branches.Total
	}
	cs.Lines.Recompute()
	cs.Branches.Recompute()
	r.Coverage.Summary = cs

	sort.SliceStable(r.Test.Suites, func(i, j int) bool {
		a, b := r.Test.Suites[i], r.Test.Suites[j]
		if a.Language != b.Language {
			return a.Language < b.Language
		}
		return a.Name < b.Name
	})
	sort.SliceStable(r.Coverage.Files, func(i, j int) bool {
		return r.Coverage.Files[i].Path < r.Coverage.Files[j].Path
	})
	languageSet := make(map[string]bool, len(r.Languages))
	languages := r.Languages[:0]
	for _, language := range r.Languages {
		language = strings.TrimSpace(language)
		if language == "" || languageSet[language] {
			continue
		}
		languageSet[language] = true
		languages = append(languages, language)
	}
	r.Languages = languages
	sort.Strings(r.Languages)
}

// mergeCoverageFiles prevents overlapping artifacts (for example multiple
// .NET test projects or both Cobertura and LCOV) from double-counting the same
// executable lines. Per-line hits are unioned using the maximum observed hit
// count. Branch formats do not expose stable branch identities, so the most
// complete branch metric is retained conservatively instead of summed.
func mergeCoverageFiles(files []FileCoverage) []FileCoverage {
	type accumulator struct {
		file FileCoverage
		hits map[int]int
	}
	byPath := map[string]*accumulator{}
	var order []string
	for _, incoming := range files {
		path := normalizeCoveragePath(incoming.Path)
		a := byPath[path]
		if a == nil {
			a = &accumulator{file: FileCoverage{Path: path, Language: incoming.Language}, hits: map[int]int{}}
			byPath[path] = a
			order = append(order, path)
		}
		if a.file.Language == "" {
			a.file.Language = incoming.Language
		}
		if betterMetric(incoming.Lines, a.file.Lines) {
			a.file.Lines = incoming.Lines
		}
		if betterMetric(incoming.Branches, a.file.Branches) {
			a.file.Branches = incoming.Branches
		}
		for _, hit := range incoming.LineHits {
			if hit.Line <= 0 {
				continue
			}
			if hit.Hits < -1 {
				hit.Hits = -1
			}
			if previous, ok := a.hits[hit.Line]; !ok || hit.Hits > previous {
				a.hits[hit.Line] = hit.Hits
			}
		}
	}

	out := make([]FileCoverage, 0, len(order))
	for _, path := range order {
		a := byPath[path]
		if len(a.hits) > 0 {
			lines := make([]int, 0, len(a.hits))
			for line := range a.hits {
				lines = append(lines, line)
			}
			sort.Ints(lines)
			a.file.LineHits = make([]LineHit, 0, len(lines))
			a.file.Lines = Metric{}
			for _, line := range lines {
				hits := a.hits[line]
				a.file.LineHits = append(a.file.LineHits, LineHit{Line: line, Hits: hits})
				if hits < 0 {
					continue
				}
				a.file.Lines.Total++
				if hits > 0 {
					a.file.Lines.Covered++
				}
			}
		}
		out = append(out, a.file)
	}
	return out
}

func normalizeCoveragePath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	return value
}

func betterMetric(candidate, current Metric) bool {
	return candidate.Total > current.Total || (candidate.Total == current.Total && candidate.Covered > current.Covered)
}
