// Package cli implements the smith command-line interface: a cobra root
// command with repo-level subcommands and a namespace-first dispatcher for
// everything else. Every command is currently a stub.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Execute runs the root command and exits non-zero on failure.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "smith",
		Short: "Smith — a language-agnostic monorepo build tool",
		Long: `Smith is a namespace-first build tool for monorepos.

Top-level commands work on the repository as a whole; anything matching a
namespace (libs, services, tools, images, …) is dispatched to that
namespace's projects. See "smith help" for the full grammar.`,
		Args:    cobra.ArbitraryArgs,
		RunE:    dispatch,
		Version: "0.0.0",
	}
	root.PersistentFlags().IntP("parallel", "p", 1,
		"run N targets concurrently (default 1 = serial, deterministic output)")

	root.AddCommand(
		newInitCmd(),
		newNewCmd(),
		newSyncCmd(),
		newTypesCmd(),
		newDoctorCmd(),
		newTreeCmd(),
		newListCmd(),
	)
	return root
}

// stdout is where normal command output goes. cobra's own Print* helpers
// default to stderr (since v1.9); smith writes informational output to
// stdout so it can be piped or redirected.
func stdout(cmd *cobra.Command) io.Writer {
	return cmd.OutOrStdout()
}

// dispatch handles invocations that did not match a subcommand: known
// namespaces get the namespace stub, anything else is an unknown command.
func dispatch(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if isNamespace(args[0]) {
		return runNamespace(cmd, args)
	}
	return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
}
