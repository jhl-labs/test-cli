package ingest

import (
	"encoding/xml"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// junitSuites mirrors the <testsuites> wrapper element. Many tools emit a bare
// <testsuite> root instead, which we handle by also trying junitSuite directly.
type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Name    string       `xml:"name,attr"`
	Time    float64      `xml:"time,attr"`
	Cases   []junitCase  `xml:"testcase"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	XMLName xml.Name     `xml:"testsuite"`
	Name    string       `xml:"name,attr"`
	Time    float64      `xml:"time,attr"`
	File    string       `xml:"file,attr"`
	Cases   []junitCase  `xml:"testcase"`
	Nested  []junitSuite `xml:"testsuite"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	File      string        `xml:"file,attr"`
	Time      float64       `xml:"time,attr"`
	Failures  []junitDetail `xml:"failure"`
	Errors    []junitDetail `xml:"error"`
	Skipped   *junitDetail  `xml:"skipped"`
}

type junitDetail struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

// looksLikeJUnit reports whether the document root is a JUnit XML element.
func looksLikeJUnit(data []byte) bool {
	head := sniffHead(data)
	return strings.Contains(head, "<testsuite")
}

// ParseJUnit parses JUnit-style XML (the de-facto standard emitted by pytest,
// jest-junit, dotnet junit logger, surefire, and cargo-nextest) into normalized
// suites. The language label is attached to every suite for grouping.
func ParseJUnit(data []byte, language string) ([]model.TestSuite, error) {
	var suites []junitSuite
	var root junitSuites
	if err := xml.Unmarshal(data, &root); err == nil {
		if len(root.Cases) > 0 {
			suites = append(suites, junitSuite{
				Name:  firstNonEmpty(root.Name, "tests"),
				Time:  root.Time,
				Cases: root.Cases,
			})
		}
		suites = append(suites, root.Suites...)
		if root.XMLName.Local == "testsuites" && len(suites) == 0 {
			return nil, nil
		}
	} else {
		// Fall back to a bare <testsuite> root (many tools emit this).
		var single junitSuite
		if err := xml.Unmarshal(data, &single); err != nil {
			return nil, err
		}
		if single.Name != "" || len(single.Cases) > 0 || len(single.Nested) > 0 {
			suites = []junitSuite{single}
		}
	}

	var out []model.TestSuite
	var walk func(s junitSuite)
	walk = func(s junitSuite) {
		if len(s.Cases) > 0 {
			out = append(out, convertJUnitSuite(s, language))
		}
		for _, n := range s.Nested {
			walk(n)
		}
	}
	for _, s := range suites {
		walk(s)
	}
	return out, nil
}

func convertJUnitSuite(s junitSuite, language string) model.TestSuite {
	suite := model.TestSuite{
		Name:       firstNonEmpty(s.Name, "tests"),
		Language:   language,
		File:       s.File,
		DurationMs: s.Time * 1000,
	}
	for _, c := range s.Cases {
		tc := model.TestCase{
			Name:       c.Name,
			Classname:  c.Classname,
			Status:     model.StatusPassed,
			DurationMs: c.Time * 1000,
		}
		switch {
		case len(c.Errors) > 0:
			tc.Status = model.StatusError
			tc.Message, tc.Detail = detailOf(c.Errors[0])
		case len(c.Failures) > 0:
			tc.Status = model.StatusFailed
			tc.Message, tc.Detail = detailOf(c.Failures[0])
		case c.Skipped != nil:
			tc.Status = model.StatusSkipped
			tc.Message, tc.Detail = detailOf(*c.Skipped)
		}
		if suite.File == "" && c.File != "" {
			suite.File = c.File
		}
		suite.Cases = append(suite.Cases, tc)
	}
	return suite
}

func detailOf(d junitDetail) (msg, detail string) {
	msg = strings.TrimSpace(d.Message)
	detail = strings.TrimSpace(d.Text)
	if msg == "" {
		// Use the first line of the body as the message.
		if i := strings.IndexByte(detail, '\n'); i > 0 {
			msg = strings.TrimSpace(detail[:i])
		} else {
			msg = detail
		}
	}
	return msg, detail
}
