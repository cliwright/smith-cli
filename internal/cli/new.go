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
	"github.com/cliwright/smith/internal/runner"
)

// kindRoots maps the kind segment of `smith new <language>/<kind>` to the
// workspace root the project is scaffolded under.
var kindRoots = map[string]string{
	"lib":     "libs",
	"tool":    "tools",
	"service": "services",
	"image":   "images",
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

Runs cookiecutter (which must be on PATH — BYOT) against the templates repo
of the first registry configured in .smith/repo.yml, with the template at
<language>/<kind>/v<version>, and places the result under the root that kind
maps to (lib→libs, tool→tools, service→services, image→images).

The template itself ships the project's smith.yml; smith does not generate
one. On success the root is appended to workspace.project_roots if missing.`,
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
	templatesGit := ""
	for _, reg := range cfg.Registries {
		if reg.Templates.Git != "" {
			templatesGit = reg.Templates.Git
			break
		}
	}
	if templatesGit == "" {
		return fmt.Errorf("no registry with a templates git source (templates.git) configured in .smith/repo.yml")
	}

	rootAbs := filepath.Join(repoRoot, root)
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", rootAbs, err)
	}

	tplDir := fmt.Sprintf("%s/%s/v%d", language, kind, version)
	runErr := newCommandRunner.Run(context.Background(), runner.Interactive(), "cookiecutter",
		templatesGit,
		"--directory", tplDir,
		"--output-dir", rootAbs,
	)
	if runErr != nil {
		return fmt.Errorf("cookiecutter failed: %w", runErr)
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
