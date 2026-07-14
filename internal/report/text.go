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
	if ts.Total-ts.Skipped <= 0 {
		status = "NO TESTS"
	} else if !ts.Passing() {
		status = "FAIL"
	}
	fmt.Fprintf(w, "result: %s\n", status)
	fmt.Fprintf(w, "quality: %d/100 (grade %s, %s risk; findings %d critical, %d high, %d medium)\n",
		r.Quality.Score, r.Quality.Grade, r.Quality.Risk, r.Quality.Summary.Critical, r.Quality.Summary.High, r.Quality.Summary.Medium)
	if c := r.Quality.Comparison; c != nil {
		status := "NO MATERIAL REGRESSION"
		if c.Regressed {
			status = "REGRESSED"
		}
		fmt.Fprintf(w, "baseline: %s (score %+d, line coverage %+.1fpt, failures %+d; new %d, resolved %d)\n",
			status, c.Score.Delta, c.LineCoveragePct.Delta, c.Failures.Delta, len(c.NewFailures), len(c.ResolvedFailures))
	}
	if changes := r.Quality.Changes; changes != nil {
		coverage := "n/a"
		if changes.CoverableLines > 0 {
			coverage = pct1(changes.CoveragePct)
		}
		fmt.Fprintf(w, "changes: %s vs %s (%d source files, %d changed lines, %d/%d executable lines covered; %d file(s) without evidence)\n",
			coverage, changes.Base, changes.SourceFilesChanged, changes.ChangedLines, changes.CoveredLines, changes.CoverableLines, changes.FilesWithoutCoverage)
	}
	if history := r.Quality.History; history != nil {
		fmt.Fprintf(w, "history: %d runs (%d flaky; score slope %+.2f/run, line coverage %+.2fpt/run, duration %+.0fms/run)\n",
			history.Runs, len(history.FlakyTests), history.QualityScore.Slope, history.LineCoveragePct.Slope, history.TestDurationMs.Slope)
	}
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

	if len(r.Quality.Findings) > 0 {
		fmt.Fprintln(w, "\nquality findings:")
		for i, finding := range r.Quality.Findings {
			if i >= 5 {
				fmt.Fprintf(w, "  ... and %d more (see report.json or insights.html)\n", len(r.Quality.Findings)-i)
				break
			}
			fmt.Fprintf(w, "  %-8s %-10s %s — %s\n", strings.ToUpper(finding.Severity), finding.ID, finding.Title, truncate(finding.Evidence, 100))
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
	if ts.Total-ts.Skipped <= 0 {
		badge = "⚠️ no executable tests"
	} else if !ts.Passing() {
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

	b.WriteString("## Quality assessment\n\n")
	fmt.Fprintf(&b, "**Score:** %d/100 (grade **%s**) &nbsp;·&nbsp; **Risk:** %s &nbsp;·&nbsp; **Findings:** %d critical, %d high, %d medium\n\n",
		r.Quality.Score, r.Quality.Grade, r.Quality.Risk, r.Quality.Summary.Critical, r.Quality.Summary.High, r.Quality.Summary.Medium)
	b.WriteString("| Dimension | Score | Weight | Evidence |\n|---|---:|---:|---|\n")
	for _, d := range r.Quality.Dimensions {
		fmt.Fprintf(&b, "| %s | %d/100 | %d%% | %s |\n", markdownCell(d.Name), d.Score, d.Weight, markdownCell(d.Detail))
	}
	b.WriteString("\n")
	if len(r.Quality.Findings) > 0 {
		b.WriteString("### Diagnostic findings\n\n")
		b.WriteString("| Severity | Rule | Finding | Evidence | Recommendation |\n|---|---|---|---|---|\n")
		for _, f := range r.Quality.Findings {
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", markdownCell(strings.ToUpper(f.Severity)), markdownInline(f.ID), markdownCell(f.Title), markdownCell(f.Evidence), markdownCell(f.Recommendation))
		}
		b.WriteString("\n")
	}
	if c := r.Quality.Comparison; c != nil {
		b.WriteString("## Baseline comparison\n\n")
		state := "no material regression"
		if c.Regressed {
			state = "**regressed**"
		}
		fmt.Fprintf(&b, "**Result:** %s &nbsp;·&nbsp; **Baseline generated:** %s\n\n", state, c.BaselineGeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
		b.WriteString("| Metric | Baseline | Current | Delta |\n|---|---:|---:|---:|\n")
		fmt.Fprintf(&b, "| Quality score | %d | %d | %+d |\n", c.Score.Before, c.Score.After, c.Score.Delta)
		fmt.Fprintf(&b, "| Tests | %d | %d | %+d |\n", c.Tests.Before, c.Tests.After, c.Tests.Delta)
		fmt.Fprintf(&b, "| Failures | %d | %d | %+d |\n", c.Failures.Before, c.Failures.After, c.Failures.Delta)
		fmt.Fprintf(&b, "| Line coverage | %.1f%% | %.1f%% | %+.1f pt |\n", c.LineCoveragePct.Before, c.LineCoveragePct.After, c.LineCoveragePct.Delta)
		fmt.Fprintf(&b, "| Branch coverage | %.1f%% | %.1f%% | %+.1f pt |\n", c.BranchCoveragePct.Before, c.BranchCoveragePct.After, c.BranchCoveragePct.Delta)
		fmt.Fprintf(&b, "| Duration | %.0f ms | %.0f ms | %+.0f ms |\n\n", c.DurationMs.Before, c.DurationMs.After, c.DurationMs.Delta)
		if len(c.NewFailures) > 0 {
			b.WriteString("### New failures\n\n")
			for _, change := range c.NewFailures {
				fmt.Fprintf(&b, "- **[%s] %s › %s**: %s → %s\n", markdownText(change.Language), markdownText(change.Suite), markdownText(change.Name), markdownText(firstNonEmptyText(change.PreviousStatus, "new")), markdownText(change.CurrentStatus))
			}
			b.WriteString("\n")
		}
		if len(c.CoverageRegressions) > 0 {
			b.WriteString("### File coverage regressions\n\n| File | Baseline | Current | Delta |\n|---|---:|---:|---:|\n")
			for _, change := range c.CoverageRegressions {
				fmt.Fprintf(&b, "| `%s` | %.1f%% | %.1f%% | %+.1f pt |\n", markdownInline(change.Path), change.BeforePct, change.AfterPct, change.DeltaPct)
			}
			b.WriteString("\n")
		}
	}
	if changes := r.Quality.Changes; changes != nil {
		b.WriteString("## Changed-code coverage\n\n")
		coverage := "N/A"
		if changes.CoverableLines > 0 {
			coverage = pct1(changes.CoveragePct)
		}
		fmt.Fprintf(&b, "**Base:** `%s` &nbsp;·&nbsp; **Patch coverage:** %s &nbsp;·&nbsp; **Changed production lines:** %d across %d files\n\n", markdownInline(changes.Base), coverage, changes.ChangedLines, changes.SourceFilesChanged)
		b.WriteString("| File | Changed | Executable | Covered | Patch coverage | Uncovered changed lines | Unmapped |\n|---|---:|---:|---:|---:|---|---:|\n")
		for _, file := range changes.Files {
			fileCoverage := "no line data"
			if file.CoverageReported {
				fileCoverage = pct1(file.CoveragePct)
			}
			lineNumbers := "—"
			if len(file.UncoveredLineNumbers) > 0 {
				limit := min(12, len(file.UncoveredLineNumbers))
				parts := make([]string, 0, limit)
				for _, line := range file.UncoveredLineNumbers[:limit] {
					parts = append(parts, fmt.Sprintf("L%d", line))
				}
				lineNumbers = strings.Join(parts, ", ")
				if file.UncoveredLines > limit {
					lineNumbers += " …"
				}
			}
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %s | %s | %d |\n", markdownInline(file.Path), file.ChangedLines, file.CoverableLines, file.CoveredLines, fileCoverage, lineNumbers, file.UnmappedLines)
		}
		b.WriteString("\n> Patch coverage includes only changed executable production lines. Files without line-level coverage evidence are reported separately.\n\n")
	}
	if history := r.Quality.History; history != nil {
		b.WriteString("## Run history and flakiness\n\n")
		fmt.Fprintf(&b, "**Window:** %d runs &nbsp;·&nbsp; **Flaky candidates:** %d\n\n", history.Runs, len(history.FlakyTests))
		b.WriteString("| Metric | First | Current | Delta | Slope / run | Direction |\n|---|---:|---:|---:|---:|---|\n")
		fmt.Fprintf(&b, "| Quality score | %.0f | %.0f | %+.0f | %+.2f | %s |\n", history.QualityScore.First, history.QualityScore.Last, history.QualityScore.Delta, history.QualityScore.Slope, history.QualityScore.Direction)
		fmt.Fprintf(&b, "| Line coverage | %.1f%% | %.1f%% | %+.1f pt | %+.2f pt | %s |\n", history.LineCoveragePct.First, history.LineCoveragePct.Last, history.LineCoveragePct.Delta, history.LineCoveragePct.Slope, history.LineCoveragePct.Direction)
		fmt.Fprintf(&b, "| Duration | %.0f ms | %.0f ms | %+.0f ms | %+.0f ms | %s |\n", history.TestDurationMs.First, history.TestDurationMs.Last, history.TestDurationMs.Delta, history.TestDurationMs.Slope, history.TestDurationMs.Direction)
		fmt.Fprintf(&b, "| Tests | %.0f | %.0f | %+.0f | %+.2f | %s |\n", history.Tests.First, history.Tests.Last, history.Tests.Delta, history.Tests.Slope, history.Tests.Direction)
		fmt.Fprintf(&b, "| Failures | %.0f | %.0f | %+.0f | %+.2f | %s |\n\n", history.Failures.First, history.Failures.Last, history.Failures.Delta, history.Failures.Slope, history.Failures.Direction)
		if len(history.Samples) > 0 {
			b.WriteString("### Run samples\n\n| Generated | Score | Coverage | Tests | Failures | Duration |\n|---|---:|---:|---:|---:|---:|\n")
			for _, sample := range history.Samples {
				fmt.Fprintf(&b, "| %s | %d | %.1f%% | %d | %d | %.0f ms |\n", sample.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC"), sample.QualityScore, sample.LineCoveragePct, sample.Tests, sample.Failures, sample.DurationMs)
			}
			b.WriteString("\n")
		}
		if len(history.FlakyTests) > 0 {
			b.WriteString("### Flaky candidates\n\n| Test | Status history | Observations | Pass / fail | Failure rate |\n|---|---|---:|---:|---:|\n")
			for _, test := range history.FlakyTests {
				fmt.Fprintf(&b, "| `[%s] %s › %s` | %s | %d | %d / %d | %.1f%% |\n", markdownInline(test.Language), markdownInline(test.Suite), markdownInline(test.Name), markdownCell(strings.Join(test.RecentStatuses, " → ")), test.Observations, test.Passed, test.Failed, test.FlakeRatePct)
			}
			b.WriteString("\n")
		}
		b.WriteString("> Flaky candidates require at least three observations and both passing and failing/error states.\n\n")
	}

	byLang := groupByLanguage(r)
	if len(byLang) > 0 {
		b.WriteString("## By language\n\n")
		b.WriteString("| Language | Tests | Failed | Skipped | Line coverage |\n|---|---|---|---|---|\n")
		langs := sortedKeys(byLang)
		for _, l := range langs {
			s := byLang[l]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %s |\n", markdownCell(l), s.tests.Total, s.tests.Failed+s.tests.Errors, s.tests.Skipped, pct1(s.linePct()))
		}
		b.WriteString("\n")
	}

	fails := failedCases(r)
	if len(fails) > 0 {
		fmt.Fprintf(&b, "## Failures (%d)\n\n", len(fails))
		for _, fc := range fails {
			fmt.Fprintf(&b, "- **[%s] %s › %s**\n", markdownText(fc.Language), markdownText(fc.Suite), markdownText(fc.Case.Name))
			if fc.Case.Message != "" {
				fmt.Fprintf(&b, "  - %s\n", markdownText(truncate(fc.Case.Message, 300)))
			}
		}
		b.WriteString("\n")
	}

	worst := worstFiles(r.Coverage.Files, 10)
	if len(worst) > 0 {
		b.WriteString("## Lowest coverage files\n\n")
		b.WriteString("| File | Line coverage |\n|---|---|\n")
		for _, f := range worst {
			fmt.Fprintf(&b, "| `%s` | %s |\n", markdownInline(f.Path), pct1(f.Lines.Pct))
		}
		b.WriteString("\n")
	}

	if len(r.Quality.CoverageHotspots) > 0 {
		b.WriteString("## Coverage risk hotspots\n\n")
		b.WriteString("| File | Risk | Coverage | Uncovered lines |\n|---|---|---:|---:|\n")
		for _, h := range r.Quality.CoverageHotspots {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %d |\n", markdownInline(h.Path), markdownCell(h.Risk), pct1(h.CoveragePct), h.UncoveredLines)
		}
		b.WriteString("\n")
	}

	if len(r.Quality.SlowTests) > 0 {
		b.WriteString("## Slow tests (current run)\n\n")
		b.WriteString("| Test | Suite | Duration |\n|---|---|---:|\n")
		limit := min(10, len(r.Quality.SlowTests))
		for _, h := range r.Quality.SlowTests[:limit] {
			fmt.Fprintf(&b, "| `%s` | %s | %.0f ms |\n", markdownInline(h.Name), markdownCell(h.Suite), h.DurationMs)
		}
		b.WriteString("\n")
	}

	st := r.Quality.Static
	if st.SourceFiles+st.TestFiles > 0 {
		b.WriteString("## Static test-code analysis\n\n")
		fmt.Fprintf(&b, "Source: %d files / %d non-empty LOC · Tests: %d files / %d non-empty LOC · Test/source ratio: %.2f\n\n", st.SourceFiles, st.SourceLines, st.TestFiles, st.TestLines, st.TestToSourceRatio)
		if len(st.Smells) > 0 {
			b.WriteString("| Rule | Location | Signal |\n|---|---|---|\n")
			for _, smell := range st.Smells {
				fmt.Fprintf(&b, "| `%s` | `%s:%d` | %s |\n", markdownInline(smell.Rule), markdownInline(smell.Path), smell.Line, markdownCell(smell.Message))
			}
			b.WriteString("\n")
		}
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
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func markdownText(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;",
		"\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`",
		"[", "\\[", "]", "\\]", "\r", "", "\n", " ",
	)
	return replacer.Replace(s)
}

func markdownCell(s string) string {
	s = markdownText(s)
	return strings.ReplaceAll(s, "|", "\\|")
}

func markdownInline(s string) string {
	// Backslash escaping is not interpreted inside Markdown code spans, so use
	// an entity to keep an artifact-supplied backtick from closing the span.
	return strings.ReplaceAll(markdownCell(s), "\\`", "&#96;")
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
