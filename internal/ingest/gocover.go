package ingest

import (
	"bufio"
	"bytes"
	"sort"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// A coverage block cannot legitimately span more lines than the largest
// source file test-cli will read for analysis/heatmaps. Bounding expansion
// prevents a corrupt profile from allocating an attacker-controlled map.
const maxExpandedCoverageLines = 2 * 1024 * 1024

// looksLikeGoCover reports whether data is a Go coverage profile (the output of
// `go test -coverprofile`). The first line is always a mode header.
func looksLikeGoCover(data []byte) bool {
	head := sniffHead(data)
	return strings.HasPrefix(head, "mode: set") ||
		strings.HasPrefix(head, "mode: count") ||
		strings.HasPrefix(head, "mode: atomic")
}

// ParseGoCover parses a Go coverage profile. Each entry covers a block:
//
//	file.go:startLine.col,endLine.col numStatements count
//
// We expand blocks to per-line hit counts (max count wins for overlaps).
func ParseGoCover(data []byte, language string) ([]model.FileCoverage, error) {
	hits := map[string]map[int]int{} // file -> line -> hits
	var order []string

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// Split "file:range stmts count".
		colon := strings.LastIndexByte(line, ':')
		if colon < 0 {
			continue
		}
		file := line[:colon]
		if normalizePath(file) == "" {
			continue
		}
		rest := strings.Fields(line[colon+1:])
		if len(rest) != 3 {
			continue
		}
		rangePart := rest[0]
		statements, statementsOK := parseNonNegativeInt(rest[1])
		count, countOK := parseNonNegativeInt(rest[2])
		if !statementsOK || statements == 0 || !countOK {
			continue
		}

		startEnd := strings.SplitN(rangePart, ",", 2)
		if len(startEnd) != 2 {
			continue
		}
		startLine, startOK := parseNonNegativeInt(strings.SplitN(startEnd[0], ".", 2)[0])
		endLine, endOK := parseNonNegativeInt(strings.SplitN(startEnd[1], ".", 2)[0])
		if !startOK || !endOK || startLine == 0 || endLine < startLine || endLine-startLine >= maxExpandedCoverageLines {
			continue
		}
		fm, ok := hits[file]
		if !ok {
			fm = map[int]int{}
			hits[file] = fm
			order = append(order, file)
		}
		for ln := startLine; ; ln++ {
			if existing, seen := fm[ln]; !seen || count > existing {
				fm[ln] = count
			}
			if ln == endLine {
				break
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var out []model.FileCoverage
	for _, file := range order {
		fm := hits[file]
		fc := model.FileCoverage{Path: normalizePath(file), Language: language}
		lines := make([]int, 0, len(fm))
		for ln := range fm {
			lines = append(lines, ln)
		}
		sort.Ints(lines)
		for _, ln := range lines {
			h := fm[ln]
			fc.LineHits = append(fc.LineHits, model.LineHit{Line: ln, Hits: h})
			fc.Lines.Total++
			if h > 0 {
				fc.Lines.Covered++
			}
		}
		out = append(out, fc)
	}
	return out, nil
}
