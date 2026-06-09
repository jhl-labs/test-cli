package report

import (
	"embed"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"pct1": pct1,
	"add":  func(a, b int) int { return a + b },
}).ParseFS(templatesFS, "templates/*.tmpl"))

// writeHTML renders the full HTML site: a dashboard (index.html) plus a
// code-cov style coverage heatmap under coverage/. Returns every file written.
func writeHTML(r *model.Report, outDir, root string) ([]string, error) {
	if err := ensureDir(outDir); err != nil {
		return nil, err
	}
	var written []string

	// Dashboard.
	dash := buildDashboard(r)
	indexPath := filepath.Join(outDir, "index.html")
	if err := renderTemplate("dashboard.html.tmpl", indexPath, dash); err != nil {
		return nil, err
	}
	written = append(written, indexPath)

	// Coverage heatmap site.
	if len(r.Coverage.Files) > 0 {
		covDir := filepath.Join(outDir, "coverage")
		if err := ensureDir(covDir); err != nil {
			return nil, err
		}
		idx := buildCoverageIndex(r)
		idxPath := filepath.Join(covDir, "index.html")
		if err := renderTemplate("coverage_index.html.tmpl", idxPath, idx); err != nil {
			return nil, err
		}
		written = append(written, idxPath)

		for _, f := range r.Coverage.Files {
			view := buildCoverageFile(f, root)
			fpath := filepath.Join(covDir, view.Slug)
			if err := renderTemplate("coverage_file.html.tmpl", fpath, view); err != nil {
				return nil, err
			}
			written = append(written, fpath)
		}
	}
	return written, nil
}

func renderTemplate(name, path string, data any) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.ExecuteTemplate(f, name, data)
}

// --- view models ---

type dashboardView struct {
	ToolVersion string
	GeneratedAt string
	Root        string
	Status      string // PASS / FAIL
	StatusClass string
	Languages   []string
	Test        model.TestSummary
	PassRate    string
	Coverage    model.CoverageSummary
	HasCoverage bool
	LineGrade   string
	LangRows    []langRow
	Failures    []failureRow
	WorstFiles  []fileRow
}

type langRow struct {
	Name     string
	Tests    int
	Failed   int
	Skipped  int
	CovPct   string
	CovGrade string
}

type failureRow struct {
	Language string
	Suite    string
	Name     string
	Message  string
	Detail   string
}

type fileRow struct {
	Path    string
	Slug    string
	Pct     string
	PctNum  float64
	Covered int
	Total   int
	Grade   string
}

func buildDashboard(r *model.Report) dashboardView {
	d := dashboardView{
		ToolVersion: r.ToolVersion,
		GeneratedAt: r.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		Root:        r.Root,
		Languages:   r.Languages,
		Test:        r.Test.Summary,
		PassRate:    pct1(r.Test.Summary.PassRate() * 100),
		Coverage:    r.Coverage.Summary,
		HasCoverage: r.Coverage.Summary.Lines.Total > 0,
		LineGrade:   gradeClass(r.Coverage.Summary.Lines.Pct),
	}
	if r.Test.Summary.Passing() {
		d.Status, d.StatusClass = "PASS", "pass"
	} else {
		d.Status, d.StatusClass = "FAIL", "fail"
	}

	byLang := groupByLanguage(r)
	for _, l := range sortedKeys(byLang) {
		s := byLang[l]
		d.LangRows = append(d.LangRows, langRow{
			Name:     l,
			Tests:    s.tests.Total,
			Failed:   s.tests.Failed + s.tests.Errors,
			Skipped:  s.tests.Skipped,
			CovPct:   pct1(s.linePct()),
			CovGrade: gradeClass(s.linePct()),
		})
	}
	for _, fc := range failedCases(r) {
		d.Failures = append(d.Failures, failureRow{
			Language: fc.Language,
			Suite:    fc.Suite,
			Name:     fc.Case.Name,
			Message:  fc.Case.Message,
			Detail:   fc.Case.Detail,
		})
	}
	for _, f := range worstFiles(r.Coverage.Files, 15) {
		d.WorstFiles = append(d.WorstFiles, fileRow{
			Path:    f.Path,
			Slug:    slugFor(f.Path),
			Pct:     pct1(f.Lines.Pct),
			PctNum:  f.Lines.Pct,
			Covered: f.Lines.Covered,
			Total:   f.Lines.Total,
			Grade:   gradeClass(f.Lines.Pct),
		})
	}
	return d
}

type coverageIndexView struct {
	ToolVersion string
	GeneratedAt string
	Summary     model.CoverageSummary
	LineGrade   string
	Files       []fileRow
}

func buildCoverageIndex(r *model.Report) coverageIndexView {
	v := coverageIndexView{
		ToolVersion: r.ToolVersion,
		GeneratedAt: r.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		Summary:     r.Coverage.Summary,
		LineGrade:   gradeClass(r.Coverage.Summary.Lines.Pct),
	}
	for _, f := range r.Coverage.Files {
		v.Files = append(v.Files, fileRow{
			Path:    f.Path,
			Slug:    slugFor(f.Path),
			Pct:     pct1(f.Lines.Pct),
			PctNum:  f.Lines.Pct,
			Covered: f.Lines.Covered,
			Total:   f.Lines.Total,
			Grade:   gradeClass(f.Lines.Pct),
		})
	}
	return v
}

type coverageFileView struct {
	Path      string
	Slug      string
	Summary   model.Metric
	Branches  model.Metric
	LineGrade string
	Lines     []lineRow
	HasSource bool
}

type lineRow struct {
	Number int
	Hits   int
	Class  string // hit / miss / neutral
	Code   string
}

func buildCoverageFile(f model.FileCoverage, root string) coverageFileView {
	v := coverageFileView{
		Path:      f.Path,
		Slug:      slugFor(f.Path),
		Summary:   f.Lines,
		Branches:  f.Branches,
		LineGrade: gradeClass(f.Lines.Pct),
	}
	hits := map[int]int{}
	for _, lh := range f.LineHits {
		hits[lh.Line] = lh.Hits
	}

	src := readSource(root, f.Path)
	if len(src) > 0 {
		v.HasSource = true
		for i, code := range src {
			ln := i + 1
			v.Lines = append(v.Lines, lineRow{Number: ln, Hits: hitOrNeg(hits, ln), Class: classFor(hits, ln), Code: code})
		}
	} else {
		// No source available: render only instrumented lines with hit counts.
		for _, lh := range f.LineHits {
			v.Lines = append(v.Lines, lineRow{Number: lh.Line, Hits: lh.Hits, Class: classForHits(lh.Hits)})
		}
	}
	return v
}

func readSource(root, path string) []string {
	// Try the path as-is and as an absolute path first, then progressively trim
	// leading segments. This resolves Go coverage import paths (e.g.
	// "github.com/org/repo/internal/x.go") and other prefixed paths against the
	// repository-relative source file ("internal/x.go").
	segments := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i < len(segments); i++ {
		rel := strings.Join(segments[i:], "/")
		for _, c := range []string{filepath.Join(root, rel), rel} {
			if data, err := os.ReadFile(c); err == nil {
				return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
			}
		}
	}
	return nil
}

func hitOrNeg(hits map[int]int, ln int) int {
	if h, ok := hits[ln]; ok {
		return h
	}
	return -1
}

func classFor(hits map[int]int, ln int) string {
	h, ok := hits[ln]
	if !ok {
		return "neutral"
	}
	return classForHits(h)
}

func classForHits(h int) string {
	if h > 0 {
		return "hit"
	}
	if h == 0 {
		return "miss"
	}
	return "neutral"
}

// slugFor turns a file path into a stable HTML filename.
func slugFor(path string) string {
	s := strings.NewReplacer("/", "__", "\\", "__", ":", "_", " ", "_").Replace(path)
	return s + ".html"
}
