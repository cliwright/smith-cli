package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// namespaces are the top-level directories smith dispatches on, mirroring
// the default workspace.project_roots. Eventually these come from
// .smith/repo.yml; the set is hard-coded while the dispatcher is a stub.
var namespaces = map[string]bool{
	"libs":     true,
	"services": true,
	"tools":    true,
	"images":   true,
}

func isNamespace(arg string) bool {
	return namespaces[arg]
}

// runNamespace handles "smith <ns> ..." invocations: list projects in the
// namespace, list targets for one project, or run a target. All of that
// arrives later; for now it prints a stub.
func runNamespace(cmd *cobra.Command, args []string) error {
	cmd.Printf("namespace dispatch: %s (not implemented)\n", strings.Join(args, " "))
	return nil
}
