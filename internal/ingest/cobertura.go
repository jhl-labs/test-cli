package ingest

import (
	"encoding/xml"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// Cobertura is the most widely shared coverage interchange format: emitted by
// coverage.py (--cov-report=xml), jest/nyc (cobertura reporter), coverlet
// (.NET), and cargo-llvm-cov (--cobertura).
type coberturaRoot struct {
	XMLName  xml.Name           `xml:"coverage"`
	Sources  []string           `xml:"sources>source"`
	Packages []coberturaPackage `xml:"packages>package"`
}

type coberturaPackage struct {
	Classes []coberturaClass `xml:"classes>class"`
}

type coberturaClass struct {
	Filename string          `xml:"filename,attr"`
	Lines    []coberturaLine `xml:"lines>line"`
}

type coberturaLine struct {
	Number int  `xml:"number,attr"`
	Hits   int  `xml:"hits,attr"`
	Branch bool `xml:"branch,attr"`
	// condition-coverage like "50% (1/2)"
	ConditionCoverage string `xml:"condition-coverage,attr"`
}

func looksLikeCobertura(data []byte) bool {
	head := sniffHead(data)
	return strings.Contains(head, "<coverage") && strings.Contains(head, "<packages")
}

// ParseCobertura parses a Cobertura XML document into per-file coverage.
func ParseCobertura(data []byte, language string) ([]model.FileCoverage, error) {
	var root coberturaRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	files := map[string]*model.FileCoverage{}
	var order []string
	for _, p := range root.Packages {
		for _, c := range p.Classes {
			fc, ok := files[c.Filename]
			if !ok {
				fc = &model.FileCoverage{Path: normalizePath(c.Filename), Language: language}
				files[c.Filename] = fc
				order = append(order, c.Filename)
			}
			for _, ln := range c.Lines {
				fc.LineHits = append(fc.LineHits, model.LineHit{Line: ln.Number, Hits: ln.Hits})
				fc.Lines.Total++
				if ln.Hits > 0 {
					fc.Lines.Covered++
				}
				if cov, tot, ok := parseConditionCoverage(ln.ConditionCoverage); ok {
					fc.Branches.Covered += cov
					fc.Branches.Total += tot
				}
			}
		}
	}
	out := make([]model.FileCoverage, 0, len(order))
	for _, k := range order {
		out = append(out, *files[k])
	}
	return out, nil
}

// parseConditionCoverage extracts "(covered/total)" from strings like
// "50% (1/2)".
func parseConditionCoverage(s string) (covered, total int, ok bool) {
	open := strings.IndexByte(s, '(')
	close := strings.IndexByte(s, ')')
	if open < 0 || close < open {
		return 0, 0, false
	}
	frac := s[open+1 : close]
	slash := strings.IndexByte(frac, '/')
	if slash < 0 {
		return 0, 0, false
	}
	covered = atoiSafe(strings.TrimSpace(frac[:slash]))
	total = atoiSafe(strings.TrimSpace(frac[slash+1:]))
	return covered, total, total > 0
}
