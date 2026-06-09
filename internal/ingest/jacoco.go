package ingest

import (
	"encoding/xml"
	"path"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// JaCoCo is the standard Java/Kotlin coverage format (maven jacoco plugin,
// gradle jacocoTestReport). Its XML model is package > sourcefile > line.
type jacocoReport struct {
	XMLName  xml.Name        `xml:"report"`
	Packages []jacocoPackage `xml:"package"`
}

type jacocoPackage struct {
	Name        string             `xml:"name,attr"`
	SourceFiles []jacocoSourceFile `xml:"sourcefile"`
}

type jacocoSourceFile struct {
	Name  string       `xml:"name,attr"`
	Lines []jacocoLine `xml:"line"`
}

type jacocoLine struct {
	Nr int `xml:"nr,attr"` // line number
	CI int `xml:"ci,attr"` // covered instructions
	MI int `xml:"mi,attr"` // missed instructions
	CB int `xml:"cb,attr"` // covered branches
	MB int `xml:"mb,attr"` // missed branches
}

func looksLikeJaCoCo(data []byte) bool {
	head := strings.ToLower(sniffHead(data))
	return strings.Contains(head, "<report") &&
		(strings.Contains(head, "jacoco") || strings.Contains(head, "<sourcefile"))
}

// ParseJaCoCo parses a JaCoCo XML report into per-file coverage. A line is
// considered covered when it has at least one covered instruction.
func ParseJaCoCo(data []byte, language string) ([]model.FileCoverage, error) {
	var report jacocoReport
	if err := xml.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	var out []model.FileCoverage
	for _, p := range report.Packages {
		for _, sf := range p.SourceFiles {
			full := sf.Name
			if p.Name != "" {
				full = path.Join(p.Name, sf.Name)
			}
			fc := model.FileCoverage{Path: normalizePath(full), Language: language}
			for _, ln := range sf.Lines {
				hits := 0
				if ln.CI > 0 {
					hits = 1
				}
				fc.LineHits = append(fc.LineHits, model.LineHit{Line: ln.Nr, Hits: hits})
				fc.Lines.Total++
				if hits > 0 {
					fc.Lines.Covered++
				}
				fc.Branches.Total += ln.CB + ln.MB
				fc.Branches.Covered += ln.CB
			}
			out = append(out, fc)
		}
	}
	return out, nil
}
