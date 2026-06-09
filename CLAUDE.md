# test-cli — project guide

`test-cli` is a single, cross-platform Go binary that runs a project's tests
**with coverage** across six ecosystems and emits a **language-agnostic,
standardized report** (JSON + JUnit + Cobertura + Markdown + an HTML dashboard
and code-cov style coverage heatmap). It is designed to be driven by AI agents
and to run as a GitHub Action.

Supported languages: **python, typescript/javascript, go, rust, c#/.net, java**.

## Architecture

```
cmd/test-cli/         Entry point (os.Exit(cli.Run(...))).
internal/
  version/            Build metadata, injected via -ldflags.
  model/              The normalized schema (Report/TestReport/CoverageReport). The single source of truth.
  ingest/             Parsers that project native artifacts onto the model. Format is sniffed from content:
                      JUnit XML, go test -json, Cobertura, LCOV, JaCoCo, Go coverage profiles.
  lang/               Declarative per-language adapters: detection + default test command + artifact globs.
  config/             Optional .test-cli.json project config (stdlib JSON; no third-party deps).
  runner/             Orchestration: detect → run commands → locate artifacts → ingest → Normalize().
  report/             Renderers: json, junit (xml.go), cobertura (xml.go), markdown/stdout (text.go),
                      html dashboard + heatmap (html.go + templates/*.tmpl, go:embed).
  cli/                Command dispatch + flag parsing (run/ingest/report/detect/doctor/generate-skill).
```

Data flow: **native artifact → ingest (sniff + parse) → model.Report → Normalize() → report.Write(format)**.
Every output format is derived from the same `model.Report`, so they never disagree.

## Conventions (match these when editing)

- **Zero third-party dependencies.** Standard library only. Do not add modules to
  `go.mod`. This keeps the binary tiny, reproducible, and offline-buildable.
- **No CLI framework.** Dispatch is a `switch` in `internal/cli/cli.go`; flags use
  the stdlib `flag` package. Mirror the sibling `security-cli` ergonomics.
- **Adapters are declarative.** To add/adjust a language, edit
  `internal/lang/adapters.go` only. Parsing is generic and content-sniffed, so a
  new language usually needs no new parser if it can emit JUnit + Cobertura/LCOV.
- **Stable contracts.** Exit codes (`internal/cli/cli.go`) and the JSON schema id
  (`model.SchemaID`) are public contracts consumed by CI and agents. Bump the
  schema id (`test-cli/report@N`) on breaking changes.
- **Rollups live in `model.Normalize()`.** Build a report by appending raw
  suites/files, then call `Normalize()` once; never hand-compute summaries.
- **HTML is templated + embedded** via `go:embed`; keep templates "dumb" and
  precompute view models in Go.

## Exit codes (stable)

| 0 | success | 1 | test failure / coverage gate | 2 | usage | 3 | run failure | 4 | environment |

## Workflow

```bash
make build        # build bin/test-cli with version ldflags
make test         # go test ./...
make lint         # gofmt -l check + go vet
make run-self     # build, then run test-cli against this repo
make release-dry  # cross-compile all 6 platforms into dist/release
```

CI (`.github/workflows/ci.yml`) enforces: gofmt clean, `go mod verify`, `go vet`,
`go test -race`, cross-compile matrix, and a self-check run. Releases are
tag-driven (`v*` → `.github/workflows/release.yml`).

## Definition of done for a change

1. `make lint test` is green.
2. New parsing/normalization logic has a unit test (see `internal/ingest/*_test.go`).
3. `make run-self` still produces a valid `reports/test/report.json` and HTML.
4. If you changed the report shape, you updated `model.SchemaID` and the
   `generate-skill` template (`internal/cli/templates/skill.md.tmpl`).
