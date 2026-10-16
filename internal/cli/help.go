package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/targetrun"
)

// `help` is a reserved grammar keyword in two positions: `smith <ns> <name>
// help` shows type help (rendering the type's help/project.md) and
// `smith <ns> <name> <target> help` shows target help (rendering
// help/<target>.md and printing the target's rendered steps). Both shadow
// projects or targets literally named "help". The repo attachments get the
// same forms through dispatchRepo.

// lookPath resolves display tools (glow, less, more); it is a variable so
// tests can substitute a fake.
var lookPath = exec.LookPath

// helpContext is everything needed to compose one help document: the
// project, its custody-verified composed types, the absolute repo root, the
// merged params a real run would use, and the invocation's "--" passthrough
// ("" when absent). Per-type blocks bind SMITH_TYPE_DIR to that type's
// installed dir (the owning type's dir is what a step would see).
type helpContext struct {
	project  discovery.Project
	resolved *targetrun.Resolved
	absRoot  string
	params   map[string]string // merged params (all types, disjoint by composition)
	cliArgs  string
}

func (rc *repoContext) helpContext(p discovery.Project) (*helpContext, error) {
	rt, err := rc.typeFor(p)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(rc.repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving repo root: %w", err)
	}
	params, err := targetrun.ResolveParams(absRoot, p, rt, rc.cliArgs)
	if err != nil {
		return nil, err
	}
	return &helpContext{project: p, resolved: rt, absRoot: absRoot, params: params, cliArgs: rc.cliArgs}, nil
}

// varsFor returns the rendering context of one type's block: the merged
// params plus the computed vars (including the "--" passthrough) with
// SMITH_TYPE_DIR bound to that type's installed dir.
func (hc *helpContext) varsFor(t *projecttype.ProjectType) map[string]string {
	vars := targetrun.ComputedVars(hc.absRoot, hc.project.Dir, hc.project.Manifest.Name, hc.resolved.Dirs[refOf(t)], hc.cliArgs)
	for key, value := range hc.params {
		vars[key] = value
	}
	return vars
}

// showTypeHelp composes and displays the type-level help document: one
// generated block per type (identity, working_dir, tools, that type's params
// in the vars table, that type's environment, then the type's
// help/project.md — a note replaces the file when the type ships none),
// blocks joined with "---". A single-type project renders exactly one block.
func (rc *repoContext) showTypeHelp(cmd *cobra.Command, p discovery.Project) error {
	hc, err := rc.helpContext(p)
	if err != nil {
		return err
	}
	var doc bytes.Buffer
	for i, t := range hc.resolved.Types {
		if i > 0 {
			doc.WriteString("\n---\n\n")
		}
		hc.writeTypeBlock(&doc, t)
	}
	return displayMarkdown(cmd, doc.String())
}

// writeTypeBlock renders one type's generated summary and appends its
// help/project.md.
func (hc *helpContext) writeTypeBlock(doc *bytes.Buffer, t *projecttype.ProjectType) {
	p := hc.project
	vars := hc.varsFor(t)
	fmt.Fprintf(doc, "# %s (type %s@%d)\n\n", p.Manifest.Name, t.Name, t.Version)
	if t.Description != "" {
		fmt.Fprintf(doc, "%s\n\n", t.Description)
	}
	workingDir := string(t.WorkingDir)
	if t.WorkingDir == "" {
		workingDir = string(projecttype.WorkingDirProject)
	}
	if m := p.Manifest.WorkingDir; m != "" {
		fmt.Fprintf(doc, "- working_dir: `%s` (manifest overrides the type's `%s`)\n", m, workingDir)
	} else {
		fmt.Fprintf(doc, "- working_dir: `%s`\n", workingDir)
	}
	fmt.Fprintf(doc, "- tools: %s\n\n", strings.Join(t.Tools, ", "))

	doc.WriteString("## Template vars\n\n")
	doc.WriteString(markdownVarsTable(vars, t))

	if len(t.Environment) > 0 {
		doc.WriteString("\n## Environment\n\n```sh\n")
		for _, key := range sortedStringKeys(t.Environment) {
			value, err := projecttype.RenderTemplate(t.Environment[key], vars)
			if err != nil {
				value = t.Environment[key]
			}
			fmt.Fprintf(doc, "%s=%s\n", key, value)
		}
		doc.WriteString("```\n")
	}

	doc.WriteString("\n---\n\n")
	doc.WriteString(helpFile(hc.resolved.Dirs[refOf(t)], "project"))
}

// showTargetHelp composes and displays the target-level help document: the
// target's rendered steps (the command that actually runs), the vars those
// steps reference, then the OWNING type's help/<target>.md (a note replaces
// the file when the type ships none). The header names the owning type's ref
// when the project composes several types.
func (rc *repoContext) showTargetHelp(cmd *cobra.Command, p discovery.Project, targetName string) error {
	hc, err := rc.helpContext(p)
	if err != nil {
		return err
	}
	rt := hc.resolved
	owner, ok := rt.ByTarget[targetName]
	if !ok {
		return fmt.Errorf("project %q (%s) has no target %q (targets: %s)",
			p.Manifest.Name, rt.TypeLabel(), targetName, strings.Join(sortedKeys(rt.ByTarget), ", "))
	}
	target := owner.Targets[targetName]
	vars := hc.varsFor(owner)

	var doc bytes.Buffer
	fmt.Fprintf(&doc, "# %s:%s", p.Manifest.Name, targetName)
	if len(rt.Types) > 1 {
		fmt.Fprintf(&doc, " (type %s@%d)", owner.Name, owner.Version)
	}
	doc.WriteString("\n\n")
	if target.Description != "" {
		fmt.Fprintf(&doc, "%s\n\n", target.Description)
	}

	doc.WriteString("## Steps\n\n```sh\n")
	referenced := map[string]bool{}
	for _, step := range target.Steps {
		for _, name := range templateRefs(step) {
			referenced[name] = true
		}
		rendered, err := projecttype.RenderTemplate(step, vars)
		if err != nil {
			return fmt.Errorf("project %q target %q: rendering step: %w", p.Manifest.Name, targetName, err)
		}
		fmt.Fprintf(&doc, "%s\n", rendered)
	}
	doc.WriteString("```\n")

	var used []string
	for name := range referenced {
		used = append(used, name)
	}
	sort.Strings(used)
	if len(used) > 0 {
		doc.WriteString("\n## Vars used\n\n")
		for _, name := range used {
			value, ok := vars[name]
			if !ok {
				value = "<unset>"
			}
			fmt.Fprintf(&doc, "- `%s` = %s\n", name, value)
		}
	}

	doc.WriteString("\n---\n\n")
	doc.WriteString(helpFile(rt.Dirs[refOf(owner)], targetName))
	return displayMarkdown(cmd, doc.String())
}

// helpFile returns the contents of one help/<name>.md shipped inside the
// installed type directory typeDir, or a note when the type carries no such
// file — the generated content above always stands alone.
func helpFile(typeDir, name string) string {
	path := filepath.Join(typeDir, "help", name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("_This type ships no help/%s.md — the summary above is the help._\n", name)
	}
	return strings.TrimRight(string(data), "\n") + "\n"
}

// markdownVarsTable renders the rendering context as a markdown table: type
// params (with descriptions) first, then the computed SMITH_* vars.
func markdownVarsTable(vars map[string]string, t *projecttype.ProjectType) string {
	var b strings.Builder
	b.WriteString("| var | value |\n|---|---|\n")
	for _, key := range sortedStringKeysParams(t.Params) {
		value := vars[key]
		if desc := t.Params[key].Description; desc != "" {
			value += " — " + desc
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", key, value)
	}
	for _, key := range targetrun.ReservedTemplateVars {
		fmt.Fprintf(&b, "| `%s` | %s |\n", key, vars[key])
	}
	return b.String()
}

var templateRefPattern = regexp.MustCompile(`\{\{-?\s*\.([a-zA-Z_][a-zA-Z0-9_]*)\s*-?\}\}`)

// templateRefs extracts the {{.var}} names one template references.
func templateRefs(text string) []string {
	var names []string
	for _, m := range templateRefPattern.FindAllStringSubmatch(text, -1) {
		names = append(names, m[1])
	}
	return names
}

// displayMarkdown shows one composed help document: through glow when it is
// on PATH (BYOT), then through a pager — $SMITH_PAGER when set, else less or
// more when found — and plain stdout when no pager is available. A glow
// failure falls back to the raw markdown rather than failing the command.
func displayMarkdown(cmd *cobra.Command, doc string) error {
	rendered := doc
	if glow, err := lookPath("glow"); err == nil {
		var buf bytes.Buffer
		g := exec.Command(glow)
		g.Stdin = strings.NewReader(doc)
		g.Stdout = &buf
		g.Stderr = cmd.ErrOrStderr()
		if err := g.Run(); err == nil {
			rendered = buf.String()
		}
	}

	pager := os.Getenv("SMITH_PAGER")
	if pager == "" {
		if _, err := lookPath("less"); err == nil {
			pager = "less -R"
		} else if _, err := lookPath("more"); err == nil {
			pager = "more"
		}
	}
	if pager == "" {
		fmt.Fprint(stdout(cmd), rendered)
		return nil
	}
	page := exec.Command("sh", "-c", pager)
	page.Stdin = strings.NewReader(rendered)
	page.Stdout = stdout(cmd)
	page.Stderr = cmd.ErrOrStderr()
	if err := page.Run(); err != nil {
		return fmt.Errorf("pager %q: %w", pager, err)
	}
	return nil
}
