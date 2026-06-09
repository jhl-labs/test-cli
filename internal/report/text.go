package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// WriteStdout prints a concise, automation-friendly summary, modeled on the
// security-cli stdout layout (header, totals, per-language, failures, coverage).
func WriteStdout(r *model.Report, w io.Writer) {
	ts := r.Test.Summary
	fmt.Fprintf(w, "test-cli %s\n", r.ToolVersion)
	fmt.Fprintf(w, "root: %s\n", r.Root)
	if len(r.Languages) > 0 {
		fmt.Fprintf(w, "languages: %s\n", strings.Join(r.Languages, ", "))
	}
	status := "PASS"
	if !ts.Passing() {
		status = "FAIL"
	}
	fmt.Fprintf(w, "result: %s\n", status)
	fmt.Fprintf(w, "tests: %d (passed %d, failed %d, errors %d, skipped %d) in %.2fs\n",
		ts.Total, ts.Passed, ts.Failed, ts.Errors, ts.Skipped, ts.DurationMs/1000)
	cov := r.Coverage.Summary
	if cov.Lines.Total > 0 {
		fmt.Fprintf(w, "coverage: lines %s (%d/%d)", pct1(cov.Lines.Pct), cov.Lines.Covered, cov.Lines.Total)
		if cov.Branches.Total > 0 {
			fmt.Fprintf(w, ", branches %s (%d/%d)", pct1(cov.Branches.Pct), cov.Branches.Covered, cov.Branches.Total)
		}
		fmt.Fprintln(w)
	}

	// Per-language breakdown.
	byLang := groupByLanguage(r)
	if len(byLang) > 1 {
		fmt.Fprintln(w, "\nby language:")
		langs := make([]string, 0, len(byLang))
		for l := range byLang {
			langs = append(langs, l)
		}
		sort.Strings(langs)
		for _, l := range langs {
			s := byLang[l]
			fmt.Fprintf(w, "- %-12s tests %d (failed %d) cov %s\n", l, s.tests.Total, s.tests.Failed+s.tests.Errors, pct1(s.linePct()))
		}
	}

	// Failures.
	fails := failedCases(r)
	if len(fails) > 0 {
		fmt.Fprintf(w, "\nfailures (%d):\n", len(fails))
		limit := 25
		for i, fc := range fails {
			if i >= limit {
				fmt.Fprintf(w, "  ... and %d more\n", len(fails)-limit)
				break
			}
			msg := fc.Case.Message
			if msg == "" {
				msg = strings.ToUpper(fc.Case.Status)
			}
			fmt.Fprintf(w, "  ✗ [%s] %s :: %s\n      %s\n", fc.Language, fc.Suite, fc.Case.Name, truncate(msg, 200))
		}
	}

	// Lowest-coverage files.
	worst := worstFiles(r.Coverage.Files, 5)
	if len(worst) > 0 && cov.Lines.Pct < 100 {
		fmt.Fprintln(w, "\nlowest coverage:")
		for _, f := range worst {
			fmt.Fprintf(w, "  %-6s %s\n", pct1(f.Lines.Pct), f.Path)
		}
	}

	for _, m := range r.Messages {
		fmt.Fprintf(w, "note: %s\n", m)
	}
}

func writeMarkdown(r *model.Report, path string) error {
	var b strings.Builder
	ts := r.Test.Summary
	cov := r.Coverage.Summary

	badge := "✅ passing"
	if !ts.Passing() {
		badge = "❌ failing"
	}
	b.WriteString("# Test Report\n\n")
	fmt.Fprintf(&b, "**Status:** %s &nbsp;·&nbsp; **Tool:** `test-cli %s` &nbsp;·&nbsp; **Generated:** %s\n\n",
		badge, r.ToolVersion, r.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	if len(r.Languages) > 0 {
		fmt.Fprintf(&b, "**Languages:** %s\n\n", strings.Join(r.Languages, ", "))
	}

	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Tests | %d |\n", ts.Total)
	fmt.Fprintf(&b, "| Passed | %d |\n", ts.Passed)
	fmt.Fprintf(&b, "| Failed | %d |\n", ts.Failed)
	fmt.Fprintf(&b, "| Errors | %d |\n", ts.Errors)
	fmt.Fprintf(&b, "| Skipped | %d |\n", ts.Skipped)
	fmt.Fprintf(&b, "| Pass rate | %s |\n", pct1(ts.PassRate()*100))
	fmt.Fprintf(&b, "| Duration | %.2fs |\n", ts.DurationMs/1000)
	if cov.Lines.Total > 0 {
		fmt.Fprintf(&b, "| Line coverage | %s (%d/%d) |\n", pct1(cov.Lines.Pct), cov.Lines.Covered, cov.Lines.Total)
	}
	if cov.Branches.Total > 0 {
		fmt.Fprintf(&b, "| Branch coverage | %s (%d/%d) |\n", pct1(cov.Branches.Pct), cov.Branches.Covered, cov.Branches.Total)
	}
	b.WriteString("\n")

	byLang := groupByLanguage(r)
	if len(byLang) > 0 {
		b.WriteString("## By language\n\n")
		b.WriteString("| Language | Tests | Failed | Skipped | Line coverage |\n|---|---|---|---|---|\n")
		langs := sortedKeys(byLang)
		for _, l := range langs {
			s := byLang[l]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %s |\n", l, s.tests.Total, s.tests.Failed+s.tests.Errors, s.tests.Skipped, pct1(s.linePct()))
		}
		b.WriteString("\n")
	}

	fails := failedCases(r)
	if len(fails) > 0 {
		fmt.Fprintf(&b, "## Failures (%d)\n\n", len(fails))
		for _, fc := range fails {
			fmt.Fprintf(&b, "- **[%s] %s › %s**\n", fc.Language, fc.Suite, fc.Case.Name)
			if fc.Case.Message != "" {
				fmt.Fprintf(&b, "  - %s\n", truncate(fc.Case.Message, 300))
			}
		}
		b.WriteString("\n")
	}

	worst := worstFiles(r.Coverage.Files, 10)
	if len(worst) > 0 {
		b.WriteString("## Lowest coverage files\n\n")
		b.WriteString("| File | Line coverage |\n|---|---|\n")
		for _, f := range worst {
			fmt.Fprintf(&b, "| `%s` | %s |\n", f.Path, pct1(f.Lines.Pct))
		}
		b.WriteString("\n")
	}

	return writeFile(path, b.String())
}

// --- helpers ---

type langStats struct {
	tests  model.TestSummary
	covCov int
	covTot int
}

func (s langStats) linePct() float64 {
	if s.covTot == 0 {
		return 0
	}
	return float64(s.covCov) / float64(s.covTot) * 100
}

func groupByLanguage(r *model.Report) map[string]*langStats {
	m := map[string]*langStats{}
	get := func(l string) *langStats {
		if l == "" {
			l = "unknown"
		}
		s, ok := m[l]
		if !ok {
			s = &langStats{}
			m[l] = s
		}
		return s
	}
	for _, suite := range r.Test.Suites {
		s := get(suite.Language)
		s.tests.Total += suite.Summary.Total
		s.tests.Passed += suite.Summary.Passed
		s.tests.Failed += suite.Summary.Failed
		s.tests.Errors += suite.Summary.Errors
		s.tests.Skipped += suite.Summary.Skipped
	}
	for _, f := range r.Coverage.Files {
		s := get(f.Language)
		s.covCov += f.Lines.Covered
		s.covTot += f.Lines.Total
	}
	return m
}

func sortedKeys(m map[string]*langStats) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
