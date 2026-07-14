package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// Compare attaches a baseline comparison and regression findings to current.
// Both reports should already be normalized and evaluated. Two reports are
// sufficient for regression detection, but deliberately not for flakiness.
func Compare(current, baseline *model.Report) {
	c := &model.QualityComparison{
		BaselineGeneratedAt: baseline.GeneratedAt,
		Score:               intDelta(baseline.Quality.Score, current.Quality.Score),
		Tests:               intDelta(baseline.Test.Summary.Total, current.Test.Summary.Total),
		Failures:            intDelta(failureCount(baseline), failureCount(current)),
		DurationMs:          floatDelta(baseline.Test.Summary.DurationMs, current.Test.Summary.DurationMs),
		LineCoveragePct:     floatDelta(baseline.Coverage.Summary.Lines.Pct, current.Coverage.Summary.Lines.Pct),
		BranchCoveragePct:   floatDelta(baseline.Coverage.Summary.Branches.Pct, current.Coverage.Summary.Branches.Pct),
	}
	c.NewFailures, c.ResolvedFailures = compareFailures(current, baseline)
	c.CoverageRegressions, c.CoverageImprovements = compareCoverage(current, baseline)
	c.Regressed = comparisonRegressed(c, current, baseline)
	current.Quality.Comparison = c

	// Replace previous comparison findings when Compare is invoked repeatedly.
	findings := make([]model.QualityFinding, 0, len(current.Quality.Findings))
	for _, finding := range current.Quality.Findings {
		if !strings.HasPrefix(finding.ID, "REG-") {
			findings = append(findings, finding)
		}
	}
	current.Quality.Findings = append(findings, comparisonFindings(c, current, baseline)...)
	sort.SliceStable(current.Quality.Findings, func(i, j int) bool {
		a, b := severityRank(current.Quality.Findings[i].Severity), severityRank(current.Quality.Findings[j].Severity)
		if a != b {
			return a < b
		}
		return current.Quality.Findings[i].ID < current.Quality.Findings[j].ID
	})
	current.Quality.Summary = summarize(current.Quality.Findings)
	current.Quality.Risk = risk(current.Quality.Score, current.Quality.Findings)
}

func compareFailures(current, baseline *model.Report) (newFailures, resolved []model.TestChange) {
	before := testStatuses(baseline)
	after := testStatuses(current)
	for key, state := range after {
		if !isFailure(state.status) {
			continue
		}
		previous, existed := before[key]
		if !existed || !isFailure(previous.status) {
			newFailures = append(newFailures, model.TestChange{Language: state.language, Suite: state.suite, Classname: state.classname, Name: state.name, PreviousStatus: previous.status, CurrentStatus: state.status})
		}
	}
	for key, state := range before {
		if !isFailure(state.status) {
			continue
		}
		currentState, stillExists := after[key]
		if stillExists && !isFailure(currentState.status) {
			resolved = append(resolved, model.TestChange{Language: state.language, Suite: state.suite, Classname: state.classname, Name: state.name, PreviousStatus: state.status, CurrentStatus: currentState.status})
		}
	}
	sortTestChanges(newFailures)
	sortTestChanges(resolved)
	return newFailures, resolved
}

type testState struct {
	language, suite, classname, name, status string
}

func testStatuses(r *model.Report) map[string]testState {
	out := map[string]testState{}
	for _, suite := range r.Test.Suites {
		for _, test := range suite.Cases {
			key := strings.Join([]string{suite.Language, suite.Name, test.Classname, test.Name}, "\x00")
			out[key] = testState{language: suite.Language, suite: suite.Name, classname: test.Classname, name: test.Name, status: test.Status}
		}
	}
	return out
}

func sortTestChanges(changes []model.TestChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		a := changes[i].Language + "\x00" + changes[i].Suite + "\x00" + changes[i].Classname + "\x00" + changes[i].Name
		b := changes[j].Language + "\x00" + changes[j].Suite + "\x00" + changes[j].Classname + "\x00" + changes[j].Name
		return a < b
	})
}

func compareCoverage(current, baseline *model.Report) (regressions, improvements []model.CoverageChange) {
	before := map[string]model.FileCoverage{}
	for _, file := range baseline.Coverage.Files {
		before[comparisonPath(file.Path)] = file
	}
	for _, file := range current.Coverage.Files {
		previous, ok := before[comparisonPath(file.Path)]
		if !ok || previous.Lines.Total == 0 || file.Lines.Total == 0 {
			continue
		}
		delta := file.Lines.Pct - previous.Lines.Pct
		if math.Abs(delta) < 0.1 {
			continue
		}
		change := model.CoverageChange{Path: file.Path, Language: file.Language, BeforePct: previous.Lines.Pct, AfterPct: file.Lines.Pct, DeltaPct: delta}
		if delta < 0 {
			regressions = append(regressions, change)
		} else {
			improvements = append(improvements, change)
		}
	}
	sort.SliceStable(regressions, func(i, j int) bool { return regressions[i].DeltaPct < regressions[j].DeltaPct })
	sort.SliceStable(improvements, func(i, j int) bool { return improvements[i].DeltaPct > improvements[j].DeltaPct })
	if len(regressions) > 25 {
		regressions = regressions[:25]
	}
	if len(improvements) > 25 {
		improvements = improvements[:25]
	}
	return regressions, improvements
}

func comparisonFindings(c *model.QualityComparison, current, baseline *model.Report) []model.QualityFinding {
	var out []model.QualityFinding
	add := func(id, severity, title, detail, evidence, recommendation string) {
		out = append(out, model.QualityFinding{ID: id, Severity: severity, Category: "regression", Title: title, Detail: detail, Evidence: evidence, Recommendation: recommendation})
	}
	if len(c.NewFailures) > 0 {
		add("REG-001", "critical", "New test failures versus baseline", "Tests that were not failing in the baseline now fail or error.", fmt.Sprintf("%d new failure(s)", len(c.NewFailures)), "Treat new failures as the first regression gate; inspect the listed status transitions and failure details.")
	}
	if c.LineCoveragePct.Delta <= -1 {
		severity := "medium"
		if c.LineCoveragePct.Delta <= -5 {
			severity = "high"
		}
		add("REG-002", severity, "Line coverage regressed", "Overall executable-line coverage is lower than the explicit baseline.", fmt.Sprintf("%.1f%% → %.1f%% (%+.1f points)", c.LineCoveragePct.Before, c.LineCoveragePct.After, c.LineCoveragePct.Delta), "Review per-file regressions and add tests for changed behavior before accepting a lower baseline.")
	}
	if current.Coverage.Summary.Branches.Total > 0 && baseline.Coverage.Summary.Branches.Total > 0 && c.BranchCoveragePct.Delta <= -2 {
		severity := "medium"
		if c.BranchCoveragePct.Delta <= -10 {
			severity = "high"
		}
		add("REG-006", severity, "Branch coverage regressed", "Decision-path coverage is lower than the explicit baseline.", fmt.Sprintf("%.1f%% → %.1f%% (%+.1f points)", c.BranchCoveragePct.Before, c.BranchCoveragePct.After, c.BranchCoveragePct.Delta), "Restore alternate, boundary, and error-path tests around the regressed decisions.")
	}
	if c.Score.Delta <= -5 {
		severity := "medium"
		if c.Score.Delta <= -10 {
			severity = "high"
		}
		add("REG-003", severity, "Quality score regressed", "The weighted QA evidence is materially weaker than the baseline.", fmt.Sprintf("%d → %d (%+d)", c.Score.Before, c.Score.After, c.Score.Delta), "Use the dimension deltas and new findings to restore the previous quality level.")
	}
	if durationRegressed(c.DurationMs) {
		increase := 0.0
		if c.DurationMs.Before > 0 {
			increase = c.DurationMs.Delta / c.DurationMs.Before * 100
		}
		add("REG-004", "medium", "Test duration regressed", "The total reported test duration increased materially.", fmt.Sprintf("%.0f ms → %.0f ms (%+.1f%%)", c.DurationMs.Before, c.DurationMs.After, increase), "Inspect slow-test rankings and shared setup before normalizing the slower feedback loop.")
	}
	if len(c.CoverageRegressions) > 0 && c.LineCoveragePct.Delta > -1 {
		add("REG-005", "medium", "File-level coverage regressions", "Overall coverage is stable, but one or more files lost meaningful coverage.", fmt.Sprintf("%d regressed file(s)", len(c.CoverageRegressions)), "Review file-level deltas so aggregate improvements do not hide risk moving into critical files.")
	}
	return out
}

func comparisonRegressed(c *model.QualityComparison, current, baseline *model.Report) bool {
	if len(c.NewFailures) > 0 || c.Score.Delta <= -5 || c.LineCoveragePct.Delta <= -1 || durationRegressed(c.DurationMs) {
		return true
	}
	if current.Coverage.Summary.Branches.Total > 0 && baseline.Coverage.Summary.Branches.Total > 0 && c.BranchCoveragePct.Delta <= -2 {
		return true
	}
	for _, change := range c.CoverageRegressions {
		if change.DeltaPct <= -5 {
			return true
		}
	}
	return false
}

func durationRegressed(delta model.FloatDelta) bool {
	return delta.Before > 0 && delta.Delta >= 1000 && delta.After/delta.Before >= 1.25
}

func failureCount(r *model.Report) int { return r.Test.Summary.Failed + r.Test.Summary.Errors }
func isFailure(status string) bool {
	return status == model.StatusFailed || status == model.StatusError
}

func intDelta(before, after int) model.IntDelta {
	return model.IntDelta{Before: before, After: after, Delta: after - before}
}

func floatDelta(before, after float64) model.FloatDelta {
	return model.FloatDelta{Before: before, After: after, Delta: after - before}
}

func comparisonPath(path string) string {
	return strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
}
