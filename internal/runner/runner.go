// Package runner orchestrates a full test+coverage run: it detects languages,
// invokes each language's test command, locates the native artifacts they
// produce, and ingests them into a single normalized model.Report. It is the
// glue between lang (what to run), ingest (how to parse), and report (how to
// render).
package runner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jhl-labs/test-cli/internal/analysis"
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
	if !opts.NoRun {
		// raw is entirely tool-owned. Clearing it once also removes artifacts for
		// languages selected in a previous run but not in this one.
		if err := os.RemoveAll(rawRoot); err != nil {
			return nil, fmt.Errorf("clear generated raw artifacts: %w", err)
		}
	}

	var adapters []*lang.Adapter
	if !opts.SkipDetect {
		adapters = selectAdapters(opts)
	}
	var languagesWithoutTests []string
	for _, a := range adapters {
		rawDir := filepath.Join(rawRoot, a.Name)
		if err := os.MkdirAll(rawDir, 0o755); err != nil {
			return nil, err
		}
		if err := prepareAdapterRuntime(a, rawDir); err != nil {
			return nil, err
		}
		excluded := reportOutputExclusion(opts.Root, opts.OutDir)
		var previousTests, previousCoverage map[string]artifactFingerprint
		if !opts.NoRun {
			// Some ecosystems (notably Maven, Gradle, and nextest) emit into the
			// project build tree. Snapshot those fallback artifacts so a failed
			// command cannot make an old report look like current evidence.
			emptyRaw := filepath.Join(rawDir, ".test-cli-empty")
			previousTests = snapshotArtifacts(lang.FindArtifacts(a.TestGlobs, emptyRaw, opts.Root, excluded))
			previousCoverage = snapshotArtifacts(lang.FindArtifacts(a.CovGlobs, emptyRaw, opts.Root, excluded))
		}

		var commandFailures []string
		if !opts.NoRun {
			commandFailures = runCommands(ctx, a, opts, rawDir, logf)
		}

		testFiles := lang.FindArtifacts(a.TestGlobs, rawDir, opts.Root, excluded)
		covFiles := lang.FindArtifacts(a.CovGlobs, rawDir, opts.Root, excluded)
		if !opts.NoRun {
			testFiles = currentArtifacts(testFiles, rawDir, previousTests)
			covFiles = currentArtifacts(covFiles, rawDir, previousCoverage)
		}
		if len(testFiles) == 0 && len(covFiles) == 0 {
			logf("  %s: no artifacts found", a.Name)
			if !opts.NoRun {
				languagesWithoutTests = append(languagesWithoutTests, a.Name)
			}
			continue
		}

		casesBefore := testCaseCount(report.Test.Suites)
		suitesBefore := len(report.Test.Suites)
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
		if len(commandFailures) > 0 && testCaseCount(report.Test.Suites) > casesBefore && !hasTestFailure(report.Test.Suites[suitesBefore:]) {
			detail := strings.Join(commandFailures, "; ")
			report.Test.Suites = append(report.Test.Suites, model.TestSuite{
				Name: a.Name + " command", Language: a.Name,
				Cases: []model.TestCase{{
					Name: "[command]", Status: model.StatusError,
					Message: "test command exited unsuccessfully", Detail: detail,
				}},
			})
			report.Messages = append(report.Messages, fmt.Sprintf("%s: test command failed despite producing non-failing test artifacts (%s)", a.Name, detail))
		}
		if !opts.NoRun && testCaseCount(report.Test.Suites) == casesBefore {
			languagesWithoutTests = append(languagesWithoutTests, a.Name)
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
	if len(languagesWithoutTests) > 0 {
		return nil, fmt.Errorf("no current test cases were produced for: %s", strings.Join(languagesWithoutTests, ", "))
	}

	for l := range langSet {
		report.Languages = append(report.Languages, l)
	}
	report.Normalize()
	analysis.Evaluate(report, opts.Root)
	return report, nil
}

func prepareAdapterRuntime(adapter *lang.Adapter, rawDir string) error {
	if adapter == nil || adapter.Name != "rust" {
		return nil
	}
	junitPath := filepath.Join(rawDir, "junit.xml")
	configPath := filepath.Join(rawDir, "nextest.toml")
	content := fmt.Sprintf("[profile.default.junit]\npath = %q\n", junitPath)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write Rust nextest tool config: %w", err)
	}
	return nil
}

func reportOutputExclusion(root, outDir string) string {
	r, rerr := filepath.Abs(root)
	o, oerr := filepath.Abs(outDir)
	if rerr != nil || oerr != nil || r == o {
		return ""
	}
	return o
}

func selectAdapters(opts Options) []*lang.Adapter {
	filter := opts.Languages
	if len(filter) == 0 {
		filter = opts.Config.Languages
	}
	if len(filter) > 0 {
		var out []*lang.Adapter
		seen := map[string]bool{}
		for _, name := range filter {
			if a := lang.Get(name); a != nil && !seen[a.Name] {
				seen[a.Name] = true
				out = append(out, a)
			}
		}
		return out
	}
	return lang.Detect(opts.Root)
}

func runCommands(ctx context.Context, a *lang.Adapter, opts Options, rawDir string, logf func(string, ...any)) []string {
	commands := a.CommandsFor(opts.Root)
	// Apply per-language command override from config.
	if override, ok := opts.Config.Commands[a.Name]; ok && len(override) > 0 {
		commands = nil
		for _, argv := range override {
			commands = append(commands, lang.Command{Args: argv})
		}
	}

	var failures []string
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
				failures = append(failures, err.Error())
			}
		} else {
			out, err := c.CombinedOutput()
			if len(out) > 0 && opts.Log != nil {
				// Echo a trimmed tail so CI logs show test framework output.
				opts.Log.Write(trimTail(out, 4000))
			}
			if err != nil {
				logf("  %s: command exited with error (%v) — continuing to ingest artifacts", a.Name, err)
				detail := strings.TrimSpace(string(trimTail(out, 500)))
				if detail == "" {
					detail = err.Error()
				} else {
					detail = err.Error() + ": " + detail
				}
				failures = append(failures, detail)
			}
		}
		if cancel != nil {
			cancel()
		}
	}
	return failures
}

func ingestExplicit(report *model.Report, opts Options, logf func(string, ...any), langSet map[string]bool) {
	explicitHint := ""
	if len(opts.Languages) == 1 {
		explicitHint = opts.Languages[0]
	}
	for _, tf := range uniquePaths(opts.IngestTests) {
		hint := firstNonEmptyLanguage(explicitHint, artifactPathLanguage(tf))
		suites, format, err := ingest.LoadTests(tf, hint)
		if err != nil {
			report.Messages = append(report.Messages, fmt.Sprintf("ingest %s: %v", tf, err))
			continue
		}
		if format == ingest.FormatGoJSON {
			hint = "go"
		}
		for i := range suites {
			if suites[i].Language == "" {
				suites[i].Language = firstNonEmptyLanguage(hint, lang.InferSourceLanguage(suites[i].File))
			}
		}
		logf("ingest: %d suite(s) from %s [%s]", len(suites), filepath.Base(tf), format)
		report.Test.Suites = append(report.Test.Suites, suites...)
	}
	for _, cf := range uniquePaths(opts.IngestCoverage) {
		hint := firstNonEmptyLanguage(explicitHint, artifactPathLanguage(cf))
		files, format, err := ingest.LoadCoverage(cf, hint)
		if err != nil {
			report.Messages = append(report.Messages, fmt.Sprintf("ingest %s: %v", cf, err))
			continue
		}
		switch format {
		case ingest.FormatGoCover:
			hint = "go"
		case ingest.FormatJaCoCo:
			hint = "java"
		}
		for i := range files {
			if files[i].Language == "" {
				files[i].Language = firstNonEmptyLanguage(hint, lang.InferSourceLanguage(files[i].Path))
			}
		}
		logf("ingest: %d file(s) from %s [%s]", len(files), filepath.Base(cf), format)
		report.Coverage.Files = append(report.Coverage.Files, files...)
	}
	reconcileExplicitLanguages(report, langSet)
}

func reconcileExplicitLanguages(report *model.Report, langSet map[string]bool) {
	known := map[string]bool{}
	for _, suite := range report.Test.Suites {
		if suite.Language != "" {
			known[suite.Language] = true
		}
	}
	for _, file := range report.Coverage.Files {
		if file.Language != "" {
			known[file.Language] = true
		}
	}
	if len(known) == 1 {
		var only string
		for language := range known {
			only = language
		}
		for i := range report.Test.Suites {
			if report.Test.Suites[i].Language == "" {
				report.Test.Suites[i].Language = only
			}
		}
		for i := range report.Coverage.Files {
			if report.Coverage.Files[i].Language == "" {
				report.Coverage.Files[i].Language = only
			}
		}
	}
	for language := range known {
		langSet[language] = true
	}
}

func artifactPathLanguage(path string) string {
	aliases := map[string]string{
		"python": "python", "py": "python",
		"typescript": "typescript", "javascript": "typescript", "js": "typescript",
		"go": "go", "golang": "go",
		"rust":   "rust",
		"csharp": "csharp", "dotnet": "csharp",
		"java": "java", "kotlin": "java",
	}
	normalized := filepath.ToSlash(filepath.Clean(path))
	for _, part := range strings.Split(normalized, "/") {
		if language := aliases[strings.ToLower(part)]; language != "" {
			return language
		}
	}
	return ""
}

func firstNonEmptyLanguage(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func testCaseCount(suites []model.TestSuite) int {
	total := 0
	for _, suite := range suites {
		total += len(suite.Cases)
	}
	return total
}

func hasTestFailure(suites []model.TestSuite) bool {
	for _, suite := range suites {
		for _, test := range suite.Cases {
			if test.Status == model.StatusFailed || test.Status == model.StatusError {
				return true
			}
		}
	}
	return false
}

func uniquePaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		key := path
		if abs, err := filepath.Abs(path); err == nil {
			key = filepath.Clean(abs)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
	}
	return out
}

type artifactFingerprint struct {
	Size       int64
	ModifiedNs int64
	SHA256     [sha256.Size]byte
}

func snapshotArtifacts(paths []string) map[string]artifactFingerprint {
	out := make(map[string]artifactFingerprint, len(paths))
	for _, path := range paths {
		fingerprint, err := fingerprintArtifact(path)
		if err != nil {
			continue
		}
		out[canonicalPath(path)] = fingerprint
	}
	return out
}

func currentArtifacts(paths []string, rawDir string, previous map[string]artifactFingerprint) []string {
	current := make([]string, 0, len(paths))
	for _, path := range paths {
		if pathWithin(path, rawDir) {
			current = append(current, path)
			continue
		}
		before, existed := previous[canonicalPath(path)]
		after, err := fingerprintArtifact(path)
		if err == nil && (!existed || after != before) {
			current = append(current, path)
		}
	}
	return current
}

func fingerprintArtifact(path string) (artifactFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return artifactFingerprint{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return artifactFingerprint{}, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return artifactFingerprint{}, err
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return artifactFingerprint{Size: info.Size(), ModifiedNs: info.ModTime().UnixNano(), SHA256: sum}, nil
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func pathWithin(path, dir string) bool {
	rel, err := filepath.Rel(canonicalPath(dir), canonicalPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
