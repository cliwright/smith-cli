package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the repo's namespaces",
		Long: `List the repo's namespaces: the workspace.project_roots declared in
.smith/repo.yml, in declaration order, followed by the reserved "repo"
namespace of repo-level targets (repo_targets). Every line is something you
can type after "smith".`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd)
		},
	}
}

func runList(cmd *cobra.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith list: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(filepath.Join(repoRoot, ".smith", "repo.yml"))
	if err != nil {
		return err
	}
	out := stdout(cmd)
	for _, root := range cfg.Workspace.ProjectRoots {
		fmt.Fprintln(out, filepath.ToSlash(root))
	}
	// The synthetic namespace always exists, even with no repo_targets —
	// dispatching to it prints how to add some.
	fmt.Fprintln(out, "repo")
	return nil
}
