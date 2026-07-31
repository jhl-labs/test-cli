package analysis

import (
	"bufio"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

const maxTestMappings = 500

// testFileStat is the per-test-file evidence collected during the scanProject
// walk that feeds test-source mapping and assertion-density diagnostics.
type testFileStat struct {
	Path       string
	Language   string
	Assertions int
	TestFuncs  int
	imports    string // lowercased import-ish lines for fallback matching
}

// countTestSignals counts assertion calls and test functions with portable,
// per-language lexical patterns. Like the smell rules, it is a ranking signal
// rather than an exact framework-aware count.
func countTestSignals(data []byte, language string) (asserts, funcs int) {
	assertPatterns := map[string][]string{
		"go":         {"t.Error", "t.Fatal", "t.Errorf", "t.Fatalf", "require.", "assert."},
		"python":     {"assert ", "self.assert", "pytest.raises"},
		"typescript": {"expect(", "assert.", ".toBe", ".toEqual"},
		"rust":       {"assert!", "assert_eq!", "assert_ne!", "#[should_panic]"},
		"csharp":     {"Assert.", "Should()", ".Should("},
		"java":       {"assert", "Assert.", "assertThat", "verify("},
	}
	funcPatterns := map[string][]string{
		"go":         {"func Test"},
		"python":     {"def test_"},
		"typescript": {"it(", "it (", "test(", "test ("},
		"rust":       {"#[test]"},
		"csharp":     {"[Fact]", "[Test]", "[TestMethod]"},
		"java":       {"@Test"},
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || (language == "python" && strings.HasPrefix(line, "#")) {
			continue
		}
		for _, p := range assertPatterns[language] {
			if strings.Contains(line, p) {
				asserts++
				break
			}
		}
		for _, p := range funcPatterns[language] {
			if strings.HasPrefix(line, p) || (p[0] != 'f' && strings.Contains(line, p)) {
				funcs++
				break
			}
		}
	}
	return asserts, funcs
}

// importRefs joins the lowercased import-like lines of a test file so a source
// file stem can be matched as a fallback when name conventions fail.
func importRefs(data []byte) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	lines := 0
	for sc.Scan() && lines < 200 {
		line := strings.TrimSpace(strings.ToLower(sc.Text()))
		for _, prefix := range []string{"import", "from ", "use ", "using ", "mod ", "const ", "require", "#include"} {
			if strings.HasPrefix(line, prefix) {
				b.WriteString(line)
				b.WriteByte('\n')
				lines++
				break
			}
		}
	}
	return b.String()
}

// mappedByName reports whether test (basename, no dir) matches the naming
// convention for source stem in the given language.
func mappedByName(language, sourceDir, sourceStem, testPath string) bool {
	testDir := filepath.ToSlash(filepath.Dir(testPath))
	testBase := strings.ToLower(filepath.Base(testPath))
	testStem := strings.TrimSuffix(testBase, filepath.Ext(testBase))
	switch language {
	case "go":
		return testDir == sourceDir && testStem == sourceStem+"_test"
	case "python":
		return testStem == "test_"+sourceStem || testStem == sourceStem+"_test"
	case "typescript":
		return strings.HasPrefix(testBase, sourceStem+".test.") || strings.HasPrefix(testBase, sourceStem+".spec.")
	case "java":
		return testStem == sourceStem+"test" || testStem == sourceStem+"tests"
	case "csharp":
		return testStem == sourceStem+"test" || testStem == sourceStem+"tests"
	case "rust":
		return false // rust maps via inline #[cfg(test)] or import fallback
	}
	return false
}

// buildTestMappings links each production source file to the test files that
// appear to exercise it, by name convention first and import mention second.
// It returns the (capped) mappings and the full unmapped-source count over
// coverage-expected files.
func buildTestMappings(root string, sources []sourceFileStat, tests []testFileStat) ([]model.TestMapping, int) {
	if len(sources) == 0 {
		return nil, 0
	}
	mappings := make([]model.TestMapping, 0, len(sources))
	unmapped := 0
	for _, s := range sources {
		base := strings.ToLower(filepath.Base(s.Path))
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		dir := filepath.ToSlash(filepath.Dir(s.Path))
		m := model.TestMapping{SourcePath: s.Path, Language: s.Language, Heuristic: true}
		if s.Language == "rust" && s.InlineTests {
			m.TestPaths = append(m.TestPaths, s.Path)
			m.AssertionCount += s.InlineAssertions
			m.TestFuncCount += s.InlineTestFuncs
		}
		for _, t := range tests {
			if t.Language != s.Language {
				continue
			}
			match := mappedByName(s.Language, dir, stem, t.Path)
			if !match && len(stem) >= 3 {
				match = strings.Contains(t.imports, stem)
			}
			if match {
				m.TestPaths = append(m.TestPaths, t.Path)
				m.AssertionCount += t.Assertions
				m.TestFuncCount += t.TestFuncs
			}
		}
		if len(m.TestPaths) == 0 && coverageExpected(root, s.Path, s.Language) {
			unmapped++
		}
		mappings = append(mappings, m)
	}
	sort.SliceStable(mappings, func(i, j int) bool {
		ui, uj := len(mappings[i].TestPaths) == 0, len(mappings[j].TestPaths) == 0
		if ui != uj {
			return ui
		}
		return mappings[i].SourcePath < mappings[j].SourcePath
	})
	if len(mappings) > maxTestMappings {
		mappings = mappings[:maxTestMappings]
	}
	return mappings, unmapped
}
