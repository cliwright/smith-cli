package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/runner"
)

// kindRoots maps the kind segment of `smith new <language>/<kind>` to the
// workspace root the scaffolded tree lands under. The "type" kind is special:
// it scaffolds a registry project-type source tree, not a project.
var kindRoots = map[string]string{
	"lib":     "libs",
	"tool":    "tools",
	"service": "services",
	"image":   "images",
	"type":    "types",
}

// newCommandRunner runs external commands for `smith new`; it is a variable
// so tests can substitute a fake and never need cookiecutter installed.
var newCommandRunner commandRunner = runner.Default

type commandRunner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, opts runner.Options, name string, args ...string) error
}

func newNewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <language>/<kind>",
		Short: "Scaffold a new project from a Cookiecutter template",
		Long: `Scaffold a new project from a Cookiecutter template.

Runs cookiecutter (which must be on PATH — BYOT) against the templates source
of the first registry configured in .smith/repo.yml, with the template at
<language>/<kind>/v<version>, and places the result under the root that kind
maps to (lib→libs, tool→tools, service→services, image→images, type→types).

The template itself ships the project's smith.yml; smith does not generate
one (the user maintains it; a future CI check in the templates repo enforces
the template spec). On success the root is appended to
workspace.project_roots if missing.

The kind "type" scaffolds a registry project-type source tree instead of a
project (template smith/type/v<N>): the rendered
<language>/<flavor>/<kind>/v<N>/project-type.json is validated against the
project-type schema and the registry layout, and the smelt next step is
printed.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNew(cmd, args[0])
		},
	}
	cmd.Flags().Int("version", 0, "template version to use (mandatory positive integer, e.g. 1)")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func runNew(cmd *cobra.Command, arg string) error {
	parts := strings.Split(arg, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("want <language>/<kind> (e.g. python/lib), got %q", arg)
	}
	language, kind := parts[0], parts[1]
	root, known := kindRoots[kind]
	if !known {
		return fmt.Errorf("unknown kind %q (known kinds: %s)", kind, knownKinds())
	}
	version, err := cmd.Flags().GetInt("version")
	if err != nil {
		return err
	}
	if version < 1 {
		return fmt.Errorf("--version must be a positive integer, got %d", version)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith new: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(filepath.Join(repoRoot, cacheDirName, repoYMLName))
	if err != nil {
		return err
	}

	// Preflight everything that can fail BEFORE touching the filesystem.
	if _, err := newCommandRunner.LookPath("cookiecutter"); err != nil {
		return fmt.Errorf("cookiecutter is required on PATH to scaffold projects (BYOT — install it, e.g. `brew install cookiecutter` or `uv tool install cookiecutter`)")
	}
	templatesSrc := ""
	for _, reg := range cfg.Registries {
		if reg.Templates.Git != "" {
			templatesSrc = reg.Templates.Git
			break
		}
		if reg.Templates.Path != "" {
			templatesSrc = reg.Templates.Path
			break
		}
	}
	if templatesSrc == "" {
		return fmt.Errorf("no registry with a templates source (templates.git or templates.path) configured in .smith/repo.yml")
	}

	rootAbs := filepath.Join(repoRoot, root)
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", rootAbs, err)
	}

	var before map[string]bool
	if kind == "type" {
		before = snapshotTree(rootAbs)
	}

	tplDir := fmt.Sprintf("%s/%s/v%d", language, kind, version)
	runErr := newCommandRunner.Run(context.Background(), runner.Interactive(), "cookiecutter",
		templatesSrc,
		"--directory", tplDir,
		"--output-dir", rootAbs,
	)
	if runErr != nil {
		return fmt.Errorf("cookiecutter failed: %w", runErr)
	}

	if kind == "type" {
		if err := checkScaffoldedType(cmd, repoRoot, rootAbs, before); err != nil {
			return err
		}
	}

	out := stdout(cmd)
	fmt.Fprintf(out, "Created project from template %s under %s/\n", tplDir, root)

	// Success only: make sure the new root is declared, preserving comments.
	if !slices.Contains(cfg.Workspace.ProjectRoots, root) {
		cfg.Workspace.ProjectRoots = append(cfg.Workspace.ProjectRoots, root)
		if err := config.Save(filepath.Join(repoRoot, cacheDirName, repoYMLName), cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "Added %s to workspace.project_roots\n", root)
	}
	return nil
}

func knownKinds() string {
	kinds := make([]string, 0, len(kindRoots))
	for kind := range kindRoots {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}

// snapshotTree records the files under root (relative, slash-separated) so a
// later snapshot can be diffed to find what a scaffold created. A missing
// root yields an empty snapshot.
func snapshotTree(root string) map[string]bool {
	files := map[string]bool{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if rel, rerr := filepath.Rel(root, path); rerr == nil {
			files[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return files
}

// checkScaffoldedType validates the tree a `smith new smith/type` run just
// rendered: exactly one new project-type.json, loadable (schema included) and
// agreeing with its registry position. On success it prints the smelt next
// step. Runs before the success output and the project_roots append, so a
// failed validation leaves repo.yml untouched.
func checkScaffoldedType(cmd *cobra.Command, repoRoot, rootAbs string, before map[string]bool) error {
	var created []string
	for rel := range snapshotTree(rootAbs) {
		if !before[rel] && filepath.Base(rel) == "project-type.json" {
			created = append(created, rel)
		}
	}
	sort.Strings(created)
	switch len(created) {
	case 0:
		fmt.Fprintf(stdout(cmd), "Note: no new project-type.json appeared under %s — validate by hand: smith smelt --path <dir>\n", relForDisplay(repoRoot, rootAbs))
		return nil
	case 1:
	default:
		return fmt.Errorf("expected exactly one new project-type.json under %s, found %d (%s)",
			rootAbs, len(created), strings.Join(created, ", "))
	}

	ptPath := filepath.Join(rootAbs, filepath.FromSlash(created[0]))
	pt, err := projecttype.Load(ptPath)
	if err != nil {
		return fmt.Errorf("scaffolded type is invalid (%s): %w — fix the file, or remove the directory and re-scaffold", created[0], err)
	}
	if err := checkRegistryPosition(filepath.Dir(ptPath), pt); err != nil {
		return fmt.Errorf("scaffolded type failed the registry-layout check: %w", err)
	}
	fmt.Fprintf(stdout(cmd), "Validated %s@%d against the project-type schema and the registry layout\n", pt.Name, pt.Version)
	fmt.Fprintf(stdout(cmd), "next: smith smelt --path %s\n", relForDisplay(repoRoot, filepath.Dir(ptPath)))
	return nil
}

// relForDisplay renders dir relative to root for copy-paste-friendly output;
// on failure it returns dir unchanged.
func relForDisplay(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}
