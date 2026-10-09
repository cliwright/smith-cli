package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The repo-level commands of the CLI. types is the only stub; the rest live
// in their own files (init.go, new.go, sync.go, tree.go, list.go, doctor.go).

func newTypesCmd() *cobra.Command {
	return stub("types [<name>[@<version>]]",
		"Browse the installed project-type registry",
		"not implemented yet: list installed project types, or show one type's versions / full definition (smith types [name[@version]])")
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
