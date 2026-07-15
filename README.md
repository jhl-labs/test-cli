# test-cli

**Standardized test, coverage, and QA diagnostics across six languages — one binary, one schema.**

`test-cli` is a single, dependency-free, cross-platform Go binary that detects a
project's languages, runs their tests **with coverage**, and normalizes the
results into one **language-agnostic report**, adds an evidence-based quality
assessment and portable static test-code checks, then renders machine-readable
interchange (JSON, JUnit, Cobertura), human summaries (stdout, Markdown), and
rich HTML visualizations (QA insights, trees, treemaps, risk matrices, and
heatmaps), including multi-run trends and conservative flaky-test evidence.

It is built to be driven by **AI agents** and to run as a **GitHub Action**.

🌐 **Website & live report:** [jhl-labs.github.io/test-cli](https://jhl-labs.github.io/test-cli/) ·
📦 [Releases](https://github.com/jhl-labs/test-cli/releases) ·
⚙️ [GitHub Action](https://github.com/jhl-labs/test-cli-action)

| | |
|---|---|
| **Languages** | Python · TypeScript/JavaScript · Go · Rust · C#/.NET · Java/Kotlin |
| **Platforms** | linux, macOS, windows · amd64 + arm64 |
| **Dependencies** | none (Go standard library only) |
| **Outputs** | `report.json` · `junit.xml` · `coverage.cobertura.xml` · `report.md` · HTML dashboard + QA/coverage maps |
| **Schema** | `test-cli/report@2` |

---

## Why

Every language ships its own test runner, coverage format, and report layout.
Wiring six of them into one pipeline — and teaching an AI agent to read six
different outputs — is the actual work. `test-cli` collapses that into a single
command with a single, stable JSON schema:

```bash
test-cli run .
```

Run it on a polyglot monorepo and you get one merged report covering Go, Python,
and TypeScript at once.

The diagnosis is deliberately evidence based. A single run can identify
failures, skipped tests, slow-test outliers, coverage concentration, and static
test smells; it does **not** label a test flaky without repeated-run history.

---

## How it works

```
 detect ─▶ run native test commands ─▶ ingest (sniff+parse) ─▶ normalize ─▶ QA analyze ─▶ render
 (lang)      (pytest/jest/go test…)       (JUnit/Cobertura/…)    (schema)    (score/rules)  (json/html/…)
```

After normalization, the QA analyzer scores five transparent dimensions: test
reliability (35%), line coverage (30%), branch coverage (10%), test hygiene
(15%), and coverage distribution (10%). Stable finding IDs (`TEST-*`, `COV-*`,
`PERF-*`, `STATIC-*`, `REG-*`, `FLAKY-*`, `TREND-*`, `CHG-*`) include severity,
evidence, and a recommendation.

The normalizer is the heart of the tool: native artifacts are **content-sniffed**
(not guessed from filenames) and projected onto one schema, so a Python pytest
run and a Go `go test` run produce structurally identical reports.

| Language | Test command (default) | Test format | Coverage format |
|---|---|---|---|
| Python | `pytest --junitxml … --cov … --cov-report=xml` | JUnit XML | Cobertura |
| TypeScript/JS | project-local `node_modules/jest/bin/jest.js --reporters=jest-junit --coverage` | JUnit XML | Cobertura / LCOV |
| Go | `go test ./... -json -coverprofile` | `go test -json` | Go coverage profile |
| Rust | `cargo llvm-cov --cobertura nextest` with a generated nextest tool config | JUnit XML | Cobertura |
| C#/.NET | `dotnet test --logger junit --collect "XPlat Code Coverage"` | JUnit XML | Cobertura |
| Java/Kotlin | Maven `jacoco:prepare-agent test jacoco:report` or Gradle `test jacocoTestReport` | Surefire/Gradle JUnit XML | JaCoCo XML |

Every default command is overridable per project (see [Configuration](#configuration)).
`doctor` validates the capabilities used by those commands, not just the parent
runtime: for example pytest-cov, local Jest + jest-junit, cargo-llvm-cov +
cargo-nextest, .NET's JUnit logger + Coverlet collector,
and Gradle's `jacocoTestReport` task. Java projects select Maven,
the Gradle wrapper, or installed Gradle from their project markers. The default
JavaScript command never downloads packages during a test run.
Because ingestion sniffs format from content, any framework that can emit JUnit +
Cobertura/LCOV works out of the box — point `test-cli ingest` at the artifacts.
Each execution clears its generated raw directory and ignores unchanged
project-root artifacts, so an old report cannot mask a failed command. A run
also fails if any selected language produces no current test cases. Overlapping
per-file coverage is merged by line identity, so multiple test projects do not
inflate executable-line totals.

---

## Install

```bash
# Latest release (Linux/macOS):
curl -fsSL https://jhl-labs.github.io/test-cli/install.sh | bash

# System-wide install:
curl -fsSL https://jhl-labs.github.io/test-cli/install.sh | sudo bash

# Pin a version into a directory:
curl -fsSL https://jhl-labs.github.io/test-cli/install.sh \
  | VERSION=v0.3.0 INSTALL_DIR="$HOME/.local/bin" bash

# From source:
go install github.com/jhl-labs/test-cli/cmd/test-cli@latest
# or
git clone https://github.com/jhl-labs/test-cli && cd test-cli && make build
```

---

## Quick start

```bash
# Detect languages and run everything, printing a summary:
test-cli run . --format stdout,json

# CI-style: JSON + JUnit + Cobertura + Markdown + HTML, fail under 80% lines:
test-cli run . --profile ci --fail-under 80 --output-dir reports/test

# Add the optional standardized quality gate:
test-cli run . --profile ci --fail-under 80 --fail-quality 70

# Detect regressions against a saved main-branch report:
test-cli run . --baseline reports/main/report.json --fail-on-regression

# Analyze three runs for trends/flakiness and gate repeated flaky evidence:
test-cli run . --history reports/run-1.json --history reports/run-2.json --fail-on-flaky

# Gate only the executable lines changed relative to the main branch:
test-cli run . --diff-base origin/main --fail-diff-coverage 80

# A single language:
test-cli run . --lang go

# No toolchain? Ingest existing artifacts from any framework:
test-cli ingest --tests junit.xml --coverage coverage.xml -o reports/test

# Re-render visualizations from a saved report (no re-run). Source access is
# opt-in so externally supplied report.json files cannot read local files:
test-cli report --in reports/test/report.json --source-root . --format html,markdown

# Static QA inspection only; never executes project test commands:
test-cli analyze . --format stdout,json,html
```

Open `reports/test/index.html` for the dashboard, `reports/test/insights.html`
for QA diagnostics and test-performance views, and
`reports/test/coverage/index.html` for coverage maps and source heatmaps.

---

## Commands

| Command | Purpose |
|---|---|
| `run [target]` | Detect languages, run tests with coverage, write reports. Aliases: `scan`, `diagnose`. |
| `analyze [target]` | Statically inspect test code and ingest existing artifacts without executing tests. Alias: `inspect`. |
| `ingest` | Build reports from existing artifacts (`--tests`, `--coverage`); no execution. |
| `report` | Re-render reports from a previously generated `report.json` (`--in`). |
| `detect [target]` | List which languages/toolchains are detected (`--json` for structured output). |
| `doctor [target]` | Check built-in runner/reporter capabilities and configured command executables (`--all` lists every language). |
| `generate-skill` | Emit an AI-agent skill (`SKILL.md`) describing how to drive test-cli. |
| `version` | Print version, commit, and build date. |
| `help` | Usage. |

### Common flags

| Flag | Description |
|---|---|
| `-o, --output-dir DIR` | Output directory (default `reports/test`). |
| `--format FORMAT` | Repeatable / comma-separated: `stdout,json,junit,cobertura,markdown,html`. |
| `--lang LANG` | Restrict to a language (repeatable). |
| `--profile NAME` | Preset: `default` · `ci` · `release`. |
| `--fail-under PCT` | Exit non-zero if total line coverage `< PCT`. |
| `--fail-quality SCORE` | Exit non-zero if the standardized quality score `< SCORE` (disabled by default). |
| `--baseline FILE` | Compare tests, score, duration, and file/overall coverage against an existing `report.json`. |
| `--fail-on-regression` | Exit non-zero on a material baseline regression; requires `--baseline`. |
| `--history FILE` | Add a prior `report.json` for trends and flaky-test analysis; repeatable / comma-separated. |
| `--fail-on-flaky` | Exit non-zero when current + at least two historical reports contain repeated pass/fail evidence. |
| `--diff-base REF` | Analyze added/modified production lines relative to a Git ref, including staged, unstaged, and untracked files. |
| `--fail-diff-coverage PCT` | Exit non-zero when changed executable-line coverage is below `PCT` or a changed source file has no line-level evidence. |
| `--source-root DIR` | `report` only: explicitly trust this repository root for source-backed heatmaps and `--diff-base`; embedded report roots are metadata only. |
| `--no-run` | Ingest existing artifacts without executing tests. |
| `--timeout DUR` | Per-command timeout (default `20m`). |
| `--quiet` | Suppress progress logging. |

Explicit ingest infers language from native formats, artifact path segments,
and covered source extensions. Pass `--lang` when generic JUnit or extensionless
coverage input is otherwise ambiguous.

---

## The standardized report (`report.json`)

`report.json` (schema `test-cli/report@2`) is the source of truth; every other
format is rendered from it. Read **this**, not stdout.

```jsonc
{
  "schema": "test-cli/report@2",
  "toolVersion": "v0.1.0 (abc1234)",
  "generatedAt": "2026-06-09T00:00:00Z",
  "root": "/repo",
  "languages": ["go", "python"],
  "test": {
    "summary": { "total": 128, "passed": 124, "failed": 3, "skipped": 1, "errors": 0, "durationMs": 4210 },
    "suites": [
      {
        "name": "app.users", "language": "python", "file": "tests/test_users.py",
        "summary": { "total": 12, "passed": 11, "failed": 1, "skipped": 0, "errors": 0, "durationMs": 80 },
        "cases": [
          { "name": "test_create", "classname": "TestUsers", "status": "passed", "durationMs": 5 },
          { "name": "test_delete", "classname": "TestUsers", "status": "failed",
            "message": "AssertionError: expected 204", "detail": "traceback …" }
        ]
      }
    ]
  },
  "coverage": {
    "summary": { "lines": { "covered": 1820, "total": 2200, "pct": 82.7 },
                 "branches": { "covered": 300, "total": 410, "pct": 73.2 } },
    "files": [
      { "path": "app/users.py", "language": "python",
        "lines": { "covered": 40, "total": 48, "pct": 83.3 },
        "branches": { "covered": 8, "total": 12, "pct": 66.7 },
        "lineHits": [ { "line": 1, "hits": 5 }, { "line": 2, "hits": 0 } ] }
    ]
  },
  "quality": {
    "score": 68,
    "grade": "D",
    "risk": "high",
    "dimensions": [
      { "id": "reliability", "name": "Test reliability", "score": 60,
        "weight": 35, "detail": "124 passed, 3 failed, 0 errors" }
    ],
    "summary": { "critical": 0, "high": 1, "medium": 0, "low": 0, "info": 0 },
    "findings": [
      { "id": "TEST-003", "severity": "high", "category": "reliability",
        "title": "Failing tests", "detail": "Expected behavior is contradicted by the current test run.",
        "evidence": "3 failed of 128 total", "recommendation": "Start with failure details..." }
    ],
    "static": { "sourceFiles": 80, "testFiles": 24, "sourceLines": 6200,
      "testLines": 2100, "testToSourceRatio": 0.34 },
    "slowTests": [],
    "coverageHotspots": [],
    "comparison": {
      "baselineGeneratedAt": "2026-06-08T00:00:00Z", "regressed": true,
      "score": { "before": 74, "after": 68, "delta": -6 },
      "lineCoveragePct": { "before": 84.1, "after": 82.7, "delta": -1.4 },
      "newFailures": [], "resolvedFailures": [],
      "coverageRegressions": [], "coverageImprovements": []
    },
    "history": {
      "runs": 3,
      "windowStart": "2026-06-07T00:00:00Z", "windowEnd": "2026-06-09T00:00:00Z",
      "qualityScore": { "first": 74, "last": 68, "delta": -6, "slope": -3, "direction": "decreasing" },
      "lineCoveragePct": { "first": 84.1, "last": 82.7, "delta": -1.4, "slope": -0.7, "direction": "decreasing" },
      "samples": [],
      "flakyTests": [
        { "language": "python", "suite": "app.users", "name": "test_delete",
          "observations": 3, "passed": 2, "failed": 1, "skipped": 0,
          "flakeRatePct": 33.3, "recentStatuses": ["passed", "failed", "passed"] }
      ]
    },
    "changes": {
      "base": "origin/main", "filesChanged": 7, "sourceFilesChanged": 2,
      "changedLines": 31, "coverableLines": 18, "coveredLines": 15,
      "uncoveredLines": 3, "unmappedLines": 13, "filesWithoutCoverage": 0,
      "coveragePct": 83.3,
      "files": [
        { "path": "app/users.py", "language": "python", "changedLines": 12,
          "coverableLines": 8, "coveredLines": 6, "uncoveredLines": 2,
          "unmappedLines": 4, "coveragePct": 75, "coverageReported": true,
          "uncoveredLineNumbers": [42, 47] }
      ]
    }
  },
  "messages": []
}
```

A run is **green** when `test.summary.failed == 0 && test.summary.errors == 0`.

---

## Output formats & visualization

| Format | File | Use |
|---|---|---|
| `json` | `report.json` | The normalized schema — read this programmatically. |
| `junit` | `junit.xml` | Aggregated JUnit for CI test reporters. |
| `cobertura` | `coverage.cobertura.xml` | Aggregated coverage for Codecov / CI gates. |
| `markdown` | `report.md` | PR comments / GitHub job summaries. |
| `html` | `index.html` + `insights.html` + `coverage/` | QA dashboard, maps, and source heatmaps. |
| `stdout` | — | Concise terminal summary (failures + lowest-coverage files). |

The HTML output is a self-contained static site:

- **`index.html`** — status, quality score, prioritized QA findings,
  per-language breakdown, failures, and lowest-coverage files.
- **`insights.html`** — weighted quality dimensions, finding evidence and next
  actions, baseline regression deltas, changed-file risk maps and patch-line
  links, multi-run score/coverage trend charts, flaky status timelines, a
  test-duration heatmap, coverage risk matrix, and static test smells.
- **`coverage/index.html`** — a file-size-weighted coverage treemap, aggregated
  directory coverage tree, and filterable file table.
- **`coverage/<file>.html`** — the source file rendered line-by-line with a
  green/red **heatmap** and per-line hit counts, just like Codecov.

---

## Exit codes

A stable contract for CI and AI agents:

| Code | Meaning |
|---|---|
| `0` | Tests passed and enabled coverage/quality gates were satisfied. |
| `1` | Test failures/errors, or an enabled coverage/quality/regression/flaky/changed-line gate failed. |
| `2` | Usage error (bad arguments). |
| `3` | Could not run/parse (no artifacts produced). |
| `4` | Toolchain/environment problem. |

---

## Configuration

Configuration is optional. With no file present, built-in defaults are used for
every language. A `.test-cli.json` discovered in the target directory (or any
parent) overrides them. JSON keeps the binary dependency-free.

```json
{
  "outputDir": "reports/test",
  "formats": ["stdout", "json", "junit", "cobertura", "markdown", "html"],
  "failUnder": 80,
  "failQuality": 70,
  "languages": ["go", "python"],
  "commands": {
    "typescript": [
      ["npx", "--no-install", "vitest", "run", "--coverage", "--coverage.reporter=cobertura", "--coverage.reportsDirectory={out}", "--reporter=junit", "--outputFile={out}/junit.xml"]
    ]
  }
}
```

For regression gating, add `"baseline": "reports/main/report.json"` and
`"failOnRegression": true` after your CI workflow has downloaded or restored
that baseline file. Relative baseline paths are resolved from the config file.

For history analysis, add `"history": ["reports/run-1.json", "reports/run-2.json"]`.
Optionally add `"failOnFlaky": true` to gate tests that have both pass and
fail/error states across at least three observations. Relative history paths
are also resolved from the config file.

For changed-code gating, add `"diffBase": "origin/main"` and
`"failDiffCoverage": 80`. Patch coverage counts only added/modified executable
production lines; test files, docs, deleted lines, and non-executable lines do
not dilute the percentage. A changed source file missing line-level coverage
evidence fails the enabled gate rather than silently passing.

- `commands` overrides a language's default test command. Each command is an
  `argv` array; the placeholders `{out}` (the per-language raw output directory)
  and `{root}` (the project root) are substituted at run time.
- See [`examples/.test-cli.json`](examples/.test-cli.json).

---

## GitHub Action

The composite action lives in its own repository,
[**jhl-labs/test-cli-action**](https://github.com/jhl-labs/test-cli-action)
(mirroring the `security-cli` / `security-cli-action` split). It installs the
binary, runs it, annotates failing tests, writes a job summary, and exposes
machine-readable outputs.

```yaml
- name: Run standardized tests & coverage
  id: tests
  uses: jhl-labs/test-cli-action@main
  with:
    profile: ci
    target: .
    fail-under: "80"
    formats: stdout,json,junit,cobertura,markdown,html
```

### Inputs (selected)

| Input | Default | Description |
|---|---|---|
| `command` | `run` | test-cli command. |
| `target` | `.` | Path/target. |
| `profile` | `ci` | `default` · `ci` · `release`. |
| `languages` | _(auto)_ | Comma-separated language restriction. |
| `output-dir` | `reports/test` | Report directory. |
| `formats` | `stdout,json,junit,cobertura,markdown,html` | Output formats. |
| `fail-under` | `0` | Coverage gate (%). |
| `fail-on-test-failure` | `true` | Fail the job on test failure / gate. |
| `version` | `latest` | test-cli version to install. |

### Outputs

| Output | Description |
|---|---|
| `passed` | `true` when green and gate satisfied. |
| `tests-total` / `tests-failed` | Test counts. |
| `coverage-pct` | Total line coverage %. |
| `exit-code` | Raw test-cli exit code. |
| `report-dir` | Directory containing the reports. |

A full consumer example is in [`examples/github-actions.yml`](examples/github-actions.yml).

---

## For AI agents

`test-cli` is built to be agent-friendly: one command, one schema, stable exit
codes. Generate a ready-to-use skill that teaches an agent how to drive it:

```bash
test-cli generate-skill --out .claude/skills          # writes .claude/skills/test-runner/SKILL.md
test-cli generate-skill --stdout                       # print to stdout
```

The skill documents the recommended loop: `doctor` → `run` → read `report.json`
→ inspect failing `cases[]` (their `detail` holds framework output) → fix →
re-run. Agents should branch on the exit code and read `report.json` rather than
parsing human output.

---

## Project layout

```
cmd/test-cli/          Entry point.
internal/
  version/             Build metadata (-ldflags).
  model/               Normalized schema + Normalize() rollups (source of truth).
  analysis/            Standard QA scoring/findings + portable static test-code rules.
  ingest/              Content-sniffing parsers (junit, go-json, cobertura, lcov, jacoco, go-cover).
  lang/                Declarative per-language adapters (detection + commands + artifact globs).
  config/              Optional .test-cli.json loader (stdlib JSON).
  runner/              Orchestration: detect → run → locate → ingest → normalize → analyze.
  report/              Renderers + embedded HTML dashboard, maps, trees, and heatmaps (go:embed).
  cli/                 Command dispatch & flags + the generate-skill template.
scripts/               Cross-platform release build + install script.
.github/workflows/     ci.yml (lint/test/build/self-check) · release.yml (tag-driven).
```

See [`CLAUDE.md`](CLAUDE.md) for engineering conventions.

---

## Development

```bash
make build        # build bin/test-cli with version ldflags
make test         # Go unit tests + repository smoke TC (requires Node.js)
make lint         # gofmt check + go vet
make run-self     # build then run test-cli against this repo
make release-dry  # cross-compile all six platforms into dist/release
```

Conventions: **standard library only** (no third-party deps), no CLI framework
(stdlib `flag` + a dispatch `switch`), declarative language adapters, and all
rollups computed in `model.Normalize()`. Adding a language that can emit JUnit +
Cobertura/LCOV usually means editing only `internal/lang/adapters.go`.

---

## Versioning & releases

- **Semantic versioning** with `vMAJOR.MINOR.PATCH` git tags.
- Version/commit/date are embedded at build time via `-ldflags` into
  `internal/version`.
- Pushing a `v*` tag triggers `release.yml`, which runs the quality gate,
  cross-compiles six platforms (bare binaries + archives + `SHA256SUMS`), and
  publishes a GitHub Release with auto-generated notes.
- The schema id (`test-cli/report@N`) is bumped only on breaking report changes.

---

## License

MIT — see [LICENSE](LICENSE).
