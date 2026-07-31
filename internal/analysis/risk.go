package analysis

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// ChurnWindowDays bounds the git history window used for churn counting. The
// CLI overrides it from .test-cli.json (riskChurnDays) before Evaluate runs.
var ChurnWindowDays = 90

const maxRiskFiles = 25

// analyzeRisk combines within-project churn and complexity percentiles with
// the uncovered fraction into a 0..1 risk score per source file. It returns
// nil whenever git history is unavailable so ingest-only runs are unaffected.
func analyzeRisk(r *model.Report, root string, stats []sourceFileStat) *model.RiskAnalysis {
	if root == "" || len(stats) == 0 {
		return nil
	}
	out, err := gitOutput(root, "log", fmt.Sprintf("--since=%d.days", ChurnWindowDays), "--name-only", "--pretty=format:")
	if err != nil {
		return nil
	}
	churn := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			churn[filepath.ToSlash(line)]++
		}
	}
	coverage := map[string]model.FileCoverage{}
	for _, f := range r.Coverage.Files {
		coverage[filepath.ToSlash(f.Path)] = f
	}

	files := make([]model.RiskFile, 0, len(stats))
	for _, s := range stats {
		if !coverageExpected(root, s.Path, s.Language) {
			continue
		}
		pct := 0.0
		if fc, ok := findCoverage(coverage, s.Path); ok && fc.Lines.Total > 0 {
			pct = fc.Lines.Pct
		}
		files = append(files, model.RiskFile{Path: s.Path, Language: s.Language, Churn: churn[s.Path], Complexity: s.Complexity, CoveragePct: pct})
	}
	if len(files) == 0 {
		return nil
	}
	churnPct := rankPercentiles(files, func(f model.RiskFile) float64 { return float64(f.Churn) })
	complexityPct := rankPercentiles(files, func(f model.RiskFile) float64 { return float64(f.Complexity) })
	for i := range files {
		gap := (100 - files[i].CoveragePct) / 100
		score := math.Pow(churnPct[i], 0.3) * math.Pow(complexityPct[i], 0.3) * math.Pow(gap, 0.4)
		files[i].RiskScore = math.Round(score*10000) / 10000
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].RiskScore != files[j].RiskScore {
			return files[i].RiskScore > files[j].RiskScore
		}
		return files[i].Path < files[j].Path
	})
	if len(files) > maxRiskFiles {
		files = files[:maxRiskFiles]
	}
	return &model.RiskAnalysis{Base: fmt.Sprintf("%d days", ChurnWindowDays), Files: files}
}

// findCoverage matches a scanned source path against coverage report paths,
// tolerating coverage paths that carry extra leading directories.
func findCoverage(coverage map[string]model.FileCoverage, path string) (model.FileCoverage, bool) {
	if fc, ok := coverage[path]; ok {
		return fc, true
	}
	for cp, fc := range coverage {
		if strings.HasSuffix(cp, "/"+path) || strings.HasSuffix(path, "/"+cp) {
			return fc, true
		}
	}
	return model.FileCoverage{}, false
}

// rankPercentiles converts each value to its rank percentile in (0,1], with
// ties sharing the same percentile. A file with the lowest churn still gets a
// small non-zero percentile so multiplication does not zero out every score.
func rankPercentiles(files []model.RiskFile, value func(model.RiskFile) float64) []float64 {
	n := len(files)
	sorted := make([]float64, n)
	for i, f := range files {
		sorted[i] = value(f)
	}
	sort.Float64s(sorted)
	out := make([]float64, n)
	for i, f := range files {
		v := value(f)
		// count of values <= v
		rank := sort.SearchFloat64s(sorted, v+1e-9)
		out[i] = float64(rank) / float64(n)
	}
	return out
}

func riskFindings(r *model.Report) []model.QualityFinding {
	if r.Risk == nil || len(r.Risk.Files) == 0 {
		return nil
	}
	top := r.Risk.Files[0]
	if top.RiskScore < 0.6 {
		return nil
	}
	count := 0
	for _, f := range r.Risk.Files {
		if f.RiskScore >= 0.6 {
			count++
		}
	}
	return []model.QualityFinding{{
		ID:             "RISK-001",
		Severity:       "high",
		Category:       "risk",
		Title:          "High-churn, low-coverage risk hotspots",
		Detail:         "Files that change often, are comparatively complex, and lack coverage concentrate regression risk.",
		Evidence:       fmt.Sprintf("%d file(s) with risk score >= 0.6; top: %s (churn %d, complexity %d, coverage %.1f%%)", count, top.Path, top.Churn, top.Complexity, top.CoveragePct),
		Recommendation: "Add tests to the top entries of report.risk before lower-risk files; churn keeps re-exposing these paths.",
	}}
}
