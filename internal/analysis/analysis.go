// Package analysis derives a standardized QA assessment from normalized test
// and coverage data and performs a lightweight, cross-language scan of test
// source files. The rules are deliberately conservative and evidence based;
// for example, a single run can rank slow tests but cannot claim flakiness.
package analysis

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

const maxStaticSmells = 200

// Evaluate refreshes the report's quality assessment. Call it after Normalize
// so every score is derived from finalized test and coverage rollups.
func Evaluate(r *model.Report, root string) {
	previousComparison := r.Quality.Comparison
	previousHistory := r.Quality.History
	previousChanges := r.Quality.Changes
	var previousDerivedFindings []model.QualityFinding
	for _, finding := range r.Quality.Findings {
		if strings.HasPrefix(finding.ID, "REG-") || strings.HasPrefix(finding.ID, "FLAKY-") || strings.HasPrefix(finding.ID, "TREND-") || strings.HasPrefix(finding.ID, "CHG-") {
			previousDerivedFindings = append(previousDerivedFindings, finding)
		}
	}
	q := model.QualityReport{Static: r.Quality.Static, Comparison: previousComparison, History: previousHistory, Changes: previousChanges}
	if root != "" {
		if scanned, ok := scanProject(root); ok {
			q.Static = scanned
		}
	}
	if len(r.Languages) == 0 {
		for _, inventory := range q.Static.Inventory {
			if inventory.SourceFiles+inventory.TestFiles > 0 {
				r.Languages = append(r.Languages, inventory.Language)
			}
		}
		sort.Strings(r.Languages)
	}
	q.SlowTests = slowTests(r)
	q.CoverageHotspots = coverageHotspots(r)
	q.Findings = findings(r, q)
	q.Findings = append(q.Findings, previousDerivedFindings...)
	sort.SliceStable(q.Findings, func(i, j int) bool {
		a, b := severityRank(q.Findings[i].Severity), severityRank(q.Findings[j].Severity)
		if a != b {
			return a < b
		}
		return q.Findings[i].ID < q.Findings[j].ID
	})
	q.Dimensions = dimensions(r, q)
	q.Score = weightedScore(q.Dimensions)
	q.Grade = grade(q.Score)
	q.Risk = risk(q.Score, q.Findings)
	q.Summary = summarize(q.Findings)
	r.Quality = q
}

func dimensions(r *model.Report, q model.QualityReport) []model.QualityDimension {
	reliability := 0
	if r.Test.Summary.Total-r.Test.Summary.Skipped > 0 {
		reliability = rounded(r.Test.Summary.PassRate() * 100)
		if r.Test.Summary.Failed > 0 && reliability > 60 {
			reliability = 60
		}
		if r.Test.Summary.Errors > 0 && reliability > 35 {
			reliability = 35
		}
	}

	lineCoverage := rounded(r.Coverage.Summary.Lines.Pct)
	branchCoverage := rounded(r.Coverage.Summary.Branches.Pct)

	hygiene := 100
	if r.Test.Summary.Total > 0 {
		hygiene -= rounded(float64(r.Test.Summary.Skipped) / float64(r.Test.Summary.Total) * 100)
	}
	ruleCounts := map[string]int{}
	for _, smell := range q.Static.Smells {
		ruleCounts[smell.Rule]++
	}
	hygiene -= min(ruleCounts["focused-test"]*30, 60)
	hygiene -= min(ruleCounts["disabled-test"]*3, 25)
	hygiene -= min(ruleCounts["hard-wait"]*5, 25)
	hygiene -= min(ruleCounts["large-test-file"]*5, 15)
	hygiene = clamp(hygiene)

	distribution := 0
	coveredFiles := 0
	for _, f := range r.Coverage.Files {
		if f.Lines.Total == 0 {
			continue
		}
		distribution += rounded(f.Lines.Pct)
		coveredFiles++
	}
	if coveredFiles > 0 {
		distribution = clamp(rounded(float64(distribution) / float64(coveredFiles)))
	}

	branchDetail := "No branch coverage data was reported"
	if r.Coverage.Summary.Branches.Total > 0 {
		branchDetail = fmt.Sprintf("%d/%d branches covered", r.Coverage.Summary.Branches.Covered, r.Coverage.Summary.Branches.Total)
	}
	return []model.QualityDimension{
		{ID: "reliability", Name: "Test reliability", Score: reliability, Weight: 35, Detail: fmt.Sprintf("%d passed, %d failed, %d errors", r.Test.Summary.Passed, r.Test.Summary.Failed, r.Test.Summary.Errors)},
		{ID: "line-coverage", Name: "Line coverage", Score: lineCoverage, Weight: 30, Detail: fmt.Sprintf("%d/%d executable lines covered", r.Coverage.Summary.Lines.Covered, r.Coverage.Summary.Lines.Total)},
		{ID: "branch-coverage", Name: "Branch coverage", Score: branchCoverage, Weight: 10, Detail: branchDetail},
		{ID: "test-hygiene", Name: "Test hygiene", Score: hygiene, Weight: 15, Detail: fmt.Sprintf("%d skipped tests, %d static test smells", r.Test.Summary.Skipped, len(q.Static.Smells))},
		{ID: "coverage-distribution", Name: "Coverage distribution", Score: distribution, Weight: 10, Detail: fmt.Sprintf("Equal-weight average across %d covered files", coveredFiles)},
	}
}

func findings(r *model.Report, q model.QualityReport) []model.QualityFinding {
	var out []model.QualityFinding
	add := func(id, severity, category, title, detail, evidence, recommendation string) {
		out = append(out, model.QualityFinding{ID: id, Severity: severity, Category: category, Title: title, Detail: detail, Evidence: evidence, Recommendation: recommendation})
	}
	ts, cov := r.Test.Summary, r.Coverage.Summary
	if ts.Total-ts.Skipped <= 0 {
		detail := "The report contains no normalized test cases."
		evidence := "test.summary.total = 0"
		if ts.Total > 0 {
			detail = "Every reported test was skipped, so no behavior was checked."
			evidence = fmt.Sprintf("%d/%d tests skipped", ts.Skipped, ts.Total)
		}
		add("TEST-001", "critical", "reliability", "No executable test results", detail, evidence, "Produce executable JUnit or go test -json results and make the full test command part of the required CI path.")
	}
	if ts.Errors > 0 {
		add("TEST-002", "critical", "reliability", "Test execution errors", "Tests could not complete normally, so the run is not a reliable quality signal.", fmt.Sprintf("%d error(s)", ts.Errors), "Fix setup, fixture, or environment errors before interpreting other quality metrics.")
	}
	if ts.Failed > 0 {
		severity := "high"
		if ts.Failed >= 5 || ts.PassRate() < 0.9 {
			severity = "critical"
		}
		add("TEST-003", severity, "reliability", "Failing tests", "Expected behavior is contradicted by the current test run.", fmt.Sprintf("%d failed of %d total", ts.Failed, ts.Total), "Start with the failure details and fix deterministic failures before expanding coverage.")
	}
	if ts.Total > 0 {
		skipPct := float64(ts.Skipped) / float64(ts.Total) * 100
		if skipPct >= 10 {
			severity := "medium"
			if skipPct >= 25 {
				severity = "high"
			}
			add("TEST-004", severity, "hygiene", "High skipped-test ratio", "Skipped tests reduce the behavior actually checked by this run.", fmt.Sprintf("%d/%d skipped (%.1f%%)", ts.Skipped, ts.Total, skipPct), "Review every skip, record an owner/reason, and fail CI on expired temporary skips.")
		}
	}

	if cov.Lines.Total == 0 {
		add("COV-001", "high", "coverage", "Line coverage is not measured", "Passing tests alone do not show which production paths were exercised.", "coverage.summary.lines.total = 0", "Enable the native coverage reporter and ingest Cobertura, LCOV, JaCoCo, or Go coverage output.")
	} else if cov.Lines.Pct < 80 {
		severity := "medium"
		if cov.Lines.Pct < 60 {
			severity = "high"
		}
		add("COV-002", severity, "coverage", "Line coverage below the recommended target", "A material share of executable lines was not exercised.", fmt.Sprintf("%.1f%% (%d/%d)", cov.Lines.Pct, cov.Lines.Covered, cov.Lines.Total), "Prioritize tests for the coverage hotspots with the most uncovered lines, then enforce a non-decreasing CI gate.")
	}
	if cov.Branches.Total == 0 {
		add("COV-003", "info", "coverage", "Branch coverage is unavailable", "Line coverage cannot show whether both outcomes of decisions were exercised.", "coverage.summary.branches.total = 0", "Enable branch coverage where the ecosystem supports it and add decision-path tests for critical logic.")
	} else if cov.Branches.Pct < 70 {
		add("COV-004", "medium", "coverage", "Low branch coverage", "Conditional behavior has less exercise than the recommended diagnostic baseline.", fmt.Sprintf("%.1f%% (%d/%d)", cov.Branches.Pct, cov.Branches.Covered, cov.Branches.Total), "Add boundary, negative, and alternate-path tests around uncovered decisions.")
	}

	highRisk := 0
	for _, h := range q.CoverageHotspots {
		if h.Risk == "high" {
			highRisk++
		}
	}
	if highRisk > 0 {
		add("COV-005", "high", "risk", "Coverage risk is concentrated", "Low-covered files with many uncovered lines create disproportionate regression risk.", fmt.Sprintf("%d high-risk file(s)", highRisk), "Start with the first high-risk files in the coverage risk map; test public behavior and failure paths before small utilities.")
	}

	slowCount, threshold := slowOutliers(r)
	if slowCount > 0 {
		add("PERF-001", "medium", "performance", "Slow-test outliers", "A small set of tests is materially slower than the run's typical test and can lengthen feedback loops.", fmt.Sprintf("%d test(s) slower than %.0f ms", slowCount, threshold), "Inspect setup and I/O in the ranked slow tests; keep integration behavior explicit rather than silently slowing unit suites.")
	}

	if q.Static.SourceFiles > 0 && q.Static.TestFiles == 0 && ts.Total == 0 {
		add("STATIC-001", "high", "test-design", "No test source files found", "Production source was detected without conventional test files or executable test results.", fmt.Sprintf("%d source files", q.Static.SourceFiles), "Add tests using the ecosystem's conventional naming so local tools and CI can discover them consistently.")
	}
	if q.Static.SourceLines >= 200 && q.Static.TestToSourceRatio < 0.15 {
		add("STATIC-002", "medium", "test-design", "Low test-code density", "The amount of test code is small relative to production code; this is a prioritization signal, not a coverage substitute.", fmt.Sprintf("%.2f test/source LOC ratio", q.Static.TestToSourceRatio), "Review the coverage risk map and add focused tests around high-change or high-impact behavior.")
	}
	ruleMeta := map[string][4]string{
		"focused-test":    {"STATIC-003", "high", "Focused tests are committed", "Remove .only/fdescribe/fit markers so the full suite cannot be accidentally narrowed."},
		"disabled-test":   {"STATIC-004", "medium", "Disabled tests are present", "Give every disabled test a reason and expiry, or restore/remove it."},
		"hard-wait":       {"STATIC-005", "medium", "Hard waits in tests", "Replace fixed sleeps/delays with deterministic clocks, events, polling, or explicit synchronization."},
		"large-test-file": {"STATIC-006", "low", "Very large test files", "Split large test files by behavior or fixture scope to improve ownership and diagnosis."},
	}
	ruleCounts := map[string]int{}
	for _, smell := range q.Static.Smells {
		ruleCounts[smell.Rule]++
	}
	for rule, count := range ruleCounts {
		meta, ok := ruleMeta[rule]
		if !ok {
			continue
		}
		add(meta[0], meta[1], "static-analysis", meta[2], "Portable source heuristics found a test-maintainability risk.", fmt.Sprintf("%d occurrence(s); see quality.static.smells", count), meta[3])
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := severityRank(out[i].Severity), severityRank(out[j].Severity)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func slowTests(r *model.Report) []model.TestHotspot {
	var out []model.TestHotspot
	for _, suite := range r.Test.Suites {
		for _, tc := range suite.Cases {
			if tc.DurationMs <= 0 {
				continue
			}
			out = append(out, model.TestHotspot{Language: suite.Language, Suite: suite.Name, Name: tc.Name, Status: tc.Status, DurationMs: tc.DurationMs})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DurationMs > out[j].DurationMs })
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

func slowOutliers(r *model.Report) (int, float64) {
	var durations []float64
	for _, suite := range r.Test.Suites {
		for _, tc := range suite.Cases {
			if tc.DurationMs > 0 {
				durations = append(durations, tc.DurationMs)
			}
		}
	}
	if len(durations) < 3 {
		return 0, 0
	}
	sort.Float64s(durations)
	median := durations[len(durations)/2]
	threshold := math.Max(1000, median*3)
	count := 0
	for _, d := range durations {
		if d > threshold {
			count++
		}
	}
	return count, threshold
}

func coverageHotspots(r *model.Report) []model.CoverageHotspot {
	var out []model.CoverageHotspot
	for _, f := range r.Coverage.Files {
		uncovered := f.Lines.Total - f.Lines.Covered
		if f.Lines.Total == 0 || uncovered <= 0 {
			continue
		}
		risk := "low"
		if f.Lines.Pct < 50 && uncovered >= 20 {
			risk = "high"
		} else if f.Lines.Pct < 75 || uncovered >= 50 {
			risk = "medium"
		}
		out = append(out, model.CoverageHotspot{Path: f.Path, Language: f.Language, CoveragePct: f.Lines.Pct, UncoveredLines: uncovered, Risk: risk})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := riskRank(out[i].Risk), riskRank(out[j].Risk)
		if a != b {
			return a < b
		}
		if out[i].UncoveredLines != out[j].UncoveredLines {
			return out[i].UncoveredLines > out[j].UncoveredLines
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

func scanProject(root string) (model.StaticAnalysis, bool) {
	var out model.StaticAnalysis
	if root == "" {
		return out, false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return out, false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return out, false
	}
	root = resolvedRoot
	type counts struct{ sourceFiles, testFiles, sourceLines, testLines int }
	byLang := map[string]*counts{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && ignoredDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		language := sourceLanguage(entry.Name())
		if language == "" || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 2*1024*1024 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || strings.IndexByte(string(data), 0) >= 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		lines := countLOC(data)
		c := byLang[language]
		if c == nil {
			c = &counts{}
			byLang[language] = c
		}
		if isTestFile(rel, language) {
			c.testFiles++
			c.testLines += lines
			scanTestSmells(&out, rel, language, data, lines)
		} else {
			c.sourceFiles++
			c.sourceLines += lines
		}
		return nil
	})
	langs := make([]string, 0, len(byLang))
	for language := range byLang {
		langs = append(langs, language)
	}
	sort.Strings(langs)
	for _, language := range langs {
		c := byLang[language]
		out.SourceFiles += c.sourceFiles
		out.TestFiles += c.testFiles
		out.SourceLines += c.sourceLines
		out.TestLines += c.testLines
		out.Inventory = append(out.Inventory, model.LanguageInventory{Language: language, SourceFiles: c.sourceFiles, TestFiles: c.testFiles, SourceLines: c.sourceLines, TestLines: c.testLines})
	}
	if out.SourceLines > 0 {
		out.TestToSourceRatio = math.Round(float64(out.TestLines)/float64(out.SourceLines)*100) / 100
	}
	sort.SliceStable(out.Smells, func(i, j int) bool {
		if out.Smells[i].Path != out.Smells[j].Path {
			return out.Smells[i].Path < out.Smells[j].Path
		}
		if out.Smells[i].Line != out.Smells[j].Line {
			return out.Smells[i].Line < out.Smells[j].Line
		}
		return out.Smells[i].Rule < out.Smells[j].Rule
	})
	return out, true
}

func sourceLanguage(name string) string {
	lower := strings.ToLower(filepath.ToSlash(name))
	base := filepath.Base(lower)
	// Build metadata and declaration-only files are source-like text but are
	// not production executable lines. Including them would create impossible
	// changed-coverage requirements.
	switch {
	case strings.HasSuffix(base, ".gradle.kts"), base == "build.rs", base == "module-info.java", base == "package-info.java", strings.HasSuffix(base, ".d.ts"):
		return ""
	case typescriptToolingFile(base):
		return ""
	}
	switch filepath.Ext(lower) {
	case ".py":
		return "python"
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return "typescript"
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".cs":
		return "csharp"
	case ".java", ".kt", ".kts":
		return "java"
	default:
		return ""
	}
}

func typescriptToolingFile(base string) bool {
	if !strings.HasSuffix(base, ".js") && !strings.HasSuffix(base, ".jsx") && !strings.HasSuffix(base, ".ts") && !strings.HasSuffix(base, ".tsx") && !strings.HasSuffix(base, ".mjs") && !strings.HasSuffix(base, ".cjs") {
		return false
	}
	if strings.Contains(base, ".config.") {
		return true
	}
	for _, prefix := range []string{"babel.", "commitlint.", "eslint.", "jest.", "next.", "nuxt.", "playwright.", "postcss.", "prettier.", "rollup.", "tailwind.", "vite.", "vitest.", "webpack."} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

func isTestFile(path, language string) bool {
	p := "/" + strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	switch language {
	case "python":
		return base == "conftest.py" || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || strings.Contains(p, "/tests/") || strings.Contains(p, "/test/")
	case "typescript":
		return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.Contains(p, "/__tests__/") || strings.Contains(p, "/tests/") || strings.Contains(p, "/test/")
	case "go":
		return strings.HasSuffix(base, "_test.go")
	case "rust":
		return strings.Contains(p, "/tests/") || strings.Contains(p, "/benches/")
	case "csharp":
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		return strings.HasSuffix(stem, "test") || strings.HasSuffix(stem, "tests") || strings.Contains(p, "/tests/") || strings.Contains(p, "/test/")
	case "java":
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		return strings.HasSuffix(stem, "test") || strings.HasSuffix(stem, "tests") || strings.Contains(p, "/src/test/") || strings.Contains(p, "/tests/")
	}
	return false
}

func ignoredDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", "target", "bin", "obj", "testdata", "__fixtures__", ".venv", "venv", ".tox", ".pytest_cache", ".mypy_cache", ".next", "coverage", "reports", "out":
		return true
	default:
		return false
	}
}

func countLOC(data []byte) int {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	count := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			count++
		}
	}
	return count
}

func scanTestSmells(out *model.StaticAnalysis, path, language string, data []byte, loc int) {
	if loc >= 1000 {
		appendSmell(out, model.StaticSmell{Rule: "large-test-file", Severity: "low", Path: path, Line: 1, Message: fmt.Sprintf("test file has %d non-empty lines", loc)})
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	conditionalDisabledLines := map[int]bool{}
	if language == "go" {
		conditionalDisabledLines = conditionalGoSkipLines(data)
	}
	var quote rune
	escaped := false
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") || (language == "python" && strings.HasPrefix(line, "#")) {
			continue
		}
		code := stripQuotedLiteralsState(line, &quote, &escaped)
		if language == "typescript" && containsAny(code, ".only(", ".only (", "fdescribe(", "fdescribe (", "fit(", "fit (") {
			appendSmell(out, model.StaticSmell{Rule: "focused-test", Severity: "high", Path: path, Line: lineNo, Message: "focused test marker can exclude the rest of the suite"})
		}
		if disabledTestLine(code, language) && !conditionalDisabledLines[lineNo] {
			appendSmell(out, model.StaticSmell{Rule: "disabled-test", Severity: "medium", Path: path, Line: lineNo, Message: "disabled or skipped test marker"})
		}
		if hardWaitLine(code, language) {
			appendSmell(out, model.StaticSmell{Rule: "hard-wait", Severity: "medium", Path: path, Line: lineNo, Message: "fixed sleep/delay can make tests slow or timing-sensitive"})
		}
	}
}

// conditionalGoSkipLines uses Go's parser to distinguish an environment- or
// capability-guarded t.Skip from an unconditional disabled test. The portable
// smell rule remains lexical for other languages, but valid Go source gives us
// enough structure to avoid penalizing normal cross-platform test guards.
func conditionalGoSkipLines(data []byte) map[int]bool {
	out := map[int]bool{}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", data, 0)
	if err != nil {
		return out
	}
	var parents []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			if len(parents) > 0 {
				parents = parents[:len(parents)-1]
			}
			return true
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Skip" || selector.Sel.Name == "Skipf" || selector.Sel.Name == "SkipNow") {
				for _, parent := range parents {
					switch parent.(type) {
					case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.ForStmt, *ast.RangeStmt:
						out[fset.Position(call.Pos()).Line] = true
					}
				}
			}
		}
		parents = append(parents, node)
		return true
	})
	return out
}

func stripQuotedLiteralsState(line string, quote *rune, escaped *bool) string {
	var b strings.Builder
	for _, r := range line {
		if *quote != 0 {
			if *escaped {
				*escaped = false
				continue
			}
			if r == '\\' && *quote != '`' {
				*escaped = true
				continue
			}
			if r == *quote {
				*quote = 0
				b.WriteRune(' ')
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			*quote = r
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func disabledTestLine(line, language string) bool {
	switch language {
	case "python":
		return containsAny(line, "@pytest.mark.skip", "@unittest.skip", "pytest.skip(")
	case "typescript":
		return containsAny(line, ".skip(", ".skip (", "xdescribe(", "xdescribe (", "xit(", "xit (")
	case "go":
		return strings.Contains(line, ".Skip(") || strings.Contains(line, ".Skipf(") || strings.Contains(line, ".SkipNow(")
	case "rust":
		return strings.Contains(line, "#[ignore")
	case "csharp":
		return containsAny(line, "[Ignore", "Skip =", "Skip=")
	case "java":
		return containsAny(line, "@Disabled", "@Ignore")
	}
	return false
}

func hardWaitLine(line, language string) bool {
	switch language {
	case "python":
		return strings.Contains(line, "time.sleep(")
	case "typescript":
		return containsAny(line, "setTimeout(", "setTimeout (")
	case "go":
		return strings.Contains(line, "time.Sleep(")
	case "rust":
		return strings.Contains(line, "thread::sleep(")
	case "csharp":
		return containsAny(line, "Thread.Sleep(", "Task.Delay(")
	case "java":
		return strings.Contains(line, "Thread.sleep(")
	}
	return false
}

func appendSmell(out *model.StaticAnalysis, smell model.StaticSmell) {
	if len(out.Smells) < maxStaticSmells {
		out.Smells = append(out.Smells, smell)
	}
}

func containsAny(s string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(s, value) {
			return true
		}
	}
	return false
}

func weightedScore(dims []model.QualityDimension) int {
	total, weights := 0, 0
	for _, d := range dims {
		total += clamp(d.Score) * d.Weight
		weights += d.Weight
	}
	if weights == 0 {
		return 0
	}
	return clamp(rounded(float64(total) / float64(weights)))
}

func grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}

func risk(score int, findings []model.QualityFinding) string {
	hasHigh := false
	for _, f := range findings {
		if f.Severity == "critical" {
			return "critical"
		}
		if f.Severity == "high" {
			hasHigh = true
		}
	}
	if score < 60 || hasHigh {
		return "high"
	}
	if score < 80 {
		return "moderate"
	}
	return "low"
}

func summarize(findings []model.QualityFinding) model.FindingSummary {
	var s model.FindingSummary
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			s.Critical++
		case "high":
			s.High++
		case "medium":
			s.Medium++
		case "low":
			s.Low++
		case "info":
			s.Info++
		}
	}
	return s
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

func riskRank(risk string) int {
	switch risk {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}

func rounded(v float64) int { return int(math.Round(v)) }
func clamp(v int) int       { return max(0, min(100, v)) }
