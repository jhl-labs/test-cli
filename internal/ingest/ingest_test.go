package ingest

import (
	"testing"

	"github.com/jhl-labs/test-cli/internal/model"
)

func TestDetectAndParseJUnit(t *testing.T) {
	data := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="math" time="0.5">
    <testcase name="adds" classname="MathTest" time="0.1"/>
    <testcase name="divides" classname="MathTest" time="0.2">
      <failure message="want 2 got 3">assert 3 == 2</failure>
    </testcase>
    <testcase name="todo" classname="MathTest"><skipped message="later"/></testcase>
  </testsuite>
</testsuites>`)

	if got := DetectTestFormat(data); got != FormatJUnit {
		t.Fatalf("DetectTestFormat = %q, want junit", got)
	}
	suites, err := ParseJUnit(data, "python")
	if err != nil {
		t.Fatalf("ParseJUnit: %v", err)
	}
	if len(suites) != 1 {
		t.Fatalf("got %d suites, want 1", len(suites))
	}
	s := suites[0]
	if s.Language != "python" {
		t.Errorf("language = %q", s.Language)
	}
	if len(s.Cases) != 3 {
		t.Fatalf("got %d cases, want 3", len(s.Cases))
	}
	statuses := map[string]string{}
	for _, c := range s.Cases {
		statuses[c.Name] = c.Status
	}
	if statuses["adds"] != model.StatusPassed {
		t.Errorf("adds = %q", statuses["adds"])
	}
	if statuses["divides"] != model.StatusFailed {
		t.Errorf("divides = %q", statuses["divides"])
	}
	if statuses["todo"] != model.StatusSkipped {
		t.Errorf("todo = %q", statuses["todo"])
	}
}

func TestParseJUnitBareSuite(t *testing.T) {
	data := []byte(`<testsuite name="solo"><testcase name="ok" time="0.01"/></testsuite>`)
	suites, err := ParseJUnit(data, "java")
	if err != nil {
		t.Fatalf("ParseJUnit: %v", err)
	}
	if len(suites) != 1 || len(suites[0].Cases) != 1 {
		t.Fatalf("unexpected suites: %+v", suites)
	}
}

func TestParseJUnitCasesDirectlyUnderSuites(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="utf-8"?>
<testsuites>
  <testcase name="extractClientInfo should extract x-real-ip" time="0.000914" classname="test"/>
  <testcase name="failing case" time="0.000202" classname="test">
    <failure message="want true got false">assertion failed</failure>
  </testcase>
</testsuites>`)

	suites, err := ParseJUnit(data, "typescript")
	if err != nil {
		t.Fatalf("ParseJUnit: %v", err)
	}
	if len(suites) != 1 {
		t.Fatalf("got %d suites, want 1", len(suites))
	}
	if suites[0].Name != "tests" {
		t.Errorf("suite name = %q, want tests", suites[0].Name)
	}
	if len(suites[0].Cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(suites[0].Cases))
	}
	if suites[0].Cases[0].Status != model.StatusPassed {
		t.Errorf("first status = %q, want passed", suites[0].Cases[0].Status)
	}
	if suites[0].Cases[1].Status != model.StatusFailed {
		t.Errorf("second status = %q, want failed", suites[0].Cases[1].Status)
	}
}

func TestParseGoJSON(t *testing.T) {
	data := []byte(`{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"output","Package":"p","Test":"TestA","Output":"ok\n"}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0.01}
{"Action":"run","Package":"p","Test":"TestB"}
{"Action":"output","Package":"p","Test":"TestB","Output":"boom\n"}
{"Action":"fail","Package":"p","Test":"TestB","Elapsed":0.02}`)

	if got := DetectTestFormat(data); got != FormatGoJSON {
		t.Fatalf("DetectTestFormat = %q, want go-json", got)
	}
	suites, err := ParseGoJSON(data)
	if err != nil {
		t.Fatalf("ParseGoJSON: %v", err)
	}
	if len(suites) != 1 {
		t.Fatalf("got %d suites", len(suites))
	}
	if len(suites[0].Cases) != 2 {
		t.Fatalf("got %d cases", len(suites[0].Cases))
	}
	var fail *model.TestCase
	for i := range suites[0].Cases {
		if suites[0].Cases[i].Name == "TestB" {
			fail = &suites[0].Cases[i]
		}
	}
	if fail == nil || fail.Status != model.StatusFailed {
		t.Fatalf("TestB not failed: %+v", fail)
	}
	if fail.Message != "boom" {
		t.Errorf("TestB message = %q, want boom", fail.Message)
	}
}

func TestParseCobertura(t *testing.T) {
	data := []byte(`<?xml version="1.0"?>
<coverage>
  <packages>
    <package name="p">
      <classes>
        <class filename="src/a.py">
          <lines>
            <line number="1" hits="3"/>
            <line number="2" hits="0"/>
            <line number="3" hits="1" branch="true" condition-coverage="50% (1/2)"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`)

	if got := DetectCoverageFormat(data); got != FormatCobertura {
		t.Fatalf("DetectCoverageFormat = %q, want cobertura", got)
	}
	files, err := ParseCobertura(data, "python")
	if err != nil {
		t.Fatalf("ParseCobertura: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files", len(files))
	}
	f := files[0]
	if f.Path != "src/a.py" {
		t.Errorf("path = %q", f.Path)
	}
	if f.Lines.Total != 3 || f.Lines.Covered != 2 {
		t.Errorf("lines = %d/%d, want 2/3", f.Lines.Covered, f.Lines.Total)
	}
	if f.Branches.Total != 2 || f.Branches.Covered != 1 {
		t.Errorf("branches = %d/%d, want 1/2", f.Branches.Covered, f.Branches.Total)
	}
}

func TestParseLCOV(t *testing.T) {
	data := []byte(`TN:
SF:src/index.ts
DA:1,5
DA:2,0
BRDA:2,0,0,1
BRDA:2,0,1,-
end_of_record
`)
	if got := DetectCoverageFormat(data); got != FormatLCOV {
		t.Fatalf("DetectCoverageFormat = %q, want lcov", got)
	}
	files, err := ParseLCOV(data, "typescript")
	if err != nil {
		t.Fatalf("ParseLCOV: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files", len(files))
	}
	f := files[0]
	if f.Lines.Covered != 1 || f.Lines.Total != 2 {
		t.Errorf("lines = %d/%d", f.Lines.Covered, f.Lines.Total)
	}
	if f.Branches.Total != 2 || f.Branches.Covered != 1 {
		t.Errorf("branches = %d/%d", f.Branches.Covered, f.Branches.Total)
	}
}

func TestParseGoCover(t *testing.T) {
	data := []byte(`mode: set
github.com/x/y/a.go:10.20,12.2 2 1
github.com/x/y/a.go:14.2,15.10 1 0
`)
	if got := DetectCoverageFormat(data); got != FormatGoCover {
		t.Fatalf("DetectCoverageFormat = %q, want go-cover", got)
	}
	files, err := ParseGoCover(data, "go")
	if err != nil {
		t.Fatalf("ParseGoCover: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files", len(files))
	}
	// Lines 10,11,12 covered (count 1); 14,15 not (count 0) => 3 covered of 5.
	f := files[0]
	if f.Lines.Total != 5 || f.Lines.Covered != 3 {
		t.Errorf("lines = %d/%d, want 3/5", f.Lines.Covered, f.Lines.Total)
	}
}

func TestParseJaCoCo(t *testing.T) {
	data := []byte(`<?xml version="1.0"?>
<!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd">
<report name="app">
  <package name="com/x">
    <sourcefile name="App.java">
      <line nr="3" mi="0" ci="4" mb="0" cb="2"/>
      <line nr="4" mi="2" ci="0" mb="2" cb="0"/>
    </sourcefile>
  </package>
</report>`)
	if got := DetectCoverageFormat(data); got != FormatJaCoCo {
		t.Fatalf("DetectCoverageFormat = %q, want jacoco", got)
	}
	files, err := ParseJaCoCo(data, "java")
	if err != nil {
		t.Fatalf("ParseJaCoCo: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files", len(files))
	}
	f := files[0]
	if f.Path != "com/x/App.java" {
		t.Errorf("path = %q", f.Path)
	}
	if f.Lines.Covered != 1 || f.Lines.Total != 2 {
		t.Errorf("lines = %d/%d", f.Lines.Covered, f.Lines.Total)
	}
	if f.Branches.Covered != 2 || f.Branches.Total != 4 {
		t.Errorf("branches = %d/%d", f.Branches.Covered, f.Branches.Total)
	}
}
