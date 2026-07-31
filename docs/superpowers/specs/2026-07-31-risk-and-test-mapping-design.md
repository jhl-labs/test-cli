# Risk-Weighted Coverage & Test-Source Mapping — Design

Date: 2026-07-31
Status: Approved (design), pending implementation plan

## Goal

Deepen test-cli's QA diagnostics beyond flat coverage percentages:

- **Phase A — Risk-weighted coverage.** Rank files by combined risk
  (git churn × complexity approximation × coverage gap) so the report answers
  "where should tests be added first", not just "what is covered".
- **Phase B — Test-source mapping + assertion density.** Detect source files
  with no mapped tests and tests that execute code without verifying it
  (smoke-only tests), which raw coverage cannot see.

Both phases stay within project conventions: zero third-party dependencies,
declarative per-language adapters, rollups derived from `model.Report`,
additive (non-breaking) schema changes — `model.SchemaID` unchanged.

## Phase A — Risk-weighted coverage

### Data model (`internal/model`)

Additive fields only:

```go
type RiskAnalysis struct {
    Base  string     // churn window description, e.g. "90 days"
    Files []RiskFile // sorted by RiskScore descending, top-N retained
}

type RiskFile struct {
    Path        string
    Churn       int     // commits touching the file within the window
    Complexity  int     // LOC + branch-keyword density approximation
    CoveragePct float64 // 0 when the file has no coverage entry
    RiskScore   float64 // normalized churn × complexity × coverage gap
}
```

`Report` gains `Risk *RiskAnalysis` (omitted when git is unavailable).

### Analysis (`internal/analysis/risk.go`, new)

- **Churn:** single `git log --since=<window> --name-only --pretty=format:`
  invocation via the existing `gitOutput()` helper from `changes.go`; count
  occurrences per file. Window configurable via `.test-cli.json`
  (`risk.churnWindow`, default 90 days).
- **Complexity:** extend the existing `scanProject()` pass — it already walks
  and reads source files for LOC — to also count branch keywords
  (`if/for/while/switch/case/&&/||/catch/match`, per-language sets keyed off
  `sourceLanguage()`). No extra file reads.
- **Combination:** convert each axis to a within-project rank percentile
  (0–1), then `risk = churn^0.3 × complexity^0.3 × (1 - coverage)^0.4`.
  Multiplicative: any axis near zero suppresses the score (a fully covered
  file is low-risk regardless of complexity).
- **Scope:** files where `coverageExpected()` is false (configs, generated
  files, ignored dirs) are excluded. Files absent from the coverage report but
  coverage-expected count as 0% covered.
- **Findings:** files with `RiskScore ≥ 0.6` produce a `QualityFinding`
  (severity warning) with churn/complexity/coverage evidence; feeds a small
  penalty into the coverage quality dimension.
- **Degradation:** no git repo, shallow clone with no history in window, or
  git errors → `Risk` is nil, everything else behaves as today.

### Reporting (`internal/report`)

- JSON: `risk` object as modeled above.
- Markdown/stdout: "Risk Hotspots" table, top 10
  (path, churn, complexity, coverage %, score).
- HTML: a risk heatmap section following the existing coverage-heatmap
  template pattern (view model precomputed in Go, template stays dumb).
- JUnit/Cobertura: unchanged (no schema slot for this data).

## Phase B — Test-source mapping + assertion density

### Data model

Extends `model.StaticAnalysis` (additive):

```go
type TestMapping struct {
    SourcePath     string
    TestPaths      []string // empty → untested-source finding
    AssertionCount int
    TestFuncCount  int
    Heuristic      bool     // always true; consumers must not over-trust
}
```

### Mapping heuristics (declared in `internal/lang/adapters.go`)

Per-language, name-convention first:

| Language | Convention |
|---|---|
| go | `foo.go` ↔ `foo_test.go` (same dir) |
| python | `foo.py` ↔ `test_foo.py` / `foo_test.py` |
| typescript/js | `foo.ts` ↔ `foo.test.ts` / `foo.spec.ts` |
| java | `Foo.java` ↔ `FooTest.java` (mirrored tree) |
| rust | same-file `#[cfg(test)]` + `tests/` integration dir |
| c# | `Foo.cs` ↔ `FooTests.cs` |

Fallback: match source module names inside test files' import/using/mod
statements via per-language regexes (approximate).

### Assertion counting

Per-language assertion patterns (`assert`, `expect(`, `require.`,
`t.Error/t.Fatal/t.Errorf/t.Fatalf`, `Assert.`, `assert_eq!/assert!`, …)
counted in the same pass as `scanTestSmells()`.

### New findings

1. Coverage-expected source file with no mapped test file — severity raised
   when the file is also a top risk hotspot (Phase A cross-reference).
2. Suite with a high ratio of zero-assertion test functions (smoke-only).
3. Files in the bottom tier of assertions-per-test density.

All mapping-derived output carries `heuristic: true`.

## Testing & definition of done

- Unit tests for `risk.go` (percentile normalization, scoring, degradation
  paths) and for mapping/assertion heuristics per language, using `testdata`
  fixtures in the style of existing ingest tests.
- Tests for the no-git / shallow-clone silent-skip paths.
- `make lint test` green; `make run-self` produces a report containing `risk`
  for this repository.
- `generate-skill` template (`internal/cli/templates/skill.md.tmpl`) updated to
  document the risk and mapping fields.
- Schema id unchanged (additive fields only).

## Out of scope

- True mutation testing (requires third-party per-language tooling).
- Cross-file call-graph analysis; mapping stays name/import-heuristic based.
- New CLI subcommands — everything hangs off the existing `run`/`analyze` flow.
