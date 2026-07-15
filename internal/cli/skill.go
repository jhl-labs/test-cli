package cli

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/jhl-labs/test-cli/internal/model"
	"github.com/jhl-labs/test-cli/internal/version"
)

//go:embed templates/skill.md.tmpl
var skillFS embed.FS

var skillTmpl = template.Must(template.ParseFS(skillFS, "templates/skill.md.tmpl"))

// runGenerateSkill writes an AI-agent skill (SKILL.md) describing how to drive
// test-cli. The default layout (.claude/skills/<name>/SKILL.md) is directly
// consumable by Claude Code and compatible agents.
func runGenerateSkill(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-skill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", ".claude/skills", "directory to write the skill into")
	name := fs.String("name", "test-runner", "skill name (kebab-case)")
	title := fs.String("title", "Run tests & coverage with test-cli", "human-readable skill title")
	stdoutOnly := fs.Bool("stdout", false, "print the skill to stdout instead of writing files")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "test-cli generate-skill: unexpected positional argument %q\n", fs.Arg(0))
		return ExitUsage
	}
	if !validSkillName(*name) {
		fmt.Fprintln(stderr, "test-cli generate-skill: --name must be 1-64 lowercase letters, digits, or single hyphens")
		return ExitUsage
	}
	if strings.TrimSpace(*title) == "" || strings.ContainsAny(*title, "\r\n") {
		fmt.Fprintln(stderr, "test-cli generate-skill: --title must be a non-empty single line")
		return ExitUsage
	}

	data := struct {
		Name        string
		Title       string
		Schema      string
		ToolVersion string
	}{
		Name:        *name,
		Title:       *title,
		Schema:      model.SchemaID,
		ToolVersion: version.Long(),
	}

	var buf strings.Builder
	if err := skillTmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}

	if *stdoutOnly {
		fmt.Fprint(stdout, buf.String())
		return ExitOK
	}

	dir := filepath.Join(*out, *name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		fmt.Fprintf(stderr, "test-cli: %v\n", err)
		return ExitRunFailure
	}
	fmt.Fprintf(stdout, "wrote %s\n", path)
	return ExitOK
}

func validSkillName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, r := range name {
		isHyphen := r == '-'
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !isHyphen {
			return false
		}
		if isHyphen && previousHyphen {
			return false
		}
		previousHyphen = isHyphen
	}
	return true
}
