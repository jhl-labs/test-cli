package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// goTestEvent is one line of `go test -json` (test2json) output.
type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// looksLikeGoJSON reports whether data is a stream of go test -json events.
func looksLikeGoJSON(data []byte) bool {
	head := bytes.TrimSpace(data)
	if len(head) == 0 || head[0] != '{' {
		return false
	}
	return bytes.Contains(head[:min(len(head), 200)], []byte(`"Action"`))
}

// ParseGoJSON converts `go test -json` output into normalized suites, one suite
// per package. Sub-test output is captured as the failing case's detail.
func ParseGoJSON(data []byte) ([]model.TestSuite, error) {
	type caseKey struct{ pkg, test string }
	cases := map[caseKey]*model.TestCase{}
	output := map[caseKey]*strings.Builder{}
	order := map[string][]string{} // pkg -> ordered test names
	seen := map[caseKey]bool{}
	var pkgOrder []string
	pkgSeen := map[string]bool{}

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev goTestEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Test == "" {
			continue // package-level event
		}
		key := caseKey{ev.Package, ev.Test}
		if !pkgSeen[ev.Package] {
			pkgSeen[ev.Package] = true
			pkgOrder = append(pkgOrder, ev.Package)
		}
		if !seen[key] {
			seen[key] = true
			order[ev.Package] = append(order[ev.Package], ev.Test)
			cases[key] = &model.TestCase{Name: ev.Test, Classname: ev.Package, Status: model.StatusPassed}
			output[key] = &strings.Builder{}
		}
		switch ev.Action {
		case "output":
			output[key].WriteString(ev.Output)
		case "pass":
			cases[key].Status = model.StatusPassed
			cases[key].DurationMs = ev.Elapsed * 1000
		case "fail":
			cases[key].Status = model.StatusFailed
			cases[key].DurationMs = ev.Elapsed * 1000
		case "skip":
			cases[key].Status = model.StatusSkipped
			cases[key].DurationMs = ev.Elapsed * 1000
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var suites []model.TestSuite
	for _, pkg := range pkgOrder {
		suite := model.TestSuite{Name: pkg, Language: "go"}
		for _, test := range order[pkg] {
			key := caseKey{pkg, test}
			c := cases[key]
			if c.Status != model.StatusPassed {
				detail := strings.TrimSpace(output[key].String())
				c.Detail = detail
				c.Message = firstLine(detail)
			}
			suite.Cases = append(suite.Cases, *c)
		}
		suites = append(suites, suite)
	}
	return suites, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
