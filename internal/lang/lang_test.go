package lang

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestPythonCoverageTargetFromCoverageSource(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pyproject.toml"), `
[project]
name = "wrong-name"

[tool.coverage.run]
source = ["k8sage"]
`)

	if got := pythonCoverageTarget(dir); got != "k8sage" {
		t.Fatalf("pythonCoverageTarget = %q, want k8sage", got)
	}
}

func TestPythonCoverageTargetFromProjectName(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pyproject.toml"), `
[project]
name = "my-tool"
`)
	write(t, filepath.Join(dir, "src", "my_tool", "__init__.py"), "")

	if got := pythonCoverageTarget(dir); got != "my_tool" {
		t.Fatalf("pythonCoverageTarget = %q, want my_tool", got)
	}
}

func TestPythonCoverageTargetIgnoresTestsSource(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pyproject.toml"), `
[tool.coverage.run]
source = ["tests"]
`)

	if got := pythonCoverageTarget(dir); got != "." {
		t.Fatalf("pythonCoverageTarget = %q, want fallback .", got)
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

func TestJavaCommandsFollowProjectBuildTool(t *testing.T) {
	a := Get("java")
	maven := t.TempDir()
	write(t, filepath.Join(maven, "pom.xml"), "<project/>")
	mavenArgs := a.CommandsFor(maven)[0].Args
	if mavenArgs[0] != "mvn" || !contains(mavenArgs, "-Dmaven.test.failure.ignore=true") || !contains(mavenArgs, "org.jacoco:jacoco-maven-plugin:prepare-agent") {
		t.Fatalf("Maven command = %v", mavenArgs)
	}

	gradle := t.TempDir()
	write(t, filepath.Join(gradle, "build.gradle"), "plugins { id 'java'; id 'jacoco' }")
	gradleArgs := a.CommandsFor(gradle)[0].Args
	if gradleArgs[0] != "gradle" || !contains(gradleArgs, "--continue") {
		t.Fatalf("Gradle command = %v", gradleArgs)
	}

	wrapper := t.TempDir()
	write(t, filepath.Join(wrapper, "build.gradle.kts"), "plugins { java; jacoco }")
	write(t, filepath.Join(wrapper, "gradlew"), "wrapper")
	if got := a.CommandsFor(wrapper)[0].Args[0]; got != "./gradlew" {
		t.Fatalf("wrapper command = %q", got)
	}
	checks := a.CapabilityProbes(wrapper)
	if len(checks) != 2 || checks[1].Contains != "jacocoTestReport" {
		t.Fatalf("Gradle capability probes = %+v", checks)
	}
}

func TestTypeScriptUsesInstalledDependenciesOnly(t *testing.T) {
	a := Get("typescript")
	args := a.CommandsFor(t.TempDir())[0].Args
	if !reflect.DeepEqual(args[:2], []string{"node", "{root}/node_modules/jest/bin/jest.js"}) {
		t.Fatalf("TypeScript command prefix = %v", args[:2])
	}
	for _, arg := range args {
		if arg == "--yes" {
			t.Fatalf("TypeScript command can download at runtime: %v", args)
		}
	}
	checks := a.CapabilityProbes(t.TempDir())
	if len(checks) != 2 || !strings.Contains(checks[1].Label, "jest-junit") {
		t.Fatalf("TypeScript capability probes = %+v", checks)
	}
}

func TestRustAndCSharpCheckReportConfiguration(t *testing.T) {
	rustChecks := Get("rust").CapabilityProbes(t.TempDir())
	if len(rustChecks) != 3 || len(rustChecks[2].Files) == 0 || rustChecks[2].Contains != "[profile.default.junit]" {
		t.Fatalf("Rust capability probes = %+v", rustChecks)
	}
	csharpChecks := Get("csharp").CapabilityProbes(t.TempDir())
	if len(csharpChecks) != 3 || csharpChecks[1].Contains != "JunitXml.TestLogger" || csharpChecks[2].Contains != "coverlet.collector" {
		t.Fatalf("C# capability probes = %+v", csharpChecks)
	}
	csharpCommand := Get("csharp").CommandsFor(t.TempDir())[0].Args
	if !contains(csharpCommand, "junit;LogFilePath={out}/{assembly}-junit.xml") {
		t.Fatalf("C# command can overwrite multi-project JUnit output: %v", csharpCommand)
	}
}

func TestInferSourceLanguage(t *testing.T) {
	cases := map[string]string{"app.py": "python", "app.tsx": "typescript", "app.go": "go", "app.rs": "rust", "app.cs": "csharp", "App.kt": "java", "README.md": ""}
	for path, want := range cases {
		if got := InferSourceLanguage(path); got != want {
			t.Errorf("InferSourceLanguage(%q) = %q, want %q", path, got, want)
		}
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

func TestFindArtifactsPrefersFreshOutputAndExcludesReports(t *testing.T) {
	out := t.TempDir()
	root := t.TempDir()
	reports := filepath.Join(root, "reports", "test")
	fresh := filepath.Join(out, "junit.xml")
	write(t, fresh, "fresh")
	write(t, filepath.Join(root, "junit.xml"), "stale")

	got := FindArtifacts([]string{"junit.xml"}, out, root, reports)
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("artifacts = %v, want only fresh output %s", got, fresh)
	}

	// With no fresh raw artifact, a recursive root scan must not ingest the
	// tool's own previously rendered Cobertura report.
	emptyOut := t.TempDir()
	write(t, filepath.Join(reports, "coverage.cobertura.xml"), "generated")
	write(t, filepath.Join(root, "reports", "archive", "coverage.cobertura.xml"), "archived")
	write(t, filepath.Join(root, "node_modules", "pkg", "coverage.cobertura.xml"), "dependency")
	if got := FindArtifacts([]string{"**/coverage.cobertura.xml"}, emptyOut, root, reports); len(got) != 0 {
		t.Fatalf("rendered reports were re-ingested: %v", got)
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
