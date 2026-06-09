package report

import (
	"encoding/xml"

	"github.com/jhl-labs/test-cli/internal/model"
)

// --- JUnit emission (standardized, aggregated across all languages) ---

type xmlSuites struct {
	XMLName  xml.Name   `xml:"testsuites"`
	Name     string     `xml:"name,attr"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Skipped  int        `xml:"skipped,attr"`
	Time     float64    `xml:"time,attr"`
	Suites   []xmlSuite `xml:"testsuite"`
}

type xmlSuite struct {
	Name     string    `xml:"name,attr"`
	Tests    int       `xml:"tests,attr"`
	Failures int       `xml:"failures,attr"`
	Errors   int       `xml:"errors,attr"`
	Skipped  int       `xml:"skipped,attr"`
	Time     float64   `xml:"time,attr"`
	Lang     string    `xml:"language,attr,omitempty"`
	Cases    []xmlCase `xml:"testcase"`
}

type xmlCase struct {
	Name      string      `xml:"name,attr"`
	Classname string      `xml:"classname,attr,omitempty"`
	Time      float64     `xml:"time,attr"`
	Failure   *xmlDetail  `xml:"failure,omitempty"`
	Error     *xmlDetail  `xml:"error,omitempty"`
	Skipped   *xmlSkipped `xml:"skipped,omitempty"`
}

type xmlDetail struct {
	Message string `xml:"message,attr,omitempty"`
	Text    string `xml:",chardata"`
}

type xmlSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

func writeJUnit(r *model.Report, path string) error {
	doc := xmlSuites{
		Name:     "test-cli",
		Tests:    r.Test.Summary.Total,
		Failures: r.Test.Summary.Failed,
		Errors:   r.Test.Summary.Errors,
		Skipped:  r.Test.Summary.Skipped,
		Time:     r.Test.Summary.DurationMs / 1000,
	}
	for _, s := range r.Test.Suites {
		xs := xmlSuite{
			Name:     s.Name,
			Tests:    s.Summary.Total,
			Failures: s.Summary.Failed,
			Errors:   s.Summary.Errors,
			Skipped:  s.Summary.Skipped,
			Time:     s.DurationMs / 1000,
			Lang:     s.Language,
		}
		for _, c := range s.Cases {
			xc := xmlCase{Name: c.Name, Classname: c.Classname, Time: c.DurationMs / 1000}
			switch c.Status {
			case model.StatusFailed:
				xc.Failure = &xmlDetail{Message: c.Message, Text: c.Detail}
			case model.StatusError:
				xc.Error = &xmlDetail{Message: c.Message, Text: c.Detail}
			case model.StatusSkipped:
				xc.Skipped = &xmlSkipped{Message: c.Message}
			}
			xs.Cases = append(xs.Cases, xc)
		}
		doc.Suites = append(doc.Suites, xs)
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, xml.Header+string(data)+"\n")
}

// --- Cobertura emission (standardized, aggregated coverage) ---

type xmlCoverage struct {
	XMLName      xml.Name    `xml:"coverage"`
	LineRate     float64     `xml:"line-rate,attr"`
	BranchRate   float64     `xml:"branch-rate,attr"`
	LinesCovered int         `xml:"lines-covered,attr"`
	LinesValid   int         `xml:"lines-valid,attr"`
	Version      string      `xml:"version,attr"`
	Timestamp    int64       `xml:"timestamp,attr"`
	Sources      []string    `xml:"sources>source"`
	Packages     []xmlCovPkg `xml:"packages>package"`
}

type xmlCovPkg struct {
	Name       string        `xml:"name,attr"`
	LineRate   float64       `xml:"line-rate,attr"`
	BranchRate float64       `xml:"branch-rate,attr"`
	Classes    []xmlCovClass `xml:"classes>class"`
}

type xmlCovClass struct {
	Name       string       `xml:"name,attr"`
	Filename   string       `xml:"filename,attr"`
	LineRate   float64      `xml:"line-rate,attr"`
	BranchRate float64      `xml:"branch-rate,attr"`
	Lines      []xmlCovLine `xml:"lines>line"`
}

type xmlCovLine struct {
	Number int `xml:"number,attr"`
	Hits   int `xml:"hits,attr"`
}

func writeCobertura(r *model.Report, path string) error {
	doc := xmlCoverage{
		LineRate:     r.Coverage.Summary.Lines.Pct / 100,
		BranchRate:   r.Coverage.Summary.Branches.Pct / 100,
		LinesCovered: r.Coverage.Summary.Lines.Covered,
		LinesValid:   r.Coverage.Summary.Lines.Total,
		Version:      "test-cli",
		Sources:      []string{r.Root},
	}
	pkg := xmlCovPkg{
		Name:       "all",
		LineRate:   r.Coverage.Summary.Lines.Pct / 100,
		BranchRate: r.Coverage.Summary.Branches.Pct / 100,
	}
	for _, f := range r.Coverage.Files {
		cls := xmlCovClass{
			Name:       f.Path,
			Filename:   f.Path,
			LineRate:   f.Lines.Pct / 100,
			BranchRate: f.Branches.Pct / 100,
		}
		for _, lh := range f.LineHits {
			if lh.Hits < 0 {
				continue
			}
			cls.Lines = append(cls.Lines, xmlCovLine{Number: lh.Line, Hits: lh.Hits})
		}
		pkg.Classes = append(pkg.Classes, cls)
	}
	doc.Packages = []xmlCovPkg{pkg}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	header := xml.Header + `<!DOCTYPE coverage SYSTEM "http://cobertura.sourceforge.net/xml/coverage-04.dtd">` + "\n"
	return writeFile(path, header+string(data)+"\n")
}
