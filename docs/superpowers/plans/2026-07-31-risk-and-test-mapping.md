# Risk-Weighted Coverage & Test-Source Mapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add risk-weighted coverage ranking (git churn × complexity × coverage gap) and test-source mapping with assertion-density diagnostics to test-cli.

**Architecture:** Phase A adds `Report.Risk` computed in a new `internal/analysis/risk.go` from git churn (via existing `gitOutput`), a per-file complexity approximation collected during the existing `scanProject` walk, and existing coverage data. Phase B extends the same walk with per-language test-source mapping and assertion counting stored in `StaticAnalysis.TestMappings`, feeding new findings. All schema changes are additive; `model.SchemaID` stays `test-cli/report@2`.

**Tech Stack:** Go stdlib only (project rule: zero third-party dependencies).

## Global Constraints

- Zero third-party dependencies; stdlib only; no new go.mod entries.
- Additive JSON schema only; `model.SchemaID` unchanged at `test-cli/report@2`.
- Rollups from raw data; git-dependent analysis lives in `internal/analysis`, never `internal/model`.
- No git / errors → risk analysis silently omitted (`Report.Risk == nil`), everything else unchanged.
- `make lint test` green and `make run-self` produces valid reports at every commit.
- Findings need stable IDs: `RISK-001`, `STATIC-007`, `STATIC-008`, `STATIC-009`.

---

### Task 1: Model types

**Files:**
- Modify: `internal/model/model.go` (add `Risk *RiskAnalysis` to `Report`)
- Modify: `internal/model/quality.go` (new types; extend `StaticAnalysis`)
- Test: `internal/model/model_test.go`

**Interfaces (Produces):**

```go
// model.go — Report gains:
Risk *RiskAnalysis `json:"risk,omitempty"`

// quality.go — new:
type RiskAnalysis struct {
    Base  string     `json:"base"`            // e.g. "90 days"
    Files []RiskFile `json:"files,omitempty"` // RiskScore desc, top 25
}
type RiskFile struct {
    Path        string  `json:"path"`
    Language    string  `json:"language,omitempty"`
    Churn       int     `json:"churn"`
    Complexity  int     `json:"complexity"`
    CoveragePct float64 `json:"coveragePct"`
    RiskScore   float64 `json:"riskScore"` // 0..1
}
type TestMapping struct {
    SourcePath     string   `json:"sourcePath"`
    Language       string   `json:"language,omitempty"`
    TestPaths      []string `json:"testPaths,omitempty"`
    AssertionCount int      `json:"assertionCount"`
    TestFuncCount  int      `json:"testFuncCount"`
    Heuristic      bool     `json:"heuristic"`
}
// StaticAnalysis gains:
TestMappings    []TestMapping `json:"testMappings,omitempty"`
UnmappedSources int           `json:"unmappedSources"`
```

- [ ] Add types; JSON round-trip test in model_test.go asserting `risk` omitted when nil.
- [ ] `make lint test`; commit `feat(model): additive risk and test-mapping types`.

### Task 2: Per-file complexity stats in scanProject

**Files:**
- Modify: `internal/analysis/analysis.go`
- Test: `internal/analysis/analysis_test.go`

**Interfaces (Produces):**

```go
type sourceFileStat struct {
    Path       string // slash-relative
    Language   string
    Lines      int
    Branches   int // branch-keyword occurrences
    Complexity int // Lines + 3*Branches
}
// scanProject signature becomes:
func scanProject(root string) (model.StaticAnalysis, []sourceFileStat, bool)
```

- [ ] Add `countBranches(data []byte, language string) int` counting per-language keywords on non-comment lines: common `if`, `for`, `case`, `&&`, `||`; plus `while/except/elif` (python), `while/catch/? :` (typescript/java/csharp), `select` (go), `match/while` (rust), `catch/when` (csharp). Word-boundary match via `stripQuotedLiteralsState` + `strings.Fields` scanning.
- [ ] Collect `sourceFileStat` for non-test source files during the existing walk (no extra reads).
- [ ] Update the one `scanProject` call site in `Evaluate` to accept the extra return.
- [ ] Unit test: fixture Go file with known branch count → expected Complexity.
- [ ] `make lint test`; commit `feat(analysis): per-file complexity stats`.

### Task 3: risk.go — churn, scoring, findings, wiring

**Files:**
- Create: `internal/analysis/risk.go`
- Test: `internal/analysis/risk_test.go`
- Modify: `internal/analysis/analysis.go` (wire into `Evaluate`)

**Interfaces (Produces):**

```go
var ChurnWindowDays = 90 // overridable from config before Evaluate
func analyzeRisk(r *model.Report, root string, stats []sourceFileStat) *model.RiskAnalysis
```

- [ ] Churn: `gitOutput(root, "log", "--since=<N>.days", "--name-only", "--pretty=format:")`, count per-file occurrences; any git error → return nil.
- [ ] Join stats × churn × coverage (`r.Coverage.Files` by slash path; missing → 0%). Skip files where `coverageExpected(root, path, language)` is false.
- [ ] Percentile-normalize churn and complexity by rank (0..1); `risk = churnP^0.3 * complexityP^0.3 * ((100-coveragePct)/100)^0.4`, rounded to 4 decimals; sort desc (tie: path asc), keep top 25.
- [ ] In `Evaluate`, after `q.CoverageHotspots`: `r.Risk = analyzeRisk(r, root, stats)`; in `findings()`, when any `RiskScore >= 0.6`, add `RISK-001` (severity high, category risk, evidence: top file with churn/complexity/coverage numbers, recommendation to test top hotspots first). Pass `r.Risk` through findings via the report parameter.
- [ ] Tests: temp dir + `git init` + commits over two files with different churn; assert ordering, score monotonicity, nil on non-git dir, coverage suppressing score (100% covered file scores ~0).
- [ ] `make lint test`; commit `feat(analysis): risk-weighted coverage analysis`.

### Task 4: Config + reporting

**Files:**
- Modify: `internal/config/config.go` (`RiskChurnDays int` json `riskChurnDays`), `internal/cli/run.go` (set `analysis.ChurnWindowDays` when > 0)
- Modify: `internal/report/text.go` (Risk Hotspots table), `internal/report/html.go` + `internal/report/templates/report.tmpl` (risk section following the coverage-hotspot pattern)
- Test: `internal/report/report_test.go`, `internal/config/config_test.go`

- [ ] Markdown/stdout: `## Risk Hotspots` table (Path | Churn | Complexity | Coverage % | Score), top 10, omitted when `Risk == nil`.
- [ ] HTML: precomputed view model rows; template stays dumb; section hidden when nil.
- [ ] Tests: renderers include/exclude section by nil-ness; config parse of `riskChurnDays`.
- [ ] `make lint test && make run-self`; verify `reports/test/report.json` has `risk`; commit `feat(report): risk hotspot rendering`.

### Task 5: Test-source mapping + assertion counting (Phase B)

**Files:**
- Modify: `internal/analysis/analysis.go`
- Test: `internal/analysis/analysis_test.go`

**Interfaces (Produces):** populated `StaticAnalysis.TestMappings` / `UnmappedSources`.

- [ ] During walk, retain test-file paths and per-test-file `AssertionCount`/`TestFuncCount` counted in `scanTestSmells` pass. Assertion patterns per language: go `t.Error|t.Fatal|t.Errorf|t.Fatalf|require.|assert.`; python `assert |self.assert|pytest.raises`; typescript `expect(|assert.|.toBe|.toEqual`; rust `assert!|assert_eq!|assert_ne!|#[should_panic]`; csharp `Assert.|Should()|.Should(`; java `assert|Assert.|assertThat|verify(`. Test-func patterns: go `func Test`, python `def test_`, typescript `it(|test(`, rust `#[test]`, csharp `[Fact]|[Test]|[TestMethod]`, java `@Test`.
- [ ] After walk, map each source file to test files by name convention (from spec table: `foo_test.go`, `test_foo.py`/`foo_test.py`, `foo.test.ts`/`foo.spec.ts`, `FooTest.java`, `FooTests.cs`; rust: same-file `#[cfg(test)]` counts as mapped, else `tests/` dir any file). Fallback: test file content contains source file's stem as a word in an import/use/using/mod line (retain first 200 import lines per test file during the pass).
- [ ] `TestMappings` capped at 500, unmapped-first then path asc; `UnmappedSources` = full count; `Heuristic: true` always.
- [ ] Fixture tests per language for mapped/unmapped and assertion counts.
- [ ] `make lint test`; commit `feat(analysis): test-source mapping and assertion density`.

### Task 6: Phase B findings + skill template + wrap-up

**Files:**
- Modify: `internal/analysis/analysis.go` (findings), `internal/cli/templates/skill.md.tmpl`
- Test: `internal/analysis/analysis_test.go`

- [ ] `STATIC-007` (medium→high when file also in top risk hotspots): coverage-expected source files with no mapped test, evidence `N unmapped source file(s); see quality.static.testMappings`.
- [ ] `STATIC-008` (medium): ≥30% of test files have `TestFuncCount>0 && AssertionCount==0` → smoke-only warning.
- [ ] `STATIC-009` (low): overall assertions-per-test < 1.0 with ≥10 test funcs.
- [ ] Update `skill.md.tmpl` documenting `report.risk` and `quality.static.testMappings` (heuristic caveat).
- [ ] `make lint test && make run-self`; commit `feat(analysis): mapping findings and skill docs`; push.

## Self-Review Notes

- Spec coverage: model (T1), churn/complexity/scoring/degradation (T2–3), config window + rendering (T4), mapping/assertions (T5), findings + template (T6). JUnit/Cobertura unchanged per spec.
- Type names consistent across tasks (`RiskAnalysis`, `RiskFile`, `TestMapping`, `sourceFileStat`).
- No placeholders; per-language pattern lists are exhaustive in-plan.
