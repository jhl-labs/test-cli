package report

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"pct1": pct1,
	"add":  func(a, b int) int { return a + b },
}).ParseFS(templatesFS, "templates/*.tmpl"))

// writeHTML renders the full HTML site: overview and QA insights pages plus
// coverage maps and code-cov style source heatmaps. Returns every file written.
func writeHTML(r *model.Report, outDir, root string) ([]string, error) {
	if err := ensureDir(outDir); err != nil {
		return nil, err
	}
	// Coverage pages are a generated subtree. Remove pages from earlier runs so
	// deleted files or a coverage-free re-render cannot leave stale source code
	// accessible in the report artifact.
	covDir := filepath.Join(outDir, "coverage")
	if err := os.RemoveAll(covDir); err != nil {
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

	// Cross-cutting QA diagnostics and test-performance insights.
	insightsPath := filepath.Join(outDir, "insights.html")
	if err := renderTemplate("insights.html.tmpl", insightsPath, buildInsights(r)); err != nil {
		return nil, err
	}
	written = append(written, insightsPath)

	// Coverage heatmap site.
	if len(r.Coverage.Files) > 0 {
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
	Schema      string
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
	Quality     model.QualityReport
	ScoreGrade  string
	TopFindings []model.QualityFinding
	Comparison  *model.QualityComparison
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
	Path      string
	Label     string
	Slug      string
	Pct       string
	PctNum    float64
	Covered   int
	Total     int
	Uncovered int
	Branches  model.Metric
	Grade     string
	Weight    int
}

func buildDashboard(r *model.Report) dashboardView {
	d := dashboardView{
		Schema:      r.Schema,
		ToolVersion: r.ToolVersion,
		GeneratedAt: r.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		Root:        r.Root,
		Languages:   r.Languages,
		Test:        r.Test.Summary,
		PassRate:    pct1(r.Test.Summary.PassRate() * 100),
		Coverage:    r.Coverage.Summary,
		HasCoverage: r.Coverage.Summary.Lines.Total > 0,
		LineGrade:   gradeClass(r.Coverage.Summary.Lines.Pct),
		Quality:     r.Quality,
		ScoreGrade:  gradeClass(float64(r.Quality.Score)),
		Comparison:  r.Quality.Comparison,
	}
	if r.Test.Summary.Total-r.Test.Summary.Skipped <= 0 {
		d.Status, d.StatusClass = "NO TESTS", "warn"
	} else if r.Test.Summary.Passing() {
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
			Path:      f.Path,
			Slug:      slugFor(f.Path),
			Pct:       pct1(f.Lines.Pct),
			PctNum:    f.Lines.Pct,
			Covered:   f.Lines.Covered,
			Total:     f.Lines.Total,
			Uncovered: f.Lines.Total - f.Lines.Covered,
			Branches:  f.Branches,
			Grade:     gradeClass(f.Lines.Pct),
		})
	}
	if len(r.Quality.Findings) > 5 {
		d.TopFindings = r.Quality.Findings[:5]
	} else {
		d.TopFindings = r.Quality.Findings
	}
	return d
}

type coverageIndexView struct {
	Schema      string
	ToolVersion string
	GeneratedAt string
	Summary     model.CoverageSummary
	LineGrade   string
	Files       []fileRow
	Tree        []coverageTreeRow
}

func buildCoverageIndex(r *model.Report) coverageIndexView {
	v := coverageIndexView{
		Schema:      r.Schema,
		ToolVersion: r.ToolVersion,
		GeneratedAt: r.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		Summary:     r.Coverage.Summary,
		LineGrade:   gradeClass(r.Coverage.Summary.Lines.Pct),
	}
	prefix := commonCoveragePrefix(r.Coverage.Files)
	for _, f := range r.Coverage.Files {
		v.Files = append(v.Files, fileRow{
			Path:      f.Path,
			Label:     trimCoveragePrefix(f.Path, prefix),
			Slug:      slugFor(f.Path),
			Pct:       pct1(f.Lines.Pct),
			PctNum:    f.Lines.Pct,
			Covered:   f.Lines.Covered,
			Total:     f.Lines.Total,
			Uncovered: f.Lines.Total - f.Lines.Covered,
			Branches:  f.Branches,
			Grade:     gradeClass(f.Lines.Pct),
			Weight:    max(1, int(math.Sqrt(float64(max(1, f.Lines.Total))))),
		})
	}
	v.Tree = buildCoverageTree(r.Coverage.Files)
	return v
}

type coverageTreeRow struct {
	Path     string
	Label    string
	Slug     string
	Depth    int
	Indent   int
	IsDir    bool
	Files    int
	Covered  int
	Total    int
	Pct      string
	PctNum   float64
	Grade    string
	Branches model.Metric
}

type coverageAggregate struct {
	lines    model.Metric
	branches model.Metric
	files    int
}

func buildCoverageTree(files []model.FileCoverage) []coverageTreeRow {
	dirs := map[string]*coverageAggregate{".": {}}
	prefix := commonCoveragePrefix(files)
	for _, f := range files {
		p := trimCoveragePrefix(f.Path, prefix)
		parts := strings.Split(p, "/")
		parents := []string{"."}
		for i := 1; i < len(parts); i++ {
			parents = append(parents, strings.Join(parts[:i], "/"))
		}
		for _, parent := range parents {
			a := dirs[parent]
			if a == nil {
				a = &coverageAggregate{}
				dirs[parent] = a
			}
			a.files++
			a.lines.Covered += f.Lines.Covered
			a.lines.Total += f.Lines.Total
			a.branches.Covered += f.Branches.Covered
			a.branches.Total += f.Branches.Total
		}
	}
	type treeEntry struct {
		path  string
		isDir bool
		file  *model.FileCoverage
	}
	entries := make([]treeEntry, 0, len(dirs)+len(files))
	for path := range dirs {
		entries = append(entries, treeEntry{path: path, isDir: true})
	}
	for i := range files {
		f := files[i]
		entries = append(entries, treeEntry{path: trimCoveragePrefix(f.Path, prefix), file: &f})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].path == "." {
			return true
		}
		if entries[j].path == "." {
			return false
		}
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		return entries[i].isDir
	})
	rows := make([]coverageTreeRow, 0, len(entries))
	for _, entry := range entries {
		if entry.isDir {
			a := dirs[entry.path]
			a.lines.Recompute()
			a.branches.Recompute()
			label, depth := entry.path, 0
			if entry.path != "." {
				label = filepath.Base(entry.path)
				depth = strings.Count(entry.path, "/") + 1
			}
			rows = append(rows, coverageTreeRow{Path: entry.path, Label: label, Depth: depth, Indent: depth * 18, IsDir: true, Files: a.files, Covered: a.lines.Covered, Total: a.lines.Total, Pct: pct1(a.lines.Pct), PctNum: a.lines.Pct, Grade: gradeClass(a.lines.Pct), Branches: a.branches})
			continue
		}
		f := entry.file
		depth := strings.Count(entry.path, "/") + 1
		rows = append(rows, coverageTreeRow{Path: entry.path, Label: filepath.Base(entry.path), Slug: slugFor(f.Path), Depth: depth, Indent: depth * 18, Covered: f.Lines.Covered, Total: f.Lines.Total, Pct: pct1(f.Lines.Pct), PctNum: f.Lines.Pct, Grade: gradeClass(f.Lines.Pct), Branches: f.Branches})
	}
	return rows
}

func commonCoveragePrefix(files []model.FileCoverage) string {
	if len(files) < 2 {
		return ""
	}
	common := strings.Split(strings.TrimPrefix(filepath.ToSlash(files[0].Path), "./"), "/")
	common = common[:max(0, len(common)-1)] // directories only
	for _, f := range files[1:] {
		parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(f.Path), "./"), "/")
		limit := min(len(common), max(0, len(parts)-1))
		i := 0
		for i < limit && common[i] == parts[i] {
			i++
		}
		common = common[:i]
		if len(common) == 0 {
			return ""
		}
	}
	// Keep a single meaningful top-level directory such as src/ or app/.
	if len(common) < 2 {
		return ""
	}
	return strings.Join(common, "/") + "/"
}

func trimCoveragePrefix(path, prefix string) string {
	p := strings.TrimPrefix(filepath.ToSlash(path), "./")
	if prefix != "" {
		p = strings.TrimPrefix(p, prefix)
	}
	return p
}

type insightsView struct {
	Schema               string
	ToolVersion          string
	GeneratedAt          string
	Quality              model.QualityReport
	ScoreGrade           string
	StaticRatio          string
	SlowTests            []slowTestRow
	CoverageHotspots     []coverageRiskRow
	RiskPoints           []riskPoint
	HasStaticAnalysis    bool
	BaselineAt           string
	CoverageRegressions  []comparisonCoverageRow
	CoverageImprovements []comparisonCoverageRow
	HistorySamples       []historySampleRow
	ChangeFiles          []changeFileRow
}

type slowTestRow struct {
	model.TestHotspot
	Duration string
	Heat     int
}

type coverageRiskRow struct {
	model.CoverageHotspot
	Slug  string
	Grade string
}

type riskPoint struct {
	Path   string
	Slug   string
	Risk   string
	Left   float64
	Bottom float64
	Size   int
}

type comparisonCoverageRow struct {
	model.CoverageChange
	Slug string
}

type historySampleRow struct {
	model.HistorySample
	Generated string
	Duration  string
}

type changeFileRow struct {
	model.ChangedFileCoverage
	Slug           string
	Grade          string
	UncoveredText  string
	FirstUncovered int
	MapWeight      int
}

func buildInsights(r *model.Report) insightsView {
	v := insightsView{
		Schema:            r.Schema,
		ToolVersion:       r.ToolVersion,
		GeneratedAt:       r.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		Quality:           r.Quality,
		ScoreGrade:        gradeClass(float64(r.Quality.Score)),
		StaticRatio:       fmt2(r.Quality.Static.TestToSourceRatio),
		HasStaticAnalysis: r.Quality.Static.SourceFiles+r.Quality.Static.TestFiles > 0,
	}
	if comparison := r.Quality.Comparison; comparison != nil {
		v.BaselineAt = comparison.BaselineGeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC")
		for _, change := range comparison.CoverageRegressions {
			v.CoverageRegressions = append(v.CoverageRegressions, comparisonCoverageRow{CoverageChange: change, Slug: slugFor(change.Path)})
		}
		for _, change := range comparison.CoverageImprovements {
			v.CoverageImprovements = append(v.CoverageImprovements, comparisonCoverageRow{CoverageChange: change, Slug: slugFor(change.Path)})
		}
	}
	if history := r.Quality.History; history != nil {
		for _, sample := range history.Samples {
			v.HistorySamples = append(v.HistorySamples, historySampleRow{
				HistorySample: sample,
				Generated:     sample.GeneratedAt.UTC().Format("Jan 02 15:04"),
				Duration:      durationText(sample.DurationMs),
			})
		}
	}
	if changes := r.Quality.Changes; changes != nil {
		for _, file := range changes.Files {
			coveragePath := firstNonEmptyText(file.CoveragePath, file.Path)
			row := changeFileRow{
				ChangedFileCoverage: file,
				Slug:                slugFor(coveragePath),
				Grade:               gradeClass(file.CoveragePct),
				MapWeight:           max(1, min(8, int(math.Ceil(math.Sqrt(float64(file.ChangedLines)))))),
			}
			if len(file.UncoveredLineNumbers) > 0 {
				row.FirstUncovered = file.UncoveredLineNumbers[0]
				limit := min(12, len(file.UncoveredLineNumbers))
				parts := make([]string, 0, limit)
				for _, line := range file.UncoveredLineNumbers[:limit] {
					parts = append(parts, fmt.Sprintf("L%d", line))
				}
				row.UncoveredText = strings.Join(parts, ", ")
				if len(file.UncoveredLineNumbers) > limit || file.UncoveredLines > len(file.UncoveredLineNumbers) {
					row.UncoveredText += " …"
				}
			}
			v.ChangeFiles = append(v.ChangeFiles, row)
		}
	}
	maxDuration := 0.0
	for _, test := range r.Quality.SlowTests {
		maxDuration = math.Max(maxDuration, test.DurationMs)
	}
	for _, test := range r.Quality.SlowTests {
		heat := 0
		if maxDuration > 0 {
			heat = min(4, int(math.Ceil(test.DurationMs/maxDuration*4)))
		}
		v.SlowTests = append(v.SlowTests, slowTestRow{TestHotspot: test, Duration: durationText(test.DurationMs), Heat: heat})
	}
	maxUncovered := 0
	for _, h := range r.Quality.CoverageHotspots {
		maxUncovered = max(maxUncovered, h.UncoveredLines)
		v.CoverageHotspots = append(v.CoverageHotspots, coverageRiskRow{CoverageHotspot: h, Slug: slugFor(h.Path), Grade: gradeClass(h.CoveragePct)})
	}
	for _, h := range r.Quality.CoverageHotspots {
		bottom := 5.0
		if maxUncovered > 0 {
			bottom += math.Log1p(float64(h.UncoveredLines)) / math.Log1p(float64(maxUncovered)) * 85
		}
		size := 10 + min(14, int(math.Sqrt(float64(h.UncoveredLines))))
		v.RiskPoints = append(v.RiskPoints, riskPoint{Path: h.Path, Slug: slugFor(h.Path), Risk: h.Risk, Left: h.CoveragePct, Bottom: bottom, Size: size})
	}
	return v
}

func durationText(ms float64) string {
	if ms >= 1000 {
		return fmt2(ms/1000) + " s"
	}
	return fmt.Sprintf("%.0f ms", ms)
}

func fmt2(v float64) string { return fmt.Sprintf("%.2f", v) }

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
	// Resolve only beneath root while progressively trimming leading segments.
	// This resolves Go coverage import paths (e.g.
	// "github.com/org/repo/internal/x.go") and other prefixed paths against the
	// repository-relative source file ("internal/x.go") without allowing an
	// artifact path or symlink to read files outside the analyzed repository.
	if strings.TrimSpace(root) == "" {
		return nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil
	}
	segments := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i < len(segments); i++ {
		rel := strings.Join(segments[i:], "/")
		candidate := filepath.Join(rootAbs, filepath.FromSlash(rel))
		if !pathWithinRoot(rootAbs, candidate) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || !pathWithinRoot(rootResolved, resolved) {
			continue
		}
		if data, err := os.ReadFile(resolved); err == nil {
			return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		}
	}
	return nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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

// slugFor turns a file path into a stable, bounded, collision-resistant HTML
// filename. Coverage paths may contain separators, spaces, or names that map
// to the same sanitized text, so a digest suffix is part of the filename.
func slugFor(path string) string {
	const maxBaseBytes = 96
	var base strings.Builder
	for _, r := range filepath.ToSlash(path) {
		if base.Len() >= maxBaseBytes {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			base.WriteRune(r)
		default:
			base.WriteByte('_')
		}
	}
	name := strings.Trim(base.String(), "._")
	if name == "" {
		name = "source"
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(path)))
	return fmt.Sprintf("%s-%x.html", name, digest[:6])
}
