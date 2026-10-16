// Package cli implements the smith command-line interface: a cobra root
// command with repo-level subcommands and a namespace-first dispatcher for
// everything else. Every command is currently a stub.
package cli

import (
	"io"
	"os"
	"regexp"
	"strings"

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
		// Errors are self-descriptive (they list available targets,
		// attachments, or suggest `smith sync`); a runtime failure is not a
		// usage mistake, so never dump the command tree after one.
		SilenceUsage: true,
	}
	root.PersistentFlags().IntP("parallel", "p", 1,
		"run N targets concurrently (default 1 = serial, deterministic output)")
	root.PersistentFlags().String("env", "",
		"select the environment: load dotenv/.<env>.env overlays and export SMITH_ENV to target steps (overrides $SMITH_ENV and repo.yml smith_env.default)")
	root.PersistentFlags().Bool("context", false,
		"print the resolved environment (annotated KEY=VALUE) for the addressed scope instead of running; bare 'smith --context' shows the repo-level dotenv view")

	root.AddCommand(
		newInitCmd(),
		newNewCmd(),
		newSyncCmd(),
		newTypesCmd(),
		newSmeltCmd(),
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

// dispatch handles invocations that did not match a subcommand: reserved
// synthetic namespaces first (repo, services — routed before anything
// name-based, since attachments/services share the project-name space), then
// the positional grammar (namespaces, namespace-free project addressing, the
// repo-wide target run) via dispatchPositional. Subcommand names stay
// reserved — cobra routes them before this runs, so a project named "sync"
// is shadowed by the sync command (documented, not coded around).
//
// Everything after the first standalone "--" is captured verbatim before any
// routing: grammar args flow on to the namespace/repo/services dispatchers
// (so the existing too-many-arguments errors are preserved when no "--" is
// given), and the passthrough tokens become the invocation's SMITH_CLI_ARGS
// (shell-quoted join), broadcast to every step of a target run.
func dispatch(cmd *cobra.Command, args []string) error {
	grammar, passthrough := splitPassthrough(cmd, args)
	args = grammar
	contextFlag, _ := cmd.Flags().GetBool("context")
	if len(args) == 0 && !contextFlag {
		return cmd.Help()
	}
	rc, err := loadRepoContext()
	if err != nil {
		return err
	}
	rc.cliArgs = joinPassthrough(passthrough)
	if contextFlag {
		return rc.dispatchContext(cmd, args)
	}
	// "repo" is a RESERVED namespace: the synthetic attachments namespace
	// shadows any project_root directory literally named "repo".
	if args[0] == "repo" {
		return rc.dispatchRepo(cmd, args[1:])
	}
	// "services" is a RESERVED namespace too: the service-composition grammar
	// shadows the directory namespace of the same name (projects under
	// services/ stay reachable through repo-wide target runs).
	if args[0] == servicesRootName {
		return rc.dispatchServices(cmd, args[1:])
	}
	return rc.dispatchPositional(cmd, args)
}

// splitPassthrough separates the grammar args from the tokens after the
// first standalone "--". pflag consumes the "--" itself, packs the remaining
// tokens into Args(), and records the boundary at ArgsLenAtDash (-1 when no
// "--" was given).
func splitPassthrough(cmd *cobra.Command, args []string) (grammar, passthrough []string) {
	if n := cmd.ArgsLenAtDash(); n >= 0 {
		return args[:n], args[n:]
	}
	return args, nil
}

// passthroughSafe matches tokens that survive an sh -c re-parse unchanged
// without quoting: alphanumerics and a conservative safe-punctuation set.
var passthroughSafe = regexp.MustCompile(`^[a-zA-Z0-9_@%+=:,./-]+$`)

// joinPassthrough renders passthrough tokens round-trip-safe for
// substitution into a step string and re-parsing by sh -c: a token is
// single-quoted iff it is empty or contains anything outside the safe
// charset (embedded single quotes close, escape, and reopen), and tokens
// are joined with single spaces. No tokens yields "".
func joinPassthrough(tokens []string) string {
	quoted := make([]string, len(tokens))
	for i, tok := range tokens {
		if tok == "" || !passthroughSafe.MatchString(tok) {
			quoted[i] = "'" + strings.ReplaceAll(tok, "'", `'\''`) + "'"
		} else {
			quoted[i] = tok
		}
	}
	return strings.Join(quoted, " ")
}
