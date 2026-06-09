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
