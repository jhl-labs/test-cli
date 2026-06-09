package lang

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectByMarkerFile(t *testing.T) {
	cases := map[string]string{
		"go":         "go.mod",
		"python":     "pyproject.toml",
		"typescript": "package.json",
		"rust":       "Cargo.toml",
		"java":       "pom.xml",
	}
	for want, marker := range cases {
		dir := t.TempDir()
		write(t, filepath.Join(dir, marker), "x")
		found := Detect(dir)
		var names []string
		for _, a := range found {
			names = append(names, a.Name)
		}
		if !contains(names, want) {
			t.Errorf("marker %s: detected %v, want %s", marker, names, want)
		}
	}
}

func TestDetectCSharpByGlobNested(t *testing.T) {
	dir := t.TempDir()
	// *.csproj one level deep should still be detected.
	write(t, filepath.Join(dir, "src", "App.csproj"), "<Project/>")
	found := Detect(dir)
	var names []string
	for _, a := range found {
		names = append(names, a.Name)
	}
	if !contains(names, "csharp") {
		t.Errorf("nested .csproj not detected: %v", names)
	}
}

func TestPresentFalseOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if got := Detect(dir); len(got) != 0 {
		t.Errorf("empty dir detected %d languages", len(got))
	}
}

func TestCommandRenderPlaceholders(t *testing.T) {
	c := Command{Args: []string{"go", "test", "-coverprofile={out}/c.out", "{root}/..."}}
	got := c.Render("/tmp/raw", "/proj")
	want := []string{"go", "test", "-coverprofile=/tmp/raw/c.out", "/proj/..."}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGetAndNames(t *testing.T) {
	if Get("go") == nil {
		t.Error("Get(go) is nil")
	}
	if Get("cobol") != nil {
		t.Error("Get(cobol) should be nil")
	}
	if len(Names()) != 6 {
		t.Errorf("Names() = %v, want 6 languages", Names())
	}
}

func TestFindArtifactsPlainAndRecursive(t *testing.T) {
	out := t.TempDir()
	root := t.TempDir()
	// Plain glob in out dir.
	write(t, filepath.Join(out, "junit.xml"), "<x/>")
	// Recursive glob in root.
	write(t, filepath.Join(root, "a", "b", "coverage.cobertura.xml"), "<x/>")

	plain := FindArtifacts([]string{"junit.xml"}, out, root)
	if len(plain) != 1 {
		t.Errorf("plain glob found %v", plain)
	}
	rec := FindArtifacts([]string{"**/coverage.cobertura.xml"}, out, root)
	if len(rec) != 1 {
		t.Fatalf("recursive glob found %v", rec)
	}
	if filepath.Base(rec[0]) != "coverage.cobertura.xml" {
		t.Errorf("recursive match = %s", rec[0])
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
