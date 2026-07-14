package analysis

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

const maxUncoveredChangeLines = 200

// AnalyzeGitChanges reads the working tree diff relative to base and attaches
// changed-line coverage evidence to current. The diff includes committed,
// staged, unstaged, and untracked files scoped to root.
func AnalyzeGitChanges(current *model.Report, root, base string) error {
	changed, err := GitChangedLines(root, base)
	if err != nil {
		return err
	}
	analyzeChanges(current, root, base, changed)
	return nil
}

// GitChangedLines returns added/modified line numbers by repository-relative
// path. It intentionally excludes deleted lines because they cannot require
// coverage in the current source tree.
func GitChangedLines(root, base string) (map[string][]int, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil, fmt.Errorf("empty Git diff base")
	}
	resolved, err := gitOutput(root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve Git base %q: %w", base, err)
	}
	commit := strings.TrimSpace(string(resolved))
	if mergeBase, mergeErr := gitOutput(root, "merge-base", commit, "HEAD"); mergeErr == nil && strings.TrimSpace(string(mergeBase)) != "" {
		commit = strings.TrimSpace(string(mergeBase))
	}
	patch, err := gitOutput(root, "-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--unified=0", "--diff-filter=ACMR", "--relative", commit, "--", ".")
	if err != nil {
		return nil, fmt.Errorf("Git diff from %q: %w", base, err)
	}
	changed := parseGitPatch(patch)

	untracked, err := gitOutput(root, "-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard", "-z", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", err)
	}
	for _, rawPath := range bytes.Split(untracked, []byte{0}) {
		path := normalizedChangePath(string(rawPath))
		if path == "" || ignoredChangePath(path) {
			continue
		}
		if _, tracked := changed[path]; tracked {
			continue
		}
		language := sourceLanguage(path)
		if language == "" || isTestFile(path, language) {
			// Only file presence is needed for non-production files; their line
			// count never enters changed-code coverage.
			changed[path] = []int{1}
			continue
		}
		lineCount, readErr := fileLineCount(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			continue
		}
		lines := make([]int, lineCount)
		for i := range lines {
			lines[i] = i + 1
		}
		changed[path] = lines
	}
	return changed, nil
}

func fileLineCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	count := 0
	hasData := false
	last := byte('\n')
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			hasData = true
			count += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if hasData && last != '\n' {
		count++
	}
	return count, nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return out, nil
}

func parseGitPatch(patch []byte) map[string][]int {
	lineSets := map[string]map[int]bool{}
	current := ""
	for _, raw := range strings.Split(strings.ReplaceAll(string(patch), "\r\n", "\n"), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(line, "+++ ") {
			current = patchPath(strings.TrimPrefix(line, "+++ "))
			if current != "" && lineSets[current] == nil {
				lineSets[current] = map[int]bool{}
			}
			continue
		}
		if current == "" || !strings.HasPrefix(line, "@@ ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
			continue
		}
		start, count, ok := parseHunkRange(strings.TrimPrefix(fields[2], "+"))
		if !ok {
			continue
		}
		for n := start; n < start+count; n++ {
			lineSets[current][n] = true
		}
	}
	out := make(map[string][]int, len(lineSets))
	for path, set := range lineSets {
		lines := make([]int, 0, len(set))
		for line := range set {
			lines = append(lines, line)
		}
		sort.Ints(lines)
		out[path] = lines
	}
	return out
}

func patchPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(value, "\"") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
	}
	value = strings.TrimPrefix(value, "b/")
	return normalizedChangePath(value)
}

func parseHunkRange(value string) (start, count int, ok bool) {
	parts := strings.SplitN(value, ",", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil || start <= 0 {
		return 0, 0, false
	}
	count = 1
	if len(parts) == 2 {
		count, err = strconv.Atoi(parts[1])
		if err != nil || count < 0 {
			return 0, 0, false
		}
	}
	return start, count, true
}

// AnalyzeChanges intersects changed production lines with portable line-hit
// coverage. Test and non-source files remain part of FilesChanged but do not
// enter patch-coverage metrics.
func AnalyzeChanges(current *model.Report, base string, changed map[string][]int) {
	analyzeChanges(current, current.Root, base, changed)
}

func analyzeChanges(current *model.Report, sourceRoot, base string, changed map[string][]int) {
	result := &model.ChangeAnalysis{Base: base}
	paths := make([]string, 0, len(changed))
	for path, lines := range changed {
		if ignoredChangePath(path) || len(uniquePositiveLines(lines)) == 0 {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result.FilesChanged = len(paths)
	for _, path := range paths {
		language := sourceLanguage(path)
		lines := uniquePositiveLines(changed[path])
		if language == "" || isTestFile(path, language) || len(lines) == 0 {
			continue
		}
		result.SourceFilesChanged++
		fileResult := model.ChangedFileCoverage{Path: normalizedChangePath(path), Language: language, ChangedLines: len(lines)}
		result.ChangedLines += len(lines)

		coverage, matched := changedFileCoverage(path, current.Coverage.Files)
		fileResult.CoverageReported = matched && len(coverage.LineHits) > 0
		fileResult.CoverageRequired = fileResult.CoverageReported || coverageExpected(sourceRoot, path, language)
		if !fileResult.CoverageReported {
			fileResult.UnmappedLines = len(lines)
			result.UnmappedLines += len(lines)
			if fileResult.CoverageRequired {
				result.FilesWithoutCoverage++
			}
			result.Files = append(result.Files, fileResult)
			continue
		}
		fileResult.CoveragePath = coverage.Path
		hits := map[int]int{}
		for _, hit := range coverage.LineHits {
			hits[hit.Line] = hit.Hits
		}
		for _, line := range lines {
			hit, coverable := hits[line]
			if !coverable || hit < 0 {
				fileResult.UnmappedLines++
				result.UnmappedLines++
				continue
			}
			fileResult.CoverableLines++
			result.CoverableLines++
			if hit > 0 {
				fileResult.CoveredLines++
				result.CoveredLines++
			} else {
				fileResult.UncoveredLines++
				result.UncoveredLines++
				if len(fileResult.UncoveredLineNumbers) < maxUncoveredChangeLines {
					fileResult.UncoveredLineNumbers = append(fileResult.UncoveredLineNumbers, line)
				}
			}
		}
		fileResult.CoveragePct = coveragePercent(fileResult.CoveredLines, fileResult.CoverableLines)
		result.Files = append(result.Files, fileResult)
	}
	result.CoveragePct = coveragePercent(result.CoveredLines, result.CoverableLines)
	sort.SliceStable(result.Files, func(i, j int) bool {
		a, b := result.Files[i], result.Files[j]
		aMissing := a.CoverageRequired && !a.CoverageReported
		bMissing := b.CoverageRequired && !b.CoverageReported
		if aMissing != bMissing {
			return aMissing
		}
		if a.UncoveredLines != b.UncoveredLines {
			return a.UncoveredLines > b.UncoveredLines
		}
		if a.ChangedLines != b.ChangedLines {
			return a.ChangedLines > b.ChangedLines
		}
		return a.Path < b.Path
	})
	current.Quality.Changes = result
	attachChangeFindings(current, result)
}

func changedFileCoverage(path string, files []model.FileCoverage) (model.FileCoverage, bool) {
	wanted := normalizedChangePath(path)
	for _, file := range files {
		candidate := normalizedChangePath(file.Path)
		if candidate == wanted {
			return file, true
		}
	}
	var matched model.FileCoverage
	matches := 0
	for _, file := range files {
		candidate := normalizedChangePath(file.Path)
		if strings.HasSuffix(candidate, "/"+wanted) || strings.HasSuffix(wanted, "/"+candidate) {
			matched = file
			matches++
		}
	}
	if matches == 1 {
		return matched, true
	}
	return model.FileCoverage{}, false
}

// coverageExpected suppresses missing-evidence findings for declaration-only
// Go/Rust files that their native coverage tools correctly omit because they
// contain no executable function body. Other languages remain conservative.
func coverageExpected(root, path, language string) bool {
	if root == "" || (language != "go" && language != "rust") {
		return true
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(normalizedChangePath(path))))
	if err != nil {
		return true
	}
	inBlockComment := false
	for _, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if inBlockComment {
			if end := strings.Index(line, "*/"); end >= 0 {
				line = strings.TrimSpace(line[end+2:])
				inBlockComment = false
			} else {
				continue
			}
		}
		if strings.HasPrefix(line, "/*") {
			if end := strings.Index(line[2:], "*/"); end >= 0 {
				line = strings.TrimSpace(line[end+4:])
			} else {
				inBlockComment = true
				continue
			}
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if language == "go" && (strings.HasPrefix(line, "func ") || strings.Contains(line, "func(")) {
			return true
		}
		if language == "rust" && (strings.HasPrefix(line, "fn ") || strings.Contains(line, " fn ")) {
			return true
		}
	}
	return false
}

func normalizedChangePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	return strings.TrimPrefix(path, "./")
}

func ignoredChangePath(path string) bool {
	for _, segment := range strings.Split(normalizedChangePath(path), "/") {
		if ignoredDir(segment) {
			return true
		}
	}
	return false
}

func uniquePositiveLines(lines []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(lines))
	for _, line := range lines {
		if line <= 0 || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	sort.Ints(out)
	return out
}

func coveragePercent(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(covered)/float64(total)*1000) / 10
}

func attachChangeFindings(current *model.Report, changes *model.ChangeAnalysis) {
	findings := make([]model.QualityFinding, 0, len(current.Quality.Findings))
	for _, finding := range current.Quality.Findings {
		if !strings.HasPrefix(finding.ID, "CHG-") {
			findings = append(findings, finding)
		}
	}
	add := func(id, severity, title, detail, evidence, recommendation string) {
		findings = append(findings, model.QualityFinding{ID: id, Severity: severity, Category: "change", Title: title, Detail: detail, Evidence: evidence, Recommendation: recommendation})
	}
	if changes.FilesWithoutCoverage > 0 {
		add("CHG-001", "high", "Changed source lacks line-level coverage evidence", "One or more changed production files could not be matched to line-hit coverage.", fmt.Sprintf("%d of %d changed source file(s)", changes.FilesWithoutCoverage, changes.SourceFilesChanged), "Make the coverage tool include these files and emit line-level data before treating aggregate coverage as sufficient.")
	}
	if changes.CoverableLines > 0 && changes.CoveragePct < 80 {
		severity := "medium"
		if changes.CoveragePct < 50 {
			severity = "high"
		}
		add("CHG-002", severity, "Changed-line coverage is below target", "Executable lines introduced or modified by this change are not sufficiently exercised.", fmt.Sprintf("%.1f%% (%d/%d changed executable lines)", changes.CoveragePct, changes.CoveredLines, changes.CoverableLines), "Start with the listed uncovered changed lines and add behavior-focused tests for the new branches, boundaries, and failure paths.")
	}
	if changes.ChangedLines >= 500 {
		add("CHG-003", "medium", "Large production change surface", "The change modifies a large number of production source lines, increasing review and regression risk.", fmt.Sprintf("%d lines across %d source files", changes.ChangedLines, changes.SourceFilesChanged), "Split independent behavior where practical and ensure reviewers can map each change cluster to focused tests.")
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := severityRank(findings[i].Severity), severityRank(findings[j].Severity)
		if a != b {
			return a < b
		}
		return findings[i].ID < findings[j].ID
	})
	current.Quality.Findings = findings
	current.Quality.Summary = summarize(findings)
	current.Quality.Risk = risk(current.Quality.Score, findings)
}
