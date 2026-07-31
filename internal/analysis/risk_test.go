package analysis

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jhl-labs/test-cli/internal/model"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goBody = "package p\n\nfunc F(a int) int {\n\tif a > 0 {\n\t\treturn a\n\t}\n\treturn 0\n}\n"

func TestAnalyzeRiskRanksChurnAndCoverage(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	gitRun(t, root, "init", "-q")
	writeFile(t, filepath.Join(root, "hot.go"), goBody)
	writeFile(t, filepath.Join(root, "cold.go"), goBody)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "c1")
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(root, "hot.go"), goBody+"\n// rev\n")
		gitRun(t, root, "commit", "-aqm", "churn")
		writeFile(t, filepath.Join(root, "hot.go"), goBody)
		gitRun(t, root, "commit", "-aqm", "churn back")
	}
	r := &model.Report{Coverage: model.CoverageReport{Files: []model.FileCoverage{
		{Path: "cold.go", Lines: model.Metric{Covered: 8, Total: 8, Pct: 100}},
	}}}
	_, stats, ok := scanProject(root)
	if !ok {
		t.Fatal("scanProject failed")
	}
	risk := analyzeRisk(r, root, stats)
	if risk == nil || len(risk.Files) != 2 {
		t.Fatalf("risk = %+v", risk)
	}
	if risk.Files[0].Path != "hot.go" {
		t.Errorf("expected hot.go first, got %+v", risk.Files)
	}
	if risk.Files[0].RiskScore <= risk.Files[1].RiskScore {
		t.Errorf("scores not ordered: %+v", risk.Files)
	}
	for _, f := range risk.Files {
		if f.Path == "cold.go" && f.RiskScore > 0.05 {
			t.Errorf("fully covered file should score near zero: %+v", f)
		}
	}
}

func TestAnalyzeRiskNilWithoutGit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), goBody)
	_, stats, _ := scanProject(root)
	if risk := analyzeRisk(&model.Report{}, root, stats); risk != nil {
		t.Errorf("expected nil risk outside git repo, got %+v", risk)
	}
}
