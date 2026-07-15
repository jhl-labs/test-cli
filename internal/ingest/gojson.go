package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jhl-labs/test-cli/internal/model"
)

// goTestEvent is one line of `go test -json` (test2json) output.
type goTestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"`
	Output  string    `json:"Output"`
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
	terminal := map[caseKey]bool{}
	order := map[string][]string{} // pkg -> ordered test names
	seen := map[caseKey]bool{}
	started := map[caseKey]time.Time{}
	packageOutput := map[string]*strings.Builder{}
	packageFailed := map[string]bool{}
	var pkgOrder []string
	pkgSeen := map[string]bool{}
	malformed := 0

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev goTestEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			malformed++
			continue
		}
		if ev.Package != "" && !pkgSeen[ev.Package] {
			pkgSeen[ev.Package] = true
			pkgOrder = append(pkgOrder, ev.Package)
			packageOutput[ev.Package] = &strings.Builder{}
		}
		if ev.Test == "" {
			switch ev.Action {
			case "output":
				if packageOutput[ev.Package] != nil {
					packageOutput[ev.Package].WriteString(ev.Output)
				}
			case "fail":
				packageFailed[ev.Package] = true
			}
			continue
		}
		key := caseKey{ev.Package, ev.Test}
		if !seen[key] {
			seen[key] = true
			order[ev.Package] = append(order[ev.Package], ev.Test)
			cases[key] = &model.TestCase{Name: ev.Test, Classname: ev.Package, Status: model.StatusError}
			output[key] = &strings.Builder{}
		}
		switch ev.Action {
		case "run":
			if !ev.Time.IsZero() {
				started[key] = ev.Time
			}
		case "output":
			output[key].WriteString(ev.Output)
		case "pass":
			cases[key].Status = model.StatusPassed
			cases[key].DurationMs = eventDurationMs(ev, started[key])
			terminal[key] = true
		case "fail":
			cases[key].Status = model.StatusFailed
			cases[key].DurationMs = eventDurationMs(ev, started[key])
			terminal[key] = true
		case "skip":
			cases[key].Status = model.StatusSkipped
			cases[key].DurationMs = eventDurationMs(ev, started[key])
			terminal[key] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if malformed > 0 {
		return nil, fmt.Errorf("go test JSON contains %d malformed event(s)", malformed)
	}

	var suites []model.TestSuite
	for _, pkg := range pkgOrder {
		suite := model.TestSuite{Name: pkg, Language: "go"}
		hasFailure := false
		for _, test := range order[pkg] {
			key := caseKey{pkg, test}
			c := cases[key]
			if !terminal[key] {
				c.Status = model.StatusError
				c.Message = "test did not complete"
			}
			if c.Status != model.StatusPassed {
				detail := strings.TrimSpace(output[key].String())
				c.Detail = detail
				if c.Message == "" {
					c.Message = firstLine(detail)
				}
			}
			if c.Status == model.StatusFailed || c.Status == model.StatusError {
				hasFailure = true
			}
			suite.Cases = append(suite.Cases, *c)
		}
		if packageFailed[pkg] && !hasFailure {
			detail := strings.TrimSpace(packageOutput[pkg].String())
			message := firstLine(detail)
			if message == "" || message == "FAIL" {
				message = "package test process failed"
			}
			suite.Cases = append(suite.Cases, model.TestCase{
				Name: "[package]", Classname: pkg, Status: model.StatusError,
				Message: message, Detail: detail,
			})
		}
		if len(suite.Cases) > 0 {
			suites = append(suites, suite)
		}
	}
	return suites, nil
}

func eventDurationMs(event goTestEvent, started time.Time) float64 {
	if event.Elapsed > 0 {
		return event.Elapsed * 1000
	}
	if !started.IsZero() && !event.Time.IsZero() && event.Time.After(started) {
		return float64(event.Time.Sub(started).Microseconds()) / 1000
	}
	return 0
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
