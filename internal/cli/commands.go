package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The repo-level commands of the CLI, one stub per spec section. Each prints
// a single line about what it will eventually do. (init, tree and sync are
// implemented; init in init.go, tree in tree.go, sync in sync.go.)

func newNewCmd() *cobra.Command {
	return stub("new <archetype> <project-type>",
		"Scaffold a new project from a Cookiecutter template",
		"not implemented yet: scaffold a new project from a Cookiecutter template (smith new lib python → libs/<name>, then extend repo.yml)")
}

func newTypesCmd() *cobra.Command {
	return stub("types [<name>[@<version>]]",
		"Browse the installed project-type registry",
		"not implemented yet: list installed project types, or show one type's versions / full definition (smith types [name[@version]])")
}

func newDoctorCmd() *cobra.Command {
	return stub("doctor",
		"BYOT toolchain check: report required tools found/missing on PATH",
		"not implemented yet: check every required tool against PATH and report found/missing, exiting non-zero if anything is missing")
}

func newListCmd() *cobra.Command {
	return stub("list",
		"List the repo's namespaces from workspace.project_roots",
		"not implemented yet: list the repo's namespaces — the workspace.project_roots declared in .smith/repo.yml")
}

// stub builds a no-argument command that prints its not-implemented-yet
// message to stdout.
func stub(use, short, message string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(stdout(cmd), message)
			return nil
		},
	}
}
