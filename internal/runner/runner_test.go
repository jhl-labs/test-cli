package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jhl-labs/test-cli/internal/config"
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
		IngestTests: []string{junit},
		Log:         &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Test.Summary.Total != 1 || !rep.Test.Summary.Passing() {
		t.Errorf("summary = %+v", rep.Test.Summary)
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
}
