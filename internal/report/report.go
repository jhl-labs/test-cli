// Package report renders a normalized model.Report into the formats consumers
// need: machine-readable interchange (json, junit, cobertura), human summaries
// (stdout, markdown), and rich visualizations (html dashboard + code-cov style
// coverage heatmap). Every format is derived from the same Report so they never
// disagree.
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// Format identifiers accepted by --format.
const (
	FormatStdout    = "stdout"
	FormatJSON      = "json"
	FormatJUnit     = "junit"
	FormatCobertura = "cobertura"
	FormatMarkdown  = "markdown"
	FormatHTML      = "html"
)

// AllFormats lists every renderable format (stdout excluded; it is implicit).
var AllFormats = []string{FormatJSON, FormatJUnit, FormatCobertura, FormatMarkdown, FormatHTML}

// FileName is the conventional output filename for a file-based format.
func FileName(format string) string {
	switch format {
	case FormatJSON:
		return "report.json"
	case FormatJUnit:
		return "junit.xml"
	case FormatCobertura:
		return "coverage.cobertura.xml"
	case FormatMarkdown:
		return "report.md"
	case FormatHTML:
		return "index.html"
	default:
		return format
	}
}

// Write renders the report in the given format. For FormatStdout it writes a
// summary to the provided writer; for file formats it writes into outDir and
// returns the path(s) produced. root is the project root, needed by the HTML
// coverage heatmap to read source files.
func Write(r *model.Report, format, outDir, root string) ([]string, error) {
	switch format {
	case FormatJSON:
		p := filepath.Join(outDir, FileName(format))
		return one(p), writeJSON(r, p)
	case FormatJUnit:
		p := filepath.Join(outDir, FileName(format))
		return one(p), writeJUnit(r, p)
	case FormatCobertura:
		p := filepath.Join(outDir, FileName(format))
		return one(p), writeCobertura(r, p)
	case FormatMarkdown:
		p := filepath.Join(outDir, FileName(format))
		return one(p), writeMarkdown(r, p)
	case FormatHTML:
		return writeHTML(r, outDir, root)
	default:
		return nil, fmt.Errorf("unknown format: %s", format)
	}
}

func one(p string) []string { return []string{p} }

func ensureDir(p string) error { return os.MkdirAll(p, 0o755) }

func writeFile(path, content string) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// --- small shared presentation helpers ---

// gradeClass maps a coverage percentage to a CSS class / qualitative band.
func gradeClass(pct float64) string {
	switch {
	case pct >= 90:
		return "high"
	case pct >= 75:
		return "good"
	case pct >= 50:
		return "medium"
	default:
		return "low"
	}
}

func pct1(v float64) string { return fmt.Sprintf("%.1f%%", v) }

func relPath(root, path string) string {
	if root == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// worstFiles returns up to n files with the lowest line coverage (that have at
// least one coverable line), sorted ascending by percentage.
func worstFiles(files []model.FileCoverage, n int) []model.FileCoverage {
	cp := make([]model.FileCoverage, 0, len(files))
	for _, f := range files {
		if f.Lines.Total > 0 {
			cp = append(cp, f)
		}
	}
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Lines.Pct < cp[j].Lines.Pct })
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp
}

// failedCases returns every non-passing case across all suites, for surfacing
// in summaries.
func failedCases(r *model.Report) []failedCase {
	var out []failedCase
	for _, s := range r.Test.Suites {
		for _, c := range s.Cases {
			if c.Status == model.StatusFailed || c.Status == model.StatusError {
				out = append(out, failedCase{Suite: s.Name, Language: s.Language, Case: c})
			}
		}
	}
	return out
}

type failedCase struct {
	Suite    string
	Language string
	Case     model.TestCase
}
