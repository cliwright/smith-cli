package cli

import (
	"github.com/spf13/cobra"
)

// The repo-level commands of the CLI, one stub per spec section. Each prints
// a single line about what it will eventually do.

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize the current directory as a Smith repository",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: initialize the current directory as a Smith repository (.smith/repo.yml, .smith/lock.json, ~/.smith cache root)")
			return nil
		},
	}
}

func newNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new <archetype> <project-type>",
		Short: "Scaffold a new project from a Cookiecutter template",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: scaffold a new project from a Cookiecutter template (smith new lib python → libs/<name>, then extend repo.yml)")
			return nil
		},
	}
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch and verify project types from the configured registry",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: fetch project types from the configured registries, verify hashes, and write .smith/lock.json")
			return nil
		},
	}
}

func newTypesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "types [<name>[@<version>]]",
		Short: "Browse the installed project-type registry",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: list installed project types, or show one type's versions / full definition (smith types [name[@version]])")
			return nil
		},
	}
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "BYOT toolchain check: report required tools found/missing on PATH",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: check every required tool against PATH and report found/missing, exiting non-zero if anything is missing")
			return nil
		},
	}
}

func newTreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tree",
		Short: "Print the discovered project tree",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: print every smith.yml found by traversal, as a tree annotated with project name and type")
			return nil
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the repo's namespaces from workspace.project_roots",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("not implemented yet: list the repo's namespaces — the workspace.project_roots declared in .smith/repo.yml")
			return nil
		},
	}
}
