package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jhl-labs/test-cli/internal/config"
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
	outputDir string
	formats   stringList
	langs     stringList
	profile   string
	failUnder float64
	noRun     bool
	quiet     bool
	timeout   time.Duration
	tests     stringList
	coverage  stringList
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
	fs.BoolVar(&cf.noRun, "no-run", false, "do not execute tests; ingest existing artifacts")
	fs.BoolVar(&cf.quiet, "quiet", false, "suppress progress logging")
	fs.DurationVar(&cf.timeout, "timeout", 20*time.Minute, "per-command timeout")
	fs.Var(&cf.tests, "tests", "explicit test artifact to ingest (repeatable)")
	fs.Var(&cf.coverage, "coverage", "explicit coverage artifact to ingest (repeatable)")
}

func runRun(args []string, stdout, stderr io.Writer) int {
	return execute(args, stdout, stderr, false)
}

func runIngest(args []string, stdout, stderr io.Writer) int {
	return execute(args, stdout, stderr, true)
}

// execute is shared by `run` and `ingest`. ingest forces --no-run.
func execute(args []string, stdout, stderr io.Writer, ingestMode bool) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf commonFlags
	cf.bind(fs)
	target, err := parseWithTarget(fs, args, ".")
	if err != nil {
		return ExitUsage
	}
	root, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitUsage
	}

	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	applyProfile(&cfg, &cf)

	outDir := firstNonEmpty(cf.outputDir, cfg.OutputDir)
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(root, outDir)
	}
	formats := []string(cf.formats)
	if len(formats) == 0 {
		formats = cfg.Formats
	}

	var logw io.Writer = stderr
	if cf.quiet {
		logw = io.Discard
	}

	opts := runner.Options{
		Root:           root,
		OutDir:         outDir,
		ToolVersion:    version.Long(),
		Languages:      []string(cf.langs),
		Config:         cfg,
		NoRun:          ingestMode || cf.noRun,
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

	if rep.Test.Summary.Total == 0 && rep.Coverage.Summary.Lines.Total == 0 {
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

	return gateExitCode(rep, cf.failUnder, stderr)
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
func gateExitCode(rep *model.Report, failUnder float64, stderr io.Writer) int {
	if !rep.Test.Summary.Passing() {
		return ExitTestFailure
	}
	if failUnder > 0 && rep.Coverage.Summary.Lines.Total > 0 && rep.Coverage.Summary.Lines.Pct < failUnder {
		fmt.Fprintf(stderr, "test-cli: line coverage %.1f%% is below threshold %.1f%%\n",
			rep.Coverage.Summary.Lines.Pct, failUnder)
		return ExitTestFailure
	}
	return ExitOK
}

// applyProfile adjusts defaults based on the chosen preset.
func applyProfile(cfg *config.Config, cf *commonFlags) {
	switch cf.profile {
	case "ci":
		if len(cfg.Formats) == 0 {
			cfg.Formats = []string{"stdout", "json", "junit", "cobertura", "html"}
		}
	case "release":
		if len(cfg.Formats) == 0 {
			cfg.Formats = []string{"json", "junit", "cobertura", "markdown", "html"}
		}
	}
}

// runReport re-renders from an existing report.json without re-running tests.
func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "path to an existing report.json")
	var cf commonFlags
	cf.bind(fs)
	pos, err := parseWithTarget(fs, args, "")
	if err != nil {
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
	rep.Normalize()

	outDir := firstNonEmpty(cf.outputDir, filepath.Dir(source))
	formats := []string(cf.formats)
	if len(formats) == 0 {
		formats = []string{"stdout", "html", "markdown"}
	}
	if code := renderAll(&rep, formats, outDir, rep.Root, stdout, stderr); code != ExitOK {
		return code
	}
	return gateExitCode(&rep, cf.failUnder, stderr)
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
	}
	return target, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
