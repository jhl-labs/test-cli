package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jhl-labs/test-cli/internal/config"
	"github.com/jhl-labs/test-cli/internal/lang"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunDiscoversAndIngestsArtifacts exercises the main Run loop without any
// real toolchain: it pre-seeds the per-language raw directory with native
// artifacts, then runs with NoRun so the runner only discovers + ingests.
func TestRunDiscoversAndIngestsArtifacts(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "reports")
	rawGo := filepath.Join(out, "raw", "go")

	writeFile(t, filepath.Join(rawGo, "gotest.json"),
		`{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0.01}
{"Action":"run","Package":"p","Test":"TestB"}
{"Action":"fail","Package":"p","Test":"TestB","Elapsed":0.02}`)
	writeFile(t, filepath.Join(rawGo, "coverage.out"),
		"mode: set\np/a.go:1.1,3.1 2 1\np/a.go:5.1,6.1 1 0\n")

	rep, err := Run(context.Background(), Options{
		Root:        root,
		OutDir:      out,
		ToolVersion: "test",
		Languages:   []string{"go"},
		Config:      config.Default(),
		NoRun:       true,
		Log:         &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Test.Summary.Total != 2 || rep.Test.Summary.Failed != 1 {
		t.Errorf("test summary = %+v, want total 2 failed 1", rep.Test.Summary)
	}
	if rep.Coverage.Summary.Lines.Total == 0 {
		t.Error("coverage not ingested")
	}
	if len(rep.Languages) != 1 || rep.Languages[0] != "go" {
		t.Errorf("languages = %v", rep.Languages)
	}
}

func TestRunExplicitIngestWithSkipDetect(t *testing.T) {
	dir := t.TempDir()
	junit := filepath.Join(dir, "junit.xml")
	writeFile(t, junit, `<testsuites><testsuite name="s"><testcase name="ok" time="0.1"/></testsuite></testsuites>`)

	rep, err := Run(context.Background(), Options{
		Root:        dir,
		OutDir:      filepath.Join(dir, "out"),
		ToolVersion: "test",
		Config:      config.Default(),
		NoRun:       true,
		SkipDetect:  true,
		IngestTests: []string{junit, junit}, // exact duplicates must not double-count tests
		Log:         &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Test.Summary.Total != 1 || !rep.Test.Summary.Passing() {
		t.Errorf("summary = %+v", rep.Test.Summary)
	}
}

func TestExplicitGoArtifactsRetainLanguage(t *testing.T) {
	dir := t.TempDir()
	tests := filepath.Join(dir, "gotest.json")
	coverage := filepath.Join(dir, "coverage.out")
	writeFile(t, tests, `{"Action":"run","Package":"example/app","Test":"TestA"}
{"Action":"pass","Package":"example/app","Test":"TestA","Elapsed":0.01}`)
	writeFile(t, coverage, "mode: set\nexample/app.go:1.1,2.1 1 1\n")

	rep, err := Run(context.Background(), Options{
		Root:           dir,
		OutDir:         filepath.Join(dir, "out"),
		ToolVersion:    "test",
		Config:         config.Default(),
		NoRun:          true,
		SkipDetect:     true,
		IngestTests:    []string{tests},
		IngestCoverage: []string{coverage},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Languages) != 1 || rep.Languages[0] != "go" {
		t.Fatalf("languages = %v", rep.Languages)
	}
	if rep.Test.Suites[0].Language != "go" || rep.Coverage.Files[0].Language != "go" {
		t.Fatalf("language attribution: suite=%q coverage=%q", rep.Test.Suites[0].Language, rep.Coverage.Files[0].Language)
	}
}

func TestExplicitLanguageHintLabelsGenericArtifacts(t *testing.T) {
	dir := t.TempDir()
	junit := filepath.Join(dir, "junit.xml")
	coverage := filepath.Join(dir, "coverage.xml")
	writeFile(t, junit, `<testsuite name="s"><testcase name="ok"/></testsuite>`)
	writeFile(t, coverage, `<coverage><packages><package><classes><class filename="module"><lines><line number="1" hits="1"/></lines></class></classes></package></packages></coverage>`)
	rep, err := Run(context.Background(), Options{
		Root: dir, OutDir: filepath.Join(dir, "out"), ToolVersion: "test",
		Languages: []string{"python"}, Config: config.Default(), NoRun: true, SkipDetect: true,
		IngestTests: []string{junit}, IngestCoverage: []string{coverage},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Test.Suites[0].Language != "python" || rep.Coverage.Files[0].Language != "python" {
		t.Fatalf("explicit hint lost: suite=%q coverage=%q", rep.Test.Suites[0].Language, rep.Coverage.Files[0].Language)
	}
}

func TestRunReportsBadArtifactsAsMessages(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "reports")
	// A file matching the go test glob but containing garbage.
	writeFile(t, filepath.Join(out, "raw", "go", "gotest.json"), "not json and not xml")

	rep, err := Run(context.Background(), Options{
		Root:      root,
		OutDir:    out,
		Languages: []string{"go"},
		Config:    config.Default(),
		NoRun:     true,
		Log:       &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Messages) == 0 {
		t.Error("expected a diagnostic message for unparseable artifact")
	}
}

func TestRunRemovesStaleRawArtifactsBeforeExecuting(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "reports")
	stale := filepath.Join(out, "raw", "go", "gotest.json")
	writeFile(t, stale, `{"Action":"run","Package":"stale","Test":"TestOld"}
{"Action":"pass","Package":"stale","Test":"TestOld","Elapsed":0.01}`)
	cfg := config.Default()
	cfg.Commands = map[string][][]string{"go": {{"go", "test-cli-definitely-not-a-go-command"}}}

	rep, err := Run(context.Background(), Options{
		Root: root, OutDir: out, ToolVersion: "test", Languages: []string{"go"},
		Config: cfg, Log: &bytes.Buffer{},
	})
	if err == nil || rep != nil {
		t.Fatalf("run = report %+v, err %v; want missing-current-results failure", rep, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale raw artifact still exists: %v", err)
	}
}

func TestCurrentArtifactsRejectsUnchangedRootFallback(t *testing.T) {
	root := t.TempDir()
	raw := filepath.Join(root, "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, "target", "junit.xml")
	writeFile(t, artifact, "old")
	previous := snapshotArtifacts([]string{artifact})
	if got := currentArtifacts([]string{artifact}, raw, previous); len(got) != 0 {
		t.Fatalf("unchanged fallback artifact treated as current: %v", got)
	}
	writeFile(t, artifact, "new")
	if got := currentArtifacts([]string{artifact}, raw, previous); len(got) != 1 || got[0] != artifact {
		t.Fatalf("updated fallback artifact was rejected: %v", got)
	}
	rawArtifact := filepath.Join(raw, "junit.xml")
	writeFile(t, rawArtifact, "fresh")
	if got := currentArtifacts([]string{rawArtifact}, raw, nil); len(got) != 1 {
		t.Fatalf("fresh raw artifact was rejected: %v", got)
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"go", "test", "a b", `q"x`})
	want := `go test "a b" "q\"x"`
	if got != want {
		t.Errorf("shellJoin = %q, want %q", got, want)
	}
}

func TestTrimTail(t *testing.T) {
	if got := trimTail([]byte("hello"), 10); string(got) != "hello" {
		t.Errorf("no-trim case = %q", got)
	}
	out := trimTail([]byte("0123456789abc"), 5)
	if !bytes.HasSuffix(out, []byte("9abc")) || !bytes.HasPrefix(out, []byte("…")) {
		t.Errorf("trimmed = %q", out)
	}
	utf8Tail := trimTail([]byte("앞부분가나다"), 7)
	if !bytes.Equal(bytes.ToValidUTF8(utf8Tail, nil), utf8Tail) || !bytes.HasSuffix(utf8Tail, []byte("나다")) {
		t.Errorf("UTF-8 tail = %q", utf8Tail)
	}
}

func TestSelectAdaptersDeduplicatesLanguages(t *testing.T) {
	adapters := selectAdapters(Options{Languages: []string{"go", "go"}, Config: config.Default()})
	if len(adapters) != 1 || adapters[0].Name != "go" {
		t.Fatalf("adapters = %v", adapters)
	}
}

func TestPrepareAdapterRuntimeWritesNextestJUnitConfig(t *testing.T) {
	raw := t.TempDir()
	if err := prepareAdapterRuntime(lang.Get("rust"), raw); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(raw, "nextest.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "[profile.default.junit]") || !strings.Contains(text, filepath.Join(raw, "junit.xml")) {
		t.Fatalf("nextest config = %q", text)
	}
}

func TestRunSurfacesCommandFailureWhenArtifactsLookPassing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell script")
	}
	root := t.TempDir()
	out := filepath.Join(root, "reports")
	script := filepath.Join(root, "failing-test-command")
	writeFile(t, script, "#!/bin/sh\nprintf '%s\\n' '<testsuite name=\"s\"><testcase name=\"ok\"/></testsuite>' > \"$1/junit.xml\"\nexit 7\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Commands = map[string][][]string{"python": {{script, "{out}"}}}
	rep, err := Run(context.Background(), Options{
		Root: root, OutDir: out, Languages: []string{"python"}, Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Test.Summary.Errors != 1 || rep.Test.Summary.Passing() {
		t.Fatalf("command failure was hidden by passing artifact: %+v", rep.Test.Summary)
	}
}
