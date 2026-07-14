package analysis

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// AnalyzeHistory attaches multi-run trends and conservative flaky-test
// candidates. Historical reports should be normalized and evaluated with the
// current scoring rules before this function is called.
func AnalyzeHistory(current *model.Report, historical []*model.Report) {
	runs := orderedHistory(current, historical)
	history := &model.QualityHistory{Runs: len(runs)}
	if len(runs) > 0 {
		history.WindowStart = runs[0].GeneratedAt
		history.WindowEnd = runs[len(runs)-1].GeneratedAt
	}
	var scores, coverage, durations, tests, failures []float64
	for _, run := range runs {
		failed := failureCount(run)
		history.Samples = append(history.Samples, model.HistorySample{
			GeneratedAt: run.GeneratedAt, QualityScore: run.Quality.Score,
			LineCoveragePct: run.Coverage.Summary.Lines.Pct,
			Tests:           run.Test.Summary.Total, Failures: failed,
			DurationMs: run.Test.Summary.DurationMs,
		})
		scores = append(scores, float64(run.Quality.Score))
		coverage = append(coverage, run.Coverage.Summary.Lines.Pct)
		durations = append(durations, run.Test.Summary.DurationMs)
		tests = append(tests, float64(run.Test.Summary.Total))
		failures = append(failures, float64(failed))
	}
	history.QualityScore = trendMetric(scores, 0.25)
	history.LineCoveragePct = trendMetric(coverage, 0.1)
	history.TestDurationMs = trendMetric(durations, 10)
	history.Tests = trendMetric(tests, 0.1)
	history.Failures = trendMetric(failures, 0.1)
	history.FlakyTests = flakyTests(runs)
	current.Quality.History = history

	findings := make([]model.QualityFinding, 0, len(current.Quality.Findings))
	for _, finding := range current.Quality.Findings {
		if !strings.HasPrefix(finding.ID, "FLAKY-") && !strings.HasPrefix(finding.ID, "TREND-") {
			findings = append(findings, finding)
		}
	}
	current.Quality.Findings = append(findings, historyFindings(history)...)
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

func orderedHistory(current *model.Report, historical []*model.Report) []*model.Report {
	runs := make([]*model.Report, 0, len(historical)+1)
	seen := map[string]bool{}
	for _, report := range historical {
		if report == nil {
			continue
		}
		key := historyKey(report)
		if seen[key] || key == historyKey(current) {
			continue
		}
		seen[key] = true
		runs = append(runs, report)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].GeneratedAt.Before(runs[j].GeneratedAt) })
	return append(runs, current)
}

func historyKey(report *model.Report) string {
	var observation strings.Builder
	fmt.Fprintf(&observation, "%s|%s|%d|%d|%.6f|%d|%.6f", report.GeneratedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), report.Root, report.Test.Summary.Total, failureCount(report), report.Test.Summary.DurationMs, report.Coverage.Summary.Lines.Total, report.Coverage.Summary.Lines.Pct)
	states := testStatuses(report)
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&observation, "|%q=%s", key, states[key].status)
	}
	digest := sha256.Sum256([]byte(observation.String()))
	return fmt.Sprintf("%x", digest[:])
}

func trendMetric(values []float64, stableEpsilon float64) model.TrendMetric {
	if len(values) == 0 {
		return model.TrendMetric{Direction: "stable"}
	}
	metric := model.TrendMetric{First: values[0], Last: values[len(values)-1]}
	metric.Delta = metric.Last - metric.First
	if len(values) > 1 {
		n := float64(len(values))
		var sumX, sumY, sumXY, sumXX float64
		for i, value := range values {
			x := float64(i)
			sumX += x
			sumY += value
			sumXY += x * value
			sumXX += x * x
		}
		denominator := n*sumXX - sumX*sumX
		if denominator != 0 {
			metric.Slope = (n*sumXY - sumX*sumY) / denominator
		}
	}
	switch {
	case metric.Slope > stableEpsilon:
		metric.Direction = "increasing"
	case metric.Slope < -stableEpsilon:
		metric.Direction = "decreasing"
	default:
		metric.Direction = "stable"
	}
	return metric
}

type flakyAccumulator struct {
	model.FlakyTest
}

func flakyTests(runs []*model.Report) []model.FlakyTest {
	byTest := map[string]*flakyAccumulator{}
	for _, run := range runs {
		states := testStatuses(run)
		for key, state := range states {
			a := byTest[key]
			if a == nil {
				a = &flakyAccumulator{FlakyTest: model.FlakyTest{Language: state.language, Suite: state.suite, Classname: state.classname, Name: state.name}}
				byTest[key] = a
			}
			a.Observations++
			switch state.status {
			case model.StatusPassed:
				a.Passed++
			case model.StatusFailed, model.StatusError:
				a.Failed++
			case model.StatusSkipped:
				a.Skipped++
			}
			a.RecentStatuses = append(a.RecentStatuses, state.status)
			if len(a.RecentStatuses) > 12 {
				a.RecentStatuses = a.RecentStatuses[len(a.RecentStatuses)-12:]
			}
		}
	}
	var out []model.FlakyTest
	for _, a := range byTest {
		executed := a.Passed + a.Failed
		if a.Observations < 3 || a.Passed == 0 || a.Failed == 0 || executed == 0 {
			continue
		}
		a.FlakeRatePct = math.Round(float64(a.Failed)/float64(executed)*1000) / 10
		out = append(out, a.FlakyTest)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Failed != out[j].Failed {
			return out[i].Failed > out[j].Failed
		}
		if out[i].FlakeRatePct != out[j].FlakeRatePct {
			return out[i].FlakeRatePct > out[j].FlakeRatePct
		}
		a := out[i].Language + "\x00" + out[i].Suite + "\x00" + out[i].Name
		b := out[j].Language + "\x00" + out[j].Suite + "\x00" + out[j].Name
		return a < b
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func historyFindings(history *model.QualityHistory) []model.QualityFinding {
	if history == nil || history.Runs < 2 {
		return nil
	}
	var out []model.QualityFinding
	add := func(id, severity, category, title, detail, evidence, recommendation string) {
		out = append(out, model.QualityFinding{ID: id, Severity: severity, Category: category, Title: title, Detail: detail, Evidence: evidence, Recommendation: recommendation})
	}
	if len(history.FlakyTests) > 0 {
		severity := "medium"
		for _, test := range history.FlakyTests {
			if test.Failed >= 2 || test.FlakeRatePct >= 50 {
				severity = "high"
				break
			}
		}
		add("FLAKY-001", severity, "flakiness", "Repeated pass/fail transitions", "Tests changed between passing and failing states across at least three observed runs.", fmt.Sprintf("%d flaky candidate(s) across %d runs", len(history.FlakyTests), history.Runs), "Remove shared-state, timing, order, and environment sensitivity; keep the test quarantined only with an owner and expiry.")
	}
	if history.Runs >= 3 && history.LineCoveragePct.Slope <= -0.5 && history.LineCoveragePct.Delta <= -1 {
		severity := "medium"
		if history.LineCoveragePct.Delta <= -5 {
			severity = "high"
		}
		add("TREND-001", severity, "trend", "Line coverage is trending down", "The least-squares trend and first-to-current movement both show declining coverage.", fmt.Sprintf("%+.2f points/run; %.1f%% → %.1f%%", history.LineCoveragePct.Slope, history.LineCoveragePct.First, history.LineCoveragePct.Last), "Stop baseline erosion and prioritize persistent file-level hotspots before adding broad low-value tests.")
	}
	if history.Runs >= 3 && history.QualityScore.Slope <= -1 && history.QualityScore.Delta <= -3 {
		add("TREND-002", "medium", "trend", "Quality score is trending down", "Multiple QA dimensions are weakening across the observed run window.", fmt.Sprintf("%+.2f score/run; %.0f → %.0f", history.QualityScore.Slope, history.QualityScore.First, history.QualityScore.Last), "Inspect dimension history and recurring findings instead of accepting each small regression independently.")
	}
	if history.Runs >= 3 && history.TestDurationMs.First > 0 && history.TestDurationMs.Delta >= 1000 && history.TestDurationMs.Last/history.TestDurationMs.First >= 1.25 {
		add("TREND-003", "medium", "performance", "Test duration is trending up", "The feedback loop is materially slower across the observed run window.", fmt.Sprintf("%.0f ms → %.0f ms (%+.0f ms)", history.TestDurationMs.First, history.TestDurationMs.Last, history.TestDurationMs.Delta), "Use per-test duration history and current slow-test rankings to find accumulating setup or I/O cost.")
	}
	if history.Runs >= 3 && history.Tests.First >= 10 && history.Tests.Delta <= -math.Max(5, history.Tests.First*0.2) {
		add("TREND-004", "medium", "trend", "Reported test count declined", "A substantial part of the previously observed suite is no longer present in the report.", fmt.Sprintf("%.0f → %.0f tests", history.Tests.First, history.Tests.Last), "Confirm that tests were intentionally consolidated rather than excluded by discovery, filters, or focused-test markers.")
	}
	return out
}
