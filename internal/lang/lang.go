// Package lang holds the per-language adapters. An adapter knows how to (a)
// detect that a language is present in a project, (b) run that language's tests
// with coverage, and (c) where the resulting native artifacts land. The
// artifacts themselves are parsed by the ingest package, which sniffs formats,
// so adapters stay small and declarative.
package lang

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command is a single shell-less invocation. {out} is replaced with the raw
// output directory for the language and {root} with the project root. When
// Stdout is set, the command's standard output is redirected to {out}/<Stdout>.
type Command struct {
	Args   []string
	Stdout string
}

// Adapter is the declarative description of how to test one language.
type Adapter struct {
	Name     string   // canonical id: python, typescript, go, rust, csharp, java
	Title    string   // human label
	Markers  []string // files/dirs whose presence (in root) signals this language
	Globs    []string // shell globs (matched in root and one level deep)
	Commands []Command
	// TestGlobs and CovGlobs locate native artifacts after the run. Patterns are
	// resolved relative to the raw output dir first, then the project root.
	TestGlobs []string
	CovGlobs  []string
	Doctor    []string // candidate executables; the first found is reported
	DocsURL   string
}

// Registry is the ordered list of supported languages.
var Registry = []*Adapter{
	python(),
	typescript(),
	golang(),
	rust(),
	csharp(),
	java(),
}

// Names returns the canonical ids of every supported language.
func Names() []string {
	out := make([]string, len(Registry))
	for i, a := range Registry {
		out[i] = a.Name
	}
	return out
}

// Get returns the adapter with the given canonical name, or nil.
func Get(name string) *Adapter {
	for _, a := range Registry {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// Detect returns the adapters whose markers are present under root, in
// Registry order (stable).
func Detect(root string) []*Adapter {
	var found []*Adapter
	for _, a := range Registry {
		if a.Present(root) {
			found = append(found, a)
		}
	}
	return found
}

// Present reports whether this language is detected under root.
func (a *Adapter) Present(root string) bool {
	for _, m := range a.Markers {
		if exists(filepath.Join(root, m)) {
			return true
		}
	}
	for _, g := range a.Globs {
		if globMatch(root, g) {
			return true
		}
	}
	return false
}

// Render replaces {out} and {root} placeholders in a command's arguments.
func (c Command) Render(out, root string) []string {
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		a = strings.ReplaceAll(a, "{out}", out)
		a = strings.ReplaceAll(a, "{root}", root)
		args[i] = a
	}
	return args
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// globMatch checks a glob in the root directory and one level of subdirectories
// (covers e.g. a *.csproj nested under src/).
func globMatch(root, pattern string) bool {
	if m, _ := filepath.Glob(filepath.Join(root, pattern)); len(m) > 0 {
		return true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if m, _ := filepath.Glob(filepath.Join(root, e.Name(), pattern)); len(m) > 0 {
			return true
		}
	}
	return false
}

// FindArtifacts resolves the given globs relative to out then root, returning a
// de-duplicated, sorted list of existing files. Patterns containing "**" are
// resolved with a recursive directory walk (filepath.Glob does not support it).
func FindArtifacts(globs []string, out, root string) []string {
	set := map[string]struct{}{}
	add := func(m string) {
		if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
			set[m] = struct{}{}
		}
	}
	for _, g := range globs {
		for _, base := range []string{out, root} {
			if strings.Contains(g, "**") {
				for _, m := range recursiveGlob(base, g) {
					add(m)
				}
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(base, g))
			for _, m := range matches {
				add(m)
			}
		}
	}
	out2 := make([]string, 0, len(set))
	for k := range set {
		out2 = append(out2, k)
	}
	sort.Strings(out2)
	return out2
}

// recursiveGlob matches a "**"-containing pattern by walking base and testing
// each file's base-relative path against the simplified pattern.
func recursiveGlob(base, pattern string) []string {
	// Reduce "a/**/b.xml" to a final-segment match on "b.xml".
	last := pattern
	if i := strings.LastIndex(pattern, "**/"); i >= 0 {
		last = pattern[i+3:]
	}
	var matches []string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(last, d.Name()); ok {
			matches = append(matches, path)
		}
		return nil
	})
	return matches
}
