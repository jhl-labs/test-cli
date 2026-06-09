package ingest

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// looksLikeLCOV reports whether data is an LCOV .info tracefile (jest, nyc,
// cargo-llvm-cov --lcov, etc.).
func looksLikeLCOV(data []byte) bool {
	head := sniffHead(data)
	return strings.HasPrefix(strings.TrimSpace(head), "TN:") ||
		strings.HasPrefix(strings.TrimSpace(head), "SF:") ||
		strings.Contains(head, "\nSF:")
}

// ParseLCOV parses an LCOV tracefile. Records are delimited by "end_of_record".
// DA: lines carry per-line hit counts; BRDA: lines carry branch data.
func ParseLCOV(data []byte, language string) ([]model.FileCoverage, error) {
	var out []model.FileCoverage
	var cur *model.FileCoverage

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SF:"):
			cur = &model.FileCoverage{Path: normalizePath(line[3:]), Language: language}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "DA:"):
			parts := strings.Split(line[3:], ",")
			if len(parts) >= 2 {
				n := atoiSafe(parts[0])
				hits := atoiSafe(parts[1])
				cur.LineHits = append(cur.LineHits, model.LineHit{Line: n, Hits: hits})
				cur.Lines.Total++
				if hits > 0 {
					cur.Lines.Covered++
				}
			}
		case strings.HasPrefix(line, "BRDA:"):
			parts := strings.Split(line[5:], ",")
			if len(parts) >= 4 {
				cur.Branches.Total++
				if parts[3] != "-" && parts[3] != "0" {
					cur.Branches.Covered++
				}
			}
		case line == "end_of_record":
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out, sc.Err()
}
