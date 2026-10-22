package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/ingot"
	"github.com/cliwright/smith/internal/trust"
	"github.com/cliwright/smith/internal/typeref"
)

func newTrustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust [<language>/<flavor>/<kind>@<version>]",
		Short: "List and record trust for the project types installed in this repository",
		Long: `List and record trust for the project types installed in this
repository (see "smith types" for what is installed).

Installed types are untrusted by default: smith runs their steps, so
trusting is the explicit "I vetted this code" act that unlocks execution.
With no arguments, lists every installed type and its trust status. With
one exact ref, records trust for that type: the installed copy must pass
the custody check (its ingot matches the lock pin) — trusting content
that fails custody is refused, pointing at smith sync. --all trusts every
installed type that passes; broken installs are skipped with a warning.

Trusting records the ingot's sha256, the user, and a timestamp in a
per-user marker under .smith/trust/ (kept out of version control by a
fenced .gitignore block). A marker is valid only while the recorded hash
matches the installed content: a re-sync that lands different bytes
(publisher churn, another source) turns it stale and re-triggers the
execution gate. smith sync auto-trusts types fetched from a local path
registry; remote git sources are never auto-trusted. SMITH_AUTO_TRUST=1
bypasses the gate entirely (the CI escape hatch — set it in the workflow
environment, not a shell profile).`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("trust takes at most one type ref (or --all)")
			}
			all, err := cmd.Flags().GetBool("all")
			if err != nil {
				return err
			}
			if all && len(args) > 0 {
				return fmt.Errorf("--all takes no type ref")
			}
			return nil
		},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrust(cmd, args)
		},
	}
	cmd.Flags().Bool("all", false, "trust every installed type that passes its custody check")
	return cmd
}

func runTrust(cmd *cobra.Command, args []string) error {
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith trust: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return err
	}

	// A ref argument trusts one installed type; --all trusts every
	// installed type that passes its custody check; with neither, list.
	if len(args) == 1 {
		return trustOne(cmd, repoRoot, args[0])
	}
	if all {
		return trustAll(cmd, repoRoot)
	}
	return trustList(cmd, repoRoot)
}

// trustList prints every installed type and its trust status against the
// installed ingot. The status is marker-vs-content, independent of the
// lock; a stale marker means the installed bytes changed since the type
// was trusted.
func trustList(cmd *cobra.Command, repoRoot string) error {
	out := stdout(cmd)
	if trust.Bypassed() {
		fmt.Fprintln(out, "SMITH_AUTO_TRUST=1 — trust model bypassed")
	}
	installed, warnings, err := scanInstalledTypes(typesDir(repoRoot))
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	if len(installed) == 0 {
		fmt.Fprintf(out, "no project types installed in %s yet — run smith sync to fetch the types this repo uses\n",
			filepath.Join(cacheDirName, "types"))
		return nil
	}
	for _, it := range installed {
		status, marker, err := trust.Check(repoRoot, it.ref, it.hash)
		if err != nil {
			fmt.Fprintf(out, "warning: %s\n", err)
			continue
		}
		fmt.Fprintf(out, "%s  %s\n", it.ref, trustLabel(status, marker))
	}
	return nil
}

// trustLabel renders one status for the listing: the vetting user and
// time annotate a trusted ref.
func trustLabel(status trust.Status, m *trust.Marker) string {
	switch status {
	case trust.Trusted:
		return fmt.Sprintf("trusted (by %s at %s)", m.User, m.Time)
	case trust.Stale:
		return "stale — content changed"
	default:
		return "untrusted"
	}
}

// trustOne vets and trusts one installed type. Trusting records the ingot
// hash the user vetted; the installed copy must satisfy the custody chain
// (the artifact matches the lock pin and the unpacked tree matches the
// artifact) — trusting content that fails custody is refused.
func trustOne(cmd *cobra.Command, repoRoot, arg string) error {
	out := stdout(cmd)
	ref, err := typeref.Parse(arg)
	if err != nil {
		return fmt.Errorf("smith trust: %w", err)
	}
	lk, err := loadExistingLock(filepath.Join(repoRoot, cacheDirName, lockJSONName))
	if err != nil {
		return fmt.Errorf("smith trust: %w", err)
	}
	pin, ok := lk.Types[ref.String()]
	if !ok {
		return fmt.Errorf("type %s is not pinned in %s; run smith sync", ref, filepath.Join(cacheDirName, lockJSONName))
	}
	if err := ingot.VerifyInstalled(filepath.Join(typesDir(repoRoot), installRelDir(ref)), pin.Hash); err != nil {
		return fmt.Errorf("type %s: %w; refusing to trust content that fails the custody check — run smith sync", ref, err)
	}
	m, err := trust.Write(repoRoot, ref, pin.Hash)
	if err != nil {
		return fmt.Errorf("smith trust: %w", err)
	}
	fmt.Fprintf(out, "%s: trusted (by %s at %s)\n", ref, m.User, m.Time)
	return nil
}

// trustAll vets and trusts every installed type whose custody chain
// holds; broken installs are skipped with a warning, the same report-and-
// continue ethos as the types browser.
func trustAll(cmd *cobra.Command, repoRoot string) error {
	out := stdout(cmd)
	installed, warnings, err := scanInstalledTypes(typesDir(repoRoot))
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	if len(installed) == 0 {
		fmt.Fprintf(out, "no project types installed in %s yet — run smith sync to fetch the types this repo uses\n",
			filepath.Join(cacheDirName, "types"))
		return nil
	}
	lk, err := loadExistingLock(filepath.Join(repoRoot, cacheDirName, lockJSONName))
	if err != nil {
		return fmt.Errorf("smith trust: %w", err)
	}
	trusted := 0
	for _, it := range installed {
		pin, ok := lk.Types[it.ref.String()]
		switch {
		case !ok:
			fmt.Fprintf(out, "warning: %s: not pinned in %s — run smith sync (skipped)\n", it.ref, filepath.Join(cacheDirName, lockJSONName))
			continue
		case it.hash == "":
			fmt.Fprintf(out, "warning: %s: no ingot installed — run smith sync (skipped)\n", it.ref)
			continue
		case pin.Hash != it.hash:
			fmt.Fprintf(out, "warning: %s: hash mismatch — run smith sync (skipped)\n", it.ref)
			continue
		}
		if err := ingot.VerifyInstalled(filepath.Dir(it.path), pin.Hash); err != nil {
			fmt.Fprintf(out, "warning: %s: %v — run smith sync (skipped)\n", it.ref, err)
			continue
		}
		m, err := trust.Write(repoRoot, it.ref, pin.Hash)
		if err != nil {
			fmt.Fprintf(out, "warning: %s: %v (skipped)\n", it.ref, err)
			continue
		}
		trusted++
		fmt.Fprintf(out, "%s: trusted (by %s at %s)\n", it.ref, m.User, m.Time)
	}
	fmt.Fprintf(out, "trusted %d of %d installed type(s)\n", trusted, len(installed))
	return nil
}
