package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jhl-labs/test-cli/internal/analysis"
	"github.com/jhl-labs/test-cli/internal/config"
	"github.com/jhl-labs/test-cli/internal/lang"
	"github.com/jhl-labs/test-cli/internal/model"
	"github.com/jhl-labs/test-cli/internal/report"
	"github.com/jhl-labs/test-cli/internal/runner"
	"github.com/jhl-labs/test-cli/internal/version"
)

// stringList is a repeatable string flag (e.g. --format a --format b) that also
// accepts comma-separated values (--format a,b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

type commonFlags struct {
	outputDir        string
	formats          stringList
	langs            stringList
	profile          string
	failUnder        float64
	failQuality      int
	baseline         string
	failOnRegression bool
	history          stringList
	failOnFlaky      bool
	diffBase         string
	failDiffCoverage float64
	noRun            bool
	quiet            bool
	timeout          time.Duration
	tests            stringList
	coverage         stringList
}

func (cf *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&cf.outputDir, "output-dir", "", "output directory for reports")
	fs.StringVar(&cf.outputDir, "o", "", "output directory for reports (shorthand)")
	fs.Var(&cf.formats, "format", "report format (repeatable / comma-separated)")
	fs.Var(&cf.formats, "formats", "alias of --format")
	fs.Var(&cf.langs, "lang", "restrict to language (repeatable)")
	fs.Var(&cf.langs, "language", "alias of --lang")
	fs.StringVar(&cf.profile, "profile", "default", "preset: default | ci | release")
	fs.Float64Var(&cf.failUnder, "fail-under", 0, "fail if total line coverage < PCT")
	fs.IntVar(&cf.failQuality, "fail-quality", 0, "fail if standardized quality score < SCORE")
	fs.StringVar(&cf.baseline, "baseline", "", "compare against an existing report.json")
	fs.BoolVar(&cf.failOnRegression, "fail-on-regression", false, "fail when comparison against --baseline regresses")
	fs.Var(&cf.history, "history", "prior report.json for trends/flaky analysis (repeatable)")
	fs.BoolVar(&cf.failOnFlaky, "fail-on-flaky", false, "fail when repeated history identifies flaky tests")
	fs.StringVar(&cf.diffBase, "diff-base", "", "Git ref for changed-line coverage analysis")
	fs.Float64Var(&cf.failDiffCoverage, "fail-diff-coverage", 0, "fail if changed executable-line coverage < PCT")
	fs.BoolVar(&cf.noRun, "no-run", false, "do not execute tests; ingest existing artifacts")
	fs.BoolVar(&cf.quiet, "quiet", false, "suppress progress logging")
	fs.DurationVar(&cf.timeout, "timeout", 20*time.Minute, "per-command timeout")
	fs.Var(&cf.tests, "tests", "explicit test artifact to ingest (repeatable)")
	fs.Var(&cf.coverage, "coverage", "explicit coverage artifact to ingest (repeatable)")
}

func runRun(args []string, stdout, stderr io.Writer) int {
	return execute(args, stdout, stderr, false, false)
}

func runIngest(args []string, stdout, stderr io.Writer) int {
	return execute(args, stdout, stderr, true, false)
}

// runAnalyze performs repository/test-code static analysis and opportunistically
// ingests existing artifacts, but never executes a project command.
func runAnalyze(args []string, stdout, stderr io.Writer) int {
	return execute(args, stdout, stderr, false, true)
}

// execute is shared by run, ingest, and analyze. Ingest/analyze force --no-run.
func execute(args []string, stdout, stderr io.Writer, ingestMode, analyzeMode bool) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf commonFlags
	cf.bind(fs)
	target, err := parseWithTarget(fs, args, ".")
	if err != nil {
		return ExitUsage
	}
	if !validProfile(cf.profile) {
		fmt.Fprintf(stderr, "test-cli: unknown profile %q (expected default, ci, or release)\n", cf.profile)
		return ExitUsage
	}
	if cf.timeout < 0 {
		fmt.Fprintln(stderr, "test-cli: timeout must not be negative")
		return ExitUsage
	}
	if cf.failUnder < 0 || cf.failUnder > 100 || cf.failQuality < 0 || cf.failQuality > 100 || cf.failDiffCoverage < 0 || cf.failDiffCoverage > 100 {
		fmt.Fprintln(stderr, "test-cli: coverage and quality thresholds must be between 0 and 100")
		return ExitUsage
	}
	root, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitUsage
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "test-cli: target is not a readable directory: %s\n", root)
		return ExitUsage
	}

	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	applyProfile(&cfg, &cf)
	if err := validateCommandOverrides(cfg.Commands); err != nil {
		fmt.Fprintf(stderr, "test-cli: config: %v\n", err)
		return ExitUsage
	}

	selectedLanguages := []string(cf.langs)
	languageFlag := flagProvided(fs, "lang", "language")
	if !languageFlag {
		selectedLanguages = cfg.Languages
	} else if len(selectedLanguages) == 0 {
		fmt.Fprintln(stderr, "test-cli: language must not be empty")
		return ExitUsage
	}
	selectedLanguages, err = normalizeSelection(selectedLanguages, lang.Names(), "language")
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitUsage
	}
	cfg.Languages = selectedLanguages

	baselinePath := cf.baseline
	if !flagProvided(fs, "baseline") && cfg.Baseline != "" {
		baselinePath = cfg.Baseline
		if !filepath.IsAbs(baselinePath) && cfg.Path != "" {
			baselinePath = filepath.Join(filepath.Dir(cfg.Path), baselinePath)
		}
	}
	failOnRegression := cf.failOnRegression
	if !flagProvided(fs, "fail-on-regression") {
		failOnRegression = cfg.FailOnRegression
	}
	if failOnRegression && baselinePath == "" {
		fmt.Fprintln(stderr, "test-cli: --fail-on-regression requires --baseline (or config baseline)")
		return ExitUsage
	}
	historyPaths := []string(cf.history)
	if !flagProvided(fs, "history") {
		historyPaths = configRelativePaths(cfg.History, cfg.Path)
	}
	historyPaths = uniquePaths(historyPaths)
	failOnFlaky := cf.failOnFlaky
	if !flagProvided(fs, "fail-on-flaky") {
		failOnFlaky = cfg.FailOnFlaky
	}
	if failOnFlaky && len(historyPaths) < 2 {
		fmt.Fprintln(stderr, "test-cli: --fail-on-flaky requires at least two --history reports (or config history)")
		return ExitUsage
	}
	if cfg.RiskChurnDays > 0 {
		analysis.ChurnWindowDays = cfg.RiskChurnDays
	}
	diffBase := cf.diffBase
	if !flagProvided(fs, "diff-base") {
		diffBase = cfg.DiffBase
	}
	failDiffCoverage := cf.failDiffCoverage
	if !flagProvided(fs, "fail-diff-coverage") {
		failDiffCoverage = cfg.FailDiffCoverage
	}
	if failDiffCoverage > 0 && diffBase == "" {
		fmt.Fprintln(stderr, "test-cli: --fail-diff-coverage requires --diff-base (or config diffBase)")
		return ExitUsage
	}
	failUnder := cf.failUnder
	if !flagProvided(fs, "fail-under") {
		failUnder = cfg.FailUnder
	}
	failQuality := cf.failQuality
	if !flagProvided(fs, "fail-quality") {
		failQuality = cfg.FailQuality
	}
	if failUnder < 0 || failUnder > 100 || failQuality < 0 || failQuality > 100 || failDiffCoverage < 0 || failDiffCoverage > 100 {
		fmt.Fprintln(stderr, "test-cli: configured coverage and quality thresholds must be between 0 and 100")
		return ExitUsage
	}

	// A relative --output-dir is resolved against the current working directory
	// (standard tool behavior), NOT against the target being tested. This keeps
	// `test-cli run subdir -o reports/test` writing to ./reports/test even when
	// the target is a subdirectory.
	outDir := cfg.OutputDir
	if flagProvided(fs, "output-dir", "o") {
		outDir = cf.outputDir
	}
	outDir = strings.TrimSpace(outDir)
	if outDir == "" {
		fmt.Fprintln(stderr, "test-cli: output directory must not be empty")
		return ExitUsage
	}
	if !filepath.IsAbs(outDir) {
		if wd, werr := os.Getwd(); werr == nil {
			outDir = filepath.Join(wd, outDir)
		}
	}
	formats := []string(cf.formats)
	if !flagProvided(fs, "format", "formats") {
		formats = cfg.Formats
	}
	formats, err = normalizeSelection(formats, append([]string{report.FormatStdout}, report.AllFormats...), "format")
	if err != nil || len(formats) == 0 {
		if err == nil {
			err = fmt.Errorf("at least one report format is required")
		}
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitUsage
	}

	var logw io.Writer = stderr
	if cf.quiet {
		logw = io.Discard
	}

	opts := runner.Options{
		Root:           root,
		OutDir:         outDir,
		ToolVersion:    version.Long(),
		Languages:      selectedLanguages,
		Config:         cfg,
		NoRun:          ingestMode || analyzeMode || cf.noRun,
		Timeout:        cf.timeout,
		Log:            logw,
		IngestTests:    []string(cf.tests),
		IngestCoverage: []string(cf.coverage),
		// In ingest mode with explicit artifacts, don't also auto-scan the repo
		// for language toolchains — just normalize what was handed in.
		SkipDetect: ingestMode && len(cf.langs) == 0 && (len(cf.tests) > 0 || len(cf.coverage) > 0),
	}

	fmt.Fprintf(logw, "test-cli %s — %s\n", version.Short(), root)
	rep, err := runner.Run(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	if baselinePath != "" {
		if err := compareBaseline(rep, baselinePath); err != nil {
			fmt.Fprintf(stderr, "test-cli: baseline: %v\n", err)
			return ExitRunFailure
		}
	}
	if len(historyPaths) > 0 {
		historical, err := loadHistoricalReports(historyPaths)
		if err != nil {
			fmt.Fprintf(stderr, "test-cli: history: %v\n", err)
			return ExitRunFailure
		}
		analysis.AnalyzeHistory(rep, historical)
		if failOnFlaky && rep.Quality.History.Runs < 3 {
			fmt.Fprintln(stderr, "test-cli: --fail-on-flaky requires two distinct historical run observations")
			return ExitUsage
		}
	}
	if diffBase != "" {
		if err := analysis.AnalyzeGitChanges(rep, root, diffBase); err != nil {
			fmt.Fprintf(stderr, "test-cli: changed-line analysis: %v\n", err)
			return ExitRunFailure
		}
	}

	if rep.Test.Summary.Total == 0 && rep.Coverage.Summary.Lines.Total == 0 && (!analyzeMode || rep.Quality.Static.SourceFiles+rep.Quality.Static.TestFiles == 0) {
		fmt.Fprintln(stderr, "test-cli: no test results or coverage were produced")
		if len(rep.Messages) > 0 {
			for _, m := range rep.Messages {
				fmt.Fprintf(stderr, "  - %s\n", m)
			}
		}
		return ExitRunFailure
	}

	if code := renderAll(rep, formats, outDir, root, stdout, stderr); code != ExitOK {
		return code
	}

	return gateExitCode(rep, failUnder, failQuality, failDiffCoverage, failOnRegression, failOnFlaky, stderr)
}

// renderAll writes every requested format and prints a stdout summary when
// requested (or when no file formats are given).
func renderAll(rep *model.Report, formats []string, outDir, root string, stdout, stderr io.Writer) int {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	printStdout := false
	wroteAny := false
	for _, f := range formats {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f == report.FormatStdout {
			printStdout = true
			continue
		}
		paths, err := report.Write(rep, f, outDir, root)
		if err != nil {
			fmt.Fprintf(stderr, "test-cli: render %s: %v\n", f, err)
			return ExitRunFailure
		}
		wroteAny = true
		for _, p := range paths {
			if rel, err := filepath.Rel(root, p); err == nil {
				fmt.Fprintf(stderr, "wrote %s\n", rel)
			} else {
				fmt.Fprintf(stderr, "wrote %s\n", p)
			}
		}
	}
	if printStdout || !wroteAny {
		report.WriteStdout(rep, stdout)
	}
	return ExitOK
}

// gateExitCode applies the test + coverage gates and returns the exit code.
func gateExitCode(rep *model.Report, failUnder float64, failQuality int, failDiffCoverage float64, failOnRegression, failOnFlaky bool, stderr io.Writer) int {
	if !rep.Test.Summary.Passing() {
		return ExitTestFailure
	}
	if failUnder > 0 {
		if rep.Coverage.Summary.Lines.Total == 0 {
			fmt.Fprintf(stderr, "test-cli: line coverage is unavailable; threshold %.1f%% cannot be satisfied\n", failUnder)
			return ExitTestFailure
		}
		if rep.Coverage.Summary.Lines.Pct < failUnder {
			fmt.Fprintf(stderr, "test-cli: line coverage %.1f%% is below threshold %.1f%%\n",
				rep.Coverage.Summary.Lines.Pct, failUnder)
			return ExitTestFailure
		}
	}
	if failQuality > 0 && rep.Quality.Score < failQuality {
		fmt.Fprintf(stderr, "test-cli: quality score %d is below threshold %d\n", rep.Quality.Score, failQuality)
		return ExitTestFailure
	}
	if failDiffCoverage > 0 && rep.Quality.Changes != nil && rep.Quality.Changes.SourceFilesChanged > 0 {
		changes := rep.Quality.Changes
		if changes.FilesWithoutCoverage > 0 {
			fmt.Fprintf(stderr, "test-cli: %d changed source file(s) lack line-level coverage evidence\n", changes.FilesWithoutCoverage)
			return ExitTestFailure
		}
		if changes.CoverableLines > 0 && changes.CoveragePct < failDiffCoverage {
			fmt.Fprintf(stderr, "test-cli: changed-line coverage %.1f%% is below threshold %.1f%%\n", changes.CoveragePct, failDiffCoverage)
			return ExitTestFailure
		}
	}
	if failOnRegression && rep.Quality.Comparison != nil && rep.Quality.Comparison.Regressed {
		fmt.Fprintln(stderr, "test-cli: quality regressed against the baseline report")
		return ExitTestFailure
	}
	if failOnFlaky && rep.Quality.History != nil && len(rep.Quality.History.FlakyTests) > 0 {
		fmt.Fprintf(stderr, "test-cli: %d flaky test candidate(s) found across %d runs\n", len(rep.Quality.History.FlakyTests), rep.Quality.History.Runs)
		return ExitTestFailure
	}
	return ExitOK
}

// applyProfile adjusts defaults based on the chosen preset.
func applyProfile(cfg *config.Config, cf *commonFlags) {
	if cfg.HasExplicitFormats() {
		return
	}
	switch cf.profile {
	case "ci":
		cfg.Formats = []string{"stdout", "json", "junit", "cobertura", "html"}
	case "release":
		cfg.Formats = []string{"json", "junit", "cobertura", "markdown", "html"}
	}
}

// runReport re-renders from an existing report.json without re-running tests.
func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "path to an existing report.json")
	sourceRootFlag := fs.String("source-root", "", "trusted repository root for source heatmaps and Git diff analysis")
	var cf commonFlags
	cf.bind(fs)
	pos, err := parseWithTarget(fs, args, "")
	if err != nil {
		return ExitUsage
	}
	if !validProfile(cf.profile) {
		fmt.Fprintf(stderr, "test-cli: unknown profile %q (expected default, ci, or release)\n", cf.profile)
		return ExitUsage
	}
	if cf.timeout < 0 {
		fmt.Fprintln(stderr, "test-cli: timeout must not be negative")
		return ExitUsage
	}
	if cf.failUnder < 0 || cf.failUnder > 100 || cf.failQuality < 0 || cf.failQuality > 100 || cf.failDiffCoverage < 0 || cf.failDiffCoverage > 100 {
		fmt.Fprintln(stderr, "test-cli: coverage and quality thresholds must be between 0 and 100")
		return ExitUsage
	}
	if cf.failOnRegression && cf.baseline == "" {
		fmt.Fprintln(stderr, "test-cli: --fail-on-regression requires --baseline")
		return ExitUsage
	}
	cf.history = uniquePaths(cf.history)
	if cf.failOnFlaky && len(cf.history) < 2 {
		fmt.Fprintln(stderr, "test-cli: --fail-on-flaky requires at least two --history reports")
		return ExitUsage
	}
	if cf.failDiffCoverage > 0 && cf.diffBase == "" {
		fmt.Fprintln(stderr, "test-cli: --fail-diff-coverage requires --diff-base")
		return ExitUsage
	}
	source := *in
	if source == "" {
		source = pos
	}
	if source == "" {
		fmt.Fprintln(stderr, "test-cli report: provide --in report.json (or a path argument)")
		return ExitUsage
	}
	sourceRoot := ""
	if strings.TrimSpace(*sourceRootFlag) != "" {
		sourceRoot, err = filepath.Abs(*sourceRootFlag)
		if err != nil {
			fmt.Fprintf(stderr, "test-cli: source root: %v\n", err)
			return ExitUsage
		}
		info, statErr := os.Stat(sourceRoot)
		if statErr != nil || !info.IsDir() {
			fmt.Fprintf(stderr, "test-cli: source root is not a readable directory: %s\n", sourceRoot)
			return ExitUsage
		}
	}
	if cf.diffBase != "" && sourceRoot == "" {
		fmt.Fprintln(stderr, "test-cli: report --diff-base requires an explicit --source-root")
		return ExitUsage
	}
	data, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	var rep model.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		fmt.Fprintf(stderr, "test-cli: invalid report.json: %v\n", err)
		return ExitRunFailure
	}
	// Re-rendering can add the current analyzer's quality section to an older
	// report, so the emitted document follows the current schema contract.
	rep.Schema = model.SchemaID
	rep.Normalize()
	// The embedded root is report data, not authority to read the local
	// filesystem. Only an explicit --source-root enables source rescanning and
	// source-backed heatmaps while the original root remains report metadata.
	analysis.Evaluate(&rep, sourceRoot)
	if cf.baseline != "" {
		if err := compareBaseline(&rep, cf.baseline); err != nil {
			fmt.Fprintf(stderr, "test-cli: baseline: %v\n", err)
			return ExitRunFailure
		}
	}
	if len(cf.history) > 0 {
		historical, err := loadHistoricalReports(cf.history)
		if err != nil {
			fmt.Fprintf(stderr, "test-cli: history: %v\n", err)
			return ExitRunFailure
		}
		analysis.AnalyzeHistory(&rep, historical)
		if cf.failOnFlaky && rep.Quality.History.Runs < 3 {
			fmt.Fprintln(stderr, "test-cli: --fail-on-flaky requires two distinct historical run observations")
			return ExitUsage
		}
	}
	if cf.diffBase != "" {
		if err := analysis.AnalyzeGitChanges(&rep, sourceRoot, cf.diffBase); err != nil {
			fmt.Fprintf(stderr, "test-cli: changed-line analysis: %v\n", err)
			return ExitRunFailure
		}
	}

	outDir := filepath.Dir(source)
	if flagProvided(fs, "output-dir", "o") {
		outDir = strings.TrimSpace(cf.outputDir)
		if outDir == "" {
			fmt.Fprintln(stderr, "test-cli: output directory must not be empty")
			return ExitUsage
		}
	}
	formats := []string(cf.formats)
	if !flagProvided(fs, "format", "formats") {
		switch cf.profile {
		case "ci":
			formats = []string{"stdout", "json", "junit", "cobertura", "html"}
		case "release":
			formats = []string{"json", "junit", "cobertura", "markdown", "html"}
		default:
			formats = []string{"stdout", "html", "markdown"}
		}
	}
	formats, err = normalizeSelection(formats, append([]string{report.FormatStdout}, report.AllFormats...), "format")
	if err != nil || len(formats) == 0 {
		if err == nil {
			err = fmt.Errorf("at least one report format is required")
		}
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitUsage
	}
	if code := renderAll(&rep, formats, outDir, sourceRoot, stdout, stderr); code != ExitOK {
		return code
	}
	return gateExitCode(&rep, cf.failUnder, cf.failQuality, cf.failDiffCoverage, cf.failOnRegression, cf.failOnFlaky, stderr)
}

func configRelativePaths(paths []string, configPath string) []string {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" && !filepath.IsAbs(path) && configPath != "" {
			path = filepath.Join(filepath.Dir(configPath), path)
		}
		resolved = append(resolved, path)
	}
	return resolved
}

func uniquePaths(paths []string) stringList {
	unique := make(stringList, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		key := filepath.Clean(path)
		if absolute, err := filepath.Abs(path); err == nil {
			key = absolute
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, path)
	}
	return unique
}

func loadHistoricalReports(paths []string) ([]*model.Report, error) {
	reports := make([]*model.Report, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var historical model.Report
		if err := json.Unmarshal(data, &historical); err != nil {
			return nil, fmt.Errorf("%s: invalid report.json: %w", path, err)
		}
		historical.Normalize()
		analysis.Evaluate(&historical, "")
		reports = append(reports, &historical)
	}
	return reports, nil
}

func compareBaseline(current *model.Report, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var baseline model.Report
	if err := json.Unmarshal(data, &baseline); err != nil {
		return fmt.Errorf("invalid report.json: %w", err)
	}
	baseline.Normalize()
	// Re-evaluate using the current scoring rules while preserving the static
	// inventory stored in the baseline. This keeps score deltas comparable when
	// test-cli itself has been upgraded.
	analysis.Evaluate(&baseline, "")
	analysis.Compare(current, &baseline)
	return nil
}

// parseWithTarget parses flags that may appear either before OR after a single
// positional target argument. The stdlib flag package stops at the first
// non-flag token, so `run . --profile ci` would otherwise drop --profile; we
// recover by re-parsing the tokens that follow the positional.
func parseWithTarget(fs *flag.FlagSet, args []string, def string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	target := def
	if rest := fs.Args(); len(rest) > 0 {
		target = rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return "", err
		}
		if extra := fs.Args(); len(extra) > 0 {
			return "", fmt.Errorf("unexpected positional argument %q", extra[0])
		}
	}
	return target, nil
}

func flagProvided(fs *flag.FlagSet, names ...string) bool {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	found := false
	fs.Visit(func(f *flag.Flag) {
		if wanted[f.Name] {
			found = true
		}
	})
	return found
}

func validProfile(profile string) bool {
	return profile == "default" || profile == "ci" || profile == "release"
}

func normalizeSelection(values, allowed []string, label string) ([]string, error) {
	valid := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		valid[value] = true
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s must not be empty", label)
		}
		if !valid[value] {
			return nil, fmt.Errorf("unknown %s %q", label, value)
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, nil
}

func validateCommandOverrides(overrides map[string][][]string) error {
	languages := make([]string, 0, len(overrides))
	for language := range overrides {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	for _, language := range languages {
		if lang.Get(language) == nil {
			return fmt.Errorf("command override uses unknown language %q", language)
		}
		for i, argv := range overrides[language] {
			if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
				return fmt.Errorf("command override %s[%d] has no executable", language, i)
			}
		}
	}
	return nil
}
