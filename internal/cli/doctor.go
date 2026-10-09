package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/discovery"
)

// errUnhealthy makes the doctor command exit non-zero without cobra printing
// an "Error:" line: the findings themselves are the output, on stdout.
var errUnhealthy = errors.New("doctor found problems")

// versionProbeTimeout bounds the <tool> --version probe.
const versionProbeTimeout = 10 * time.Second

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "BYOT toolchain check: report required tools found/missing on PATH",
		Long: `Check every tool this repository needs against PATH (BYOT — smith
never installs anything).

The required set is the union of the repo's tools list, every discovered
project's type tools, and every repo_targets attachment's type tools. Each
tool is resolved with exec.LookPath and probed with "<tool> --version"; a
probe that fails, times out, or returns nothing reports "version unknown".
A type that cannot be loaded (not installed, hash mismatch) is reported as a
finding — "run smith sync" — and the check continues with the rest.

Exits non-zero when anything is missing or any type could not be checked.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true, // findings are the output; errUnhealthy only sets the exit code
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd)
		},
	}
}

func runDoctor(cmd *cobra.Command) error {
	rc, err := loadRepoContext()
	if err != nil {
		return err
	}
	out := stdout(cmd)

	// needers aggregates, per tool, the sorted deduped list of consumers:
	// project names, "repo", or "repo:<alias>".
	needers := map[string][]toolNeeder{}
	var warnings []string

	collect := func(label string, p discovery.Project) {
		t, err := rc.typeFor(p)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot check tools for %s (type %s): run smith sync", label, p.Manifest.Type))
			return
		}
		for _, tool := range t.Tools {
			addNeeder(needers, tool, label)
		}
	}

	for _, tool := range rc.cfg.Tools {
		addNeeder(needers, tool, "repo")
	}
	for _, p := range rc.projects {
		collect(p.Manifest.Name, p)
	}
	for _, a := range rc.attachments() {
		collect("repo:"+a.Manifest.Name, a)
	}

	return reportDoctor(out, needers, warnings)
}

// toolNeeder is one consumer of a tool: a project name, "repo", or
// "repo:<alias>".
type toolNeeder = string

func addNeeder(needers map[string][]toolNeeder, tool, needer string) {
	for _, existing := range needers[tool] {
		if existing == needer {
			return
		}
	}
	needers[tool] = append(needers[tool], needer)
}

// reportDoctor probes every required tool and prints the findings: one line
// per found tool, a "missing:" section, a "warnings:" section for unloadable
// types, and a summary line. Returns errUnhealthy when anything is missing
// or could not be checked.
func reportDoctor(out io.Writer, needers map[string][]toolNeeder, warnings []string) error {
	tools := make([]string, 0, len(needers))
	for tool := range needers {
		tools = append(tools, tool)
	}
	sort.Strings(tools)

	var missing []string
	ok := 0
	for _, tool := range tools {
		consumers := sortedCopy(needers[tool])
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, fmt.Sprintf("%s  needed by: %s", tool, strings.Join(consumers, ", ")))
			continue
		}
		ok++
		fmt.Fprintf(out, "%s  %s  needed by: %s\n", tool, probeVersion(tool), strings.Join(consumers, ", "))
	}

	if len(missing) > 0 {
		fmt.Fprintln(out, "missing:")
		for _, line := range missing {
			fmt.Fprintln(out, line)
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintln(out, "warnings:")
		for _, warning := range warnings {
			fmt.Fprintln(out, warning)
		}
	}

	summary := fmt.Sprintf("doctor: %d ok, %d missing", ok, len(missing))
	if len(warnings) > 0 {
		summary += fmt.Sprintf(", %d type(s) could not be checked", len(warnings))
	}
	fmt.Fprintln(out, summary)

	if len(missing) > 0 || len(warnings) > 0 {
		return errUnhealthy
	}
	return nil
}

// probeVersion returns the first non-empty line of `<tool> --version`,
// trimmed, or "version unknown" when the probe errors, times out, or prints
// nothing.
func probeVersion(tool string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, tool, "--version").CombinedOutput()
	if err != nil {
		return "version unknown"
	}
	for line := range strings.Lines(string(output)) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return "version unknown"
}

func sortedCopy(xs []string) []string {
	out := make([]string, len(xs))
	copy(out, xs)
	sort.Strings(out)
	return out
}
