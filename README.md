# test-cli

**Standardized test & coverage reports across six languages — one binary, one schema.**

`test-cli` is a single, dependency-free, cross-platform Go binary that detects a
project's languages, runs their tests **with coverage**, and normalizes the
results into one **language-agnostic report**. It then renders that report as
machine-readable interchange (JSON, JUnit, Cobertura), human summaries
(stdout, Markdown), and rich visualizations (an HTML dashboard plus a
code-cov style coverage heatmap).

It is built to be driven by **AI agents** and to run as a **GitHub Action**.

| | |
|---|---|
| **Languages** | Python · TypeScript/JavaScript · Go · Rust · C#/.NET · Java/Kotlin |
| **Platforms** | linux, macOS, windows · amd64 + arm64 |
| **Dependencies** | none (Go standard library only) |
| **Outputs** | `report.json` · `junit.xml` · `coverage.cobertura.xml` · `report.md` · HTML dashboard + coverage heatmap |
| **Schema** | `test-cli/report@1` |

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

---

## How it works

```
 detect ─▶ run native test commands ─▶ locate artifacts ─▶ ingest (sniff+parse) ─▶ model.Report ─▶ render
 (lang)      (pytest/jest/go test…)      (junit.xml, …)        (JUnit/Cobertura/…)     (Normalize)    (json/html/…)
```

The normalizer is the heart of the tool: native artifacts are **content-sniffed**
(not guessed from filenames) and projected onto one schema, so a Python pytest
run and a Go `go test` run produce structurally identical reports.

| Language | Test command (default) | Test format | Coverage format |
|---|---|---|---|
| Python | `pytest --junitxml … --cov … --cov-report=xml` | JUnit XML | Cobertura |
| TypeScript/JS | `jest --reporters=jest-junit --coverage` | JUnit XML | Cobertura / LCOV |
| Go | `go test ./... -json -coverprofile` | `go test -json` | Go coverage profile |
| Rust | `cargo llvm-cov --cobertura nextest` | JUnit XML | Cobertura |
| C#/.NET | `dotnet test --logger junit --collect "XPlat Code Coverage"` | JUnit XML | Cobertura |
| Java/Kotlin | `mvn test jacoco:report` | Surefire JUnit XML | JaCoCo XML |

Every default command is overridable per project (see [Configuration](#configuration)).
Because ingestion sniffs format from content, any framework that can emit JUnit +
Cobertura/LCOV works out of the box — point `test-cli ingest` at the artifacts.

---

## Install

```bash
# Latest release (Linux/macOS):
curl -fsSL https://raw.githubusercontent.com/jhl-labs/test-cli/main/scripts/install.sh | bash

# Pin a version into a directory:
curl -fsSL https://raw.githubusercontent.com/jhl-labs/test-cli/main/scripts/install.sh \
  | VERSION=v0.1.0 INSTALL_DIR="$HOME/.local/bin" bash

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

# A single language:
test-cli run . --lang go

# No toolchain? Ingest existing artifacts from any framework:
test-cli ingest --tests junit.xml --coverage coverage.xml -o reports/test

# Re-render visualizations from a saved report (no re-run):
test-cli report --in reports/test/report.json --format html,markdown
```

Open `reports/test/index.html` for the dashboard and
`reports/test/coverage/index.html` for the coverage heatmap.

---

## Commands

| Command | Purpose |
|---|---|
| `run [target]` | Detect languages, run tests with coverage, write reports. Aliases: `scan`, `diagnose`. |
| `ingest` | Build reports from existing artifacts (`--tests`, `--coverage`); no execution. |
| `report` | Re-render reports from a previously generated `report.json` (`--in`). |
| `detect [target]` | List which languages/toolchains are detected (`--json` for structured output). |
| `doctor [target]` | Check that detected languages' toolchains are installed (`--all` for every language). |
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
| `--no-run` | Ingest existing artifacts without executing tests. |
| `--timeout DUR` | Per-command timeout (default `20m`). |
| `--quiet` | Suppress progress logging. |

---

## The standardized report (`report.json`)

`report.json` (schema `test-cli/report@1`) is the source of truth; every other
format is rendered from it. Read **this**, not stdout.

```jsonc
{
  "schema": "test-cli/report@1",
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
| `html` | `index.html` + `coverage/` | Dashboard + **code-cov style coverage heatmap**. |
| `stdout` | — | Concise terminal summary (failures + lowest-coverage files). |

The HTML output is a self-contained static site:

- **`index.html`** — a dashboard with status, per-language breakdown, expandable
  failure details, and the lowest-coverage files.
- **`coverage/index.html`** — a filterable, sortable file list with coverage bars.
- **`coverage/<file>.html`** — the source file rendered line-by-line with a
  green/red **heatmap** and per-line hit counts, just like Codecov.

---

## Exit codes

A stable contract for CI and AI agents:

| Code | Meaning |
|---|---|
| `0` | Tests passed and the coverage gate (`--fail-under`) was satisfied. |
| `1` | Test failures/errors, or line coverage below `--fail-under`. |
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
  "languages": ["go", "python"],
  "commands": {
    "typescript": [
      ["npx", "--yes", "vitest", "run", "--coverage", "--reporter=junit", "--outputFile={out}/junit.xml"]
    ]
  }
}
```

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
  ingest/              Content-sniffing parsers (junit, go-json, cobertura, lcov, jacoco, go-cover).
  lang/                Declarative per-language adapters (detection + commands + artifact globs).
  config/              Optional .test-cli.json loader (stdlib JSON).
  runner/              Orchestration: detect → run → locate → ingest → normalize.
  report/              Renderers + embedded HTML templates (go:embed).
  cli/                 Command dispatch & flags + the generate-skill template.
scripts/               Cross-platform release build + install script.
.github/workflows/     ci.yml (lint/test/build/self-check) · release.yml (tag-driven).
action.yaml            Composite GitHub Action.
```

See [`CLAUDE.md`](CLAUDE.md) for engineering conventions.

---

## Development

```bash
make build        # build bin/test-cli with version ldflags
make test         # go test ./...
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
