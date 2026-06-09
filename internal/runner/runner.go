// Package runner orchestrates a full test+coverage run: it detects languages,
// invokes each language's test command, locates the native artifacts they
// produce, and ingests them into a single normalized model.Report. It is the
// glue between lang (what to run), ingest (how to parse), and report (how to
// render).
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jhl-labs/test-cli/internal/config"
	"github.com/jhl-labs/test-cli/internal/ingest"
	"github.com/jhl-labs/test-cli/internal/lang"
	"github.com/jhl-labs/test-cli/internal/model"
)

// Options controls a run.
type Options struct {
	Root        string        // project root (working directory for commands)
	OutDir      string        // report output directory
	ToolVersion string        // embedded into the report
	Languages   []string      // restrict to these languages (empty = all detected)
	Config      config.Config // project configuration (command overrides etc.)
	NoRun       bool          // skip command execution; only ingest existing artifacts
	SkipDetect  bool          // skip language detection/scanning (pure ingest mode)
	Timeout     time.Duration // per-command timeout (0 = none)
	Log         io.Writer     // progress log (may be nil)
	// IngestTests / IngestCoverage ingest explicit artifact files instead of (or
	// in addition to) discovered ones. Used by the `ingest` command.
	IngestTests    []string
	IngestCoverage []string
}

// Run executes the pipeline and returns a finalized, normalized report.
func Run(ctx context.Context, opts Options) (*model.Report, error) {
	logf := func(format string, a ...any) {
		if opts.Log != nil {
			fmt.Fprintf(opts.Log, format+"\n", a...)
		}
	}

	rawRoot := filepath.Join(opts.OutDir, "raw")
	report := &model.Report{
		Schema:      model.SchemaID,
		ToolVersion: opts.ToolVersion,
		GeneratedAt: time.Now(),
		Root:        opts.Root,
	}

	langSet := map[string]bool{}

	// Explicit ingest mode: parse provided artifacts and finish.
	if len(opts.IngestTests) > 0 || len(opts.IngestCoverage) > 0 {
		ingestExplicit(report, opts, logf, langSet)
	}

	var adapters []*lang.Adapter
	if !opts.SkipDetect {
		adapters = selectAdapters(opts)
	}
	for _, a := range adapters {
		rawDir := filepath.Join(rawRoot, a.Name)
		if err := os.MkdirAll(rawDir, 0o755); err != nil {
			return nil, err
		}

		if !opts.NoRun {
			runCommands(ctx, a, opts, rawDir, logf)
		}

		testFiles := lang.FindArtifacts(a.TestGlobs, rawDir, opts.Root)
		covFiles := lang.FindArtifacts(a.CovGlobs, rawDir, opts.Root)
		if len(testFiles) == 0 && len(covFiles) == 0 {
			logf("  %s: no artifacts found", a.Name)
			continue
		}

		for _, tf := range testFiles {
			suites, format, err := ingest.LoadTests(tf, a.Name)
			if err != nil {
				report.Messages = append(report.Messages, fmt.Sprintf("%s: skipped test report %s (%v)", a.Name, filepath.Base(tf), err))
				continue
			}
			logf("  %s: parsed %d suite(s) from %s [%s]", a.Name, len(suites), filepath.Base(tf), format)
			report.Test.Suites = append(report.Test.Suites, suites...)
			langSet[a.Name] = true
		}
		for _, cf := range covFiles {
			files, format, err := ingest.LoadCoverage(cf, a.Name)
			if err != nil {
				report.Messages = append(report.Messages, fmt.Sprintf("%s: skipped coverage %s (%v)", a.Name, filepath.Base(cf), err))
				continue
			}
			logf("  %s: parsed %d file(s) from %s [%s]", a.Name, len(files), filepath.Base(cf), format)
			report.Coverage.Files = append(report.Coverage.Files, files...)
			langSet[a.Name] = true
		}
	}

	for l := range langSet {
		report.Languages = append(report.Languages, l)
	}
	report.Normalize()
	return report, nil
}

func selectAdapters(opts Options) []*lang.Adapter {
	filter := opts.Languages
	if len(filter) == 0 {
		filter = opts.Config.Languages
	}
	if len(filter) > 0 {
		var out []*lang.Adapter
		for _, name := range filter {
			if a := lang.Get(name); a != nil {
				out = append(out, a)
			}
		}
		return out
	}
	return lang.Detect(opts.Root)
}

func runCommands(ctx context.Context, a *lang.Adapter, opts Options, rawDir string, logf func(string, ...any)) {
	commands := a.Commands
	// Apply per-language command override from config.
	if override, ok := opts.Config.Commands[a.Name]; ok && len(override) > 0 {
		commands = nil
		for _, argv := range override {
			commands = append(commands, lang.Command{Args: argv})
		}
	}

	for _, cmd := range commands {
		args := cmd.Render(rawDir, opts.Root)
		if len(args) == 0 {
			continue
		}
		logf("  %s: $ %s", a.Name, shellJoin(args))

		cctx := ctx
		var cancel context.CancelFunc
		if opts.Timeout > 0 {
			cctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		}
		c := exec.CommandContext(cctx, args[0], args[1:]...)
		c.Dir = opts.Root
		c.Env = os.Environ()
		// jest-junit honors JEST_JUNIT_OUTPUT_DIR/FILE.
		c.Env = append(c.Env, "JEST_JUNIT_OUTPUT_DIR="+rawDir, "JEST_JUNIT_OUTPUT_FILE="+filepath.Join(rawDir, "junit.xml"))

		if cmd.Stdout != "" {
			f, err := os.Create(filepath.Join(rawDir, cmd.Stdout))
			if err == nil {
				c.Stdout = f
				c.Stderr = io.Discard
				err = c.Run()
				f.Close()
			}
			if err != nil {
				logf("  %s: command exited with error (%v) — continuing to ingest artifacts", a.Name, err)
			}
		} else {
			out, err := c.CombinedOutput()
			if len(out) > 0 && opts.Log != nil {
				// Echo a trimmed tail so CI logs show test framework output.
				opts.Log.Write(trimTail(out, 4000))
			}
			if err != nil {
				logf("  %s: command exited with error (%v) — continuing to ingest artifacts", a.Name, err)
			}
		}
		if cancel != nil {
			cancel()
		}
	}
}

func ingestExplicit(report *model.Report, opts Options, logf func(string, ...any), langSet map[string]bool) {
	for _, tf := range opts.IngestTests {
		suites, format, err := ingest.LoadTests(tf, "")
		if err != nil {
			report.Messages = append(report.Messages, fmt.Sprintf("ingest %s: %v", tf, err))
			continue
		}
		logf("ingest: %d suite(s) from %s [%s]", len(suites), filepath.Base(tf), format)
		report.Test.Suites = append(report.Test.Suites, suites...)
	}
	for _, cf := range opts.IngestCoverage {
		files, format, err := ingest.LoadCoverage(cf, "")
		if err != nil {
			report.Messages = append(report.Messages, fmt.Sprintf("ingest %s: %v", cf, err))
			continue
		}
		logf("ingest: %d file(s) from %s [%s]", len(files), filepath.Base(cf), format)
		report.Coverage.Files = append(report.Coverage.Files, files...)
	}
}
