package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jhl-labs/test-cli/internal/model"
)

func TestCountTestSignalsGo(t *testing.T) {
	data := []byte(`package p

import "testing"

func TestA(t *testing.T) {
	if got := 1; got != 1 {
		t.Errorf("got %d", got)
	}
}

func TestB(t *testing.T) {
	t.Fatal("boom")
}

func helper() {}
`)
	asserts, funcs := countTestSignals(data, "go")
	if funcs != 2 {
		t.Errorf("funcs = %d, want 2", funcs)
	}
	if asserts != 2 {
		t.Errorf("asserts = %d, want 2", asserts)
	}
}

func TestBuildTestMappingsByNameAndImport(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("pkg/util.go", "package pkg\n\nfunc Util() int { return 1 }\n")
	mustWrite("pkg/util_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestUtil(t *testing.T) {\n\tif Util() != 1 {\n\t\tt.Error(\"bad\")\n\t}\n}\n")
	mustWrite("other/orphan.go", "package other\n\nfunc Orphan() int { return 2 }\n")

	_, stats, ok := scanProject(root)
	if !ok {
		t.Fatal("scanProject failed")
	}
	static, _, _ := scanProject(root)
	if len(static.TestMappings) != 2 {
		t.Fatalf("mappings = %+v", static.TestMappings)
	}
	// Unmapped sources sort first.
	first := static.TestMappings[0]
	if first.SourcePath != "other/orphan.go" || len(first.TestPaths) != 0 {
		t.Errorf("expected orphan first: %+v", first)
	}
	second := static.TestMappings[1]
	if second.SourcePath != "pkg/util.go" || len(second.TestPaths) != 1 || second.TestPaths[0] != "pkg/util_test.go" {
		t.Errorf("util mapping wrong: %+v", second)
	}
	if second.TestFuncCount != 1 || second.AssertionCount < 1 {
		t.Errorf("signals wrong: %+v", second)
	}
	if !second.Heuristic {
		t.Error("Heuristic must always be true")
	}
	if static.UnmappedSources != 1 {
		t.Errorf("UnmappedSources = %d, want 1", static.UnmappedSources)
	}
	_ = stats
}

func TestBuildTestMappingsRustInline(t *testing.T) {
	root := t.TempDir()
	src := "pub fn add(a: i32, b: i32) -> i32 { a + b }\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn adds() {\n        assert_eq!(add(1, 2), 3);\n    }\n}\n"
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/lib.rs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	static, _, ok := scanProject(root)
	if !ok || len(static.TestMappings) != 1 {
		t.Fatalf("mappings = %+v", static.TestMappings)
	}
	m := static.TestMappings[0]
	if len(m.TestPaths) != 1 || m.TestPaths[0] != "src/lib.rs" || m.TestFuncCount != 1 || m.AssertionCount != 1 {
		t.Errorf("rust inline mapping wrong: %+v", m)
	}
	if static.UnmappedSources != 0 {
		t.Errorf("UnmappedSources = %d, want 0", static.UnmappedSources)
	}
}

func TestMappingFindings(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "orphan.go"), []byte("package p\n\nfunc O() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &model.Report{}
	Evaluate(r, root)
	found := map[string]bool{}
	for _, f := range r.Quality.Findings {
		found[f.ID] = true
	}
	if !found["STATIC-007"] {
		t.Errorf("expected STATIC-007, findings: %+v", r.Quality.Findings)
	}
}

func TestGoPackageLevelMapping(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("changes.go", "package p\n\nfunc C() int { return 1 }\n")
	write("analysis_test.go", "package p\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {\n\tif C() != 1 {\n\t\tt.Error(\"bad\")\n\t}\n}\n")
	static, _, ok := scanProject(root)
	if !ok || len(static.TestMappings) != 1 {
		t.Fatalf("mappings = %+v", static.TestMappings)
	}
	m := static.TestMappings[0]
	if len(m.TestPaths) != 1 || m.TestPaths[0] != "analysis_test.go" {
		t.Errorf("same-package go test should map differently named source: %+v", m)
	}
	if static.UnmappedSources != 0 {
		t.Errorf("UnmappedSources = %d, want 0", static.UnmappedSources)
	}
}
