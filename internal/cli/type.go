package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/ingot"
	"github.com/cliwright/smith/internal/projecttype"
)

// newTypeCmd is the `smith type` command: project-type authoring. Its sibling
// `smith types` stays the installed-type browser; `type` is for the registry
// side (authoring and packing).
func newTypeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "type",
		Short: "Author and pack project types",
		Long: `Author and pack project types.

These commands operate on the registry side — the source directories that
publish types — not on a repository's installed copies. See "smith types"
for browsing what is installed in this repository.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.AddCommand(newTypePackCmd())
	return cmd
}

func newTypePackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Pack a type directory into its ingot and sidecar",
		Long: `Pack a type directory into its published artifact.

--path names a registry-layout type version directory — the path holding
project-type.json, e.g. python/astral/lib/v1. The directory's position in
the tree must agree with the file (the registry invariant: the last three
path segments are the type name, and the version directory is v<N> matching
the file's "version"). The pack fails loudly on a mismatch rather than
publishing an incoherent artifact.

Writes <flat-name>.ingot (a reproducible tar archive of project-type.json
plus the type's scripts/ and help/) and its .sha256 sidecar into the
directory. CI (the registry repo's "task publish") invokes this for every
type; the sidecar is what "smith sync" verifies the download against.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Flags().GetString("path")
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("smith type pack: --path is required (the type version directory, e.g. python/astral/lib/v1)")
			}
			return runTypePack(cmd, path)
		},
	}
	cmd.Flags().String("path", "",
		"type version directory to pack (registry layout: <language>/<flavor>/<kind>/v<N>)")
	return cmd
}

func runTypePack(cmd *cobra.Command, arg string) error {
	dir, err := filepath.Abs(arg)
	if err != nil {
		return fmt.Errorf("smith type pack: %w", err)
	}
	ptPath := filepath.Join(dir, "project-type.json")
	pt, err := projecttype.Load(ptPath)
	if err != nil {
		return fmt.Errorf("smith type pack: %w", err)
	}
	if err := checkRegistryPosition(dir, pt); err != nil {
		return fmt.Errorf("smith type pack: %w", err)
	}

	ingotBytes, err := ingot.Pack(dir)
	if err != nil {
		return fmt.Errorf("smith type pack: %w", err)
	}
	names, err := ingot.List(ingotBytes)
	if err != nil {
		return fmt.Errorf("smith type pack: %w", err)
	}

	artifactName := ingot.FileName(pt.Name, pt.Version)
	artifact := filepath.Join(dir, artifactName)
	if err := os.WriteFile(artifact, ingotBytes, 0o644); err != nil {
		return fmt.Errorf("smith type pack: writing %s: %w", artifact, err)
	}
	sidecar := artifact + ".sha256"
	sidecarBody := fmt.Sprintf("%s  %s\n", strings.TrimPrefix(ingot.Hash(ingotBytes), "sha256:"), artifactName)
	if err := os.WriteFile(sidecar, []byte(sidecarBody), 0o644); err != nil {
		return fmt.Errorf("smith type pack: writing %s: %w", sidecar, err)
	}

	out := stdout(cmd)
	for _, name := range names {
		fmt.Fprintf(out, "  packed %s\n", name)
	}
	fmt.Fprintf(out, "wrote %s (%d file(s), %s)\nwrote %s\n",
		filepath.Join(arg, artifactName), len(names), ingot.Hash(ingotBytes), filepath.Join(arg, artifactName+".sha256"))
	return nil
}

// checkRegistryPosition enforces the registry invariant on disk: the type
// version directory's path tail must read <name>/v<N> with the name matching
// project-type.json's "name" and N its "version".
func checkRegistryPosition(dir string, pt *projecttype.ProjectType) error {
	base := filepath.Base(dir)
	if want := "v" + strconv.Itoa(pt.Version); base != want {
		return fmt.Errorf("%s: version directory is %q but project-type.json declares version %d (registry layout wants %q)",
			dir, base, pt.Version, want)
	}
	segs := strings.Split(filepath.ToSlash(filepath.Dir(dir)), "/")
	if len(segs) < 3 {
		return fmt.Errorf("%s: type directory must sit at <language>/<flavor>/<kind>/v<N>", dir)
	}
	name := strings.Join(segs[len(segs)-3:], "/")
	if name != pt.Name {
		return fmt.Errorf("%s: path implies type %q but project-type.json declares %q", dir, name, pt.Name)
	}
	return nil
}
