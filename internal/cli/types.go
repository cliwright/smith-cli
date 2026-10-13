package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/typeref"
)

// typeNamePattern validates the name-only form of `smith types <name>`.
var typeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*){2}$`)

func newTypesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "types [<name>[@<version>]]",
		Short: "Browse the project types installed in this repository",
		Long: `Browse the project types installed in this repository's
.smith/types directory (repo-local — checked in by default, so a clone
builds without a sync step). Requires a Smith repository.

With no arguments, lists every installed type, sorted by name then version,
each annotated with its lock status from .smith/lock.json: pinned,
not pinned, or hash mismatch — run smith sync. With a name, lists every
installed version of that type; with name@version, prints the full
definition: description, capabilities, tools, working_dir, params,
environment, targets, and target depends_on.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTypes(cmd, args)
		},
	}
}

// installedType is one type found in the repo's .smith/types.
type installedType struct {
	ref  typeref.TypeRef
	path string
	data []byte
	pt   *projecttype.ProjectType
}

func runTypes(cmd *cobra.Command, args []string) error {
	out := stdout(cmd)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith types: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return err
	}
	installRoot := typesDir(repoRoot)

	installed, warnings, err := scanInstalledTypes(installRoot)
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

	// Every type is annotated with its chain-of-custody status against the
	// repo's lock; a lock that exists but cannot load is a warning, and a
	// missing lock means nothing is pinned yet.
	repoLock := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{}}
	loaded, err := lock.Load(filepath.Join(repoRoot, cacheDirName, lockJSONName))
	switch {
	case err == nil:
		repoLock = loaded
	case errors.Is(err, os.ErrNotExist):
	default:
		fmt.Fprintf(out, "warning: could not load %s: %v (treating every type as not pinned)\n",
			filepath.Join(cacheDirName, lockJSONName), err)
	}
	status := func(it installedType) string {
		return lockStatus(repoLock, it.ref, it.data)
	}

	if len(args) == 0 {
		for _, it := range installed {
			annotation := ""
			if s := status(it); s != "" {
				annotation = "  [" + s + "]"
			}
			fmt.Fprintf(out, "%s  %s%s\n", it.ref, it.pt.Description, annotation)
		}
		return nil
	}

	arg := args[0]
	if strings.Contains(arg, "@") {
		return showTypeDetail(out, installed, arg, status)
	}
	return showTypeVersions(out, installed, arg, status)
}

// scanInstalledTypes walks .smith/types, loading every project-type.json it
// finds. Corrupt or unloadable files are reported as warnings; the listing
// continues. Sorted by name, then version.
func scanInstalledTypes(installRoot string) ([]installedType, []string, error) {
	var installed []installedType
	var warnings []string
	err := filepath.WalkDir(installRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot read %s: %v", path, err))
			return nil
		}
		pt, err := projecttype.Load(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot load %s: %v", path, err))
			return nil
		}
		installed = append(installed, installedType{
			ref:  typeref.TypeRef{Name: pt.Name, Version: pt.Version},
			path: path,
			data: data,
			pt:   pt,
		})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, warnings, nil
	}
	if err != nil {
		return nil, warnings, fmt.Errorf("scanning %s: %w", installRoot, err)
	}
	sort.Slice(installed, func(i, j int) bool {
		if installed[i].ref.Name != installed[j].ref.Name {
			return installed[i].ref.Name < installed[j].ref.Name
		}
		return installed[i].ref.Version < installed[j].ref.Version
	})
	return installed, warnings, nil
}

// lockStatus annotates one installed type with its chain-of-custody status
// against the lock.
func lockStatus(lk *lock.Lock, ref typeref.TypeRef, data []byte) string {
	pin, ok := lk.Types[ref.String()]
	if !ok {
		return "not pinned"
	}
	if pin.Hash != hashBytes(data) {
		return "hash mismatch — run smith sync"
	}
	return "pinned"
}

func showTypeVersions(out io.Writer, installed []installedType, name string, status func(installedType) string) error {
	if !typeNamePattern.MatchString(name) {
		return fmt.Errorf("want <language>/<flavor>/<kind> or <language>/<flavor>/<kind>@<version>, got %q", name)
	}
	var versions []installedType
	for _, it := range installed {
		if it.ref.Name == name {
			versions = append(versions, it)
		}
	}
	if len(versions) == 0 {
		return fmt.Errorf("type %q is not installed (installed: %s)", name, strings.Join(installedNames(installed), ", "))
	}
	for _, it := range versions {
		annotation := ""
		if s := status(it); s != "" {
			annotation = "  [" + s + "]"
		}
		fmt.Fprintf(out, "%d  %s%s\n", it.ref.Version, it.pt.Description, annotation)
	}
	return nil
}

func showTypeDetail(out io.Writer, installed []installedType, arg string, status func(installedType) string) error {
	ref, err := typeref.Parse(arg)
	if err != nil {
		return err
	}
	var found *installedType
	for i := range installed {
		if installed[i].ref == ref {
			found = &installed[i]
			break
		}
	}
	if found == nil {
		var versions []string
		for _, it := range installed {
			if it.ref.Name == ref.Name {
				versions = append(versions, strconv.Itoa(it.ref.Version))
			}
		}
		if len(versions) == 0 {
			return fmt.Errorf("type %q is not installed (installed: %s)", ref.Name, strings.Join(installedNames(installed), ", "))
		}
		return fmt.Errorf("type %s is not installed (installed versions: %s)", ref, strings.Join(versions, ", "))
	}

	pt := found.pt
	fmt.Fprintf(out, "%s\n", found.ref)
	fmt.Fprintf(out, "  description: %s\n", pt.Description)
	if len(pt.Capabilities) > 0 {
		fmt.Fprintf(out, "  capabilities: %s\n", strings.Join(capabilityStrings(pt.Capabilities), ", "))
	}
	fmt.Fprintf(out, "  tools: %s\n", strings.Join(pt.Tools, ", "))
	fmt.Fprintf(out, "  working_dir: %s\n", pt.WorkingDir)
	if len(pt.Params) > 0 {
		fmt.Fprintln(out, "  params:")
		for _, key := range sortedStringKeysParams(pt.Params) {
			param := pt.Params[key]
			line := fmt.Sprintf("    %s (default: %s)", key, param.Default)
			if param.Description != "" {
				line += " — " + param.Description
			}
			fmt.Fprintln(out, line)
		}
	}
	if len(pt.Environment) > 0 {
		fmt.Fprintln(out, "  environment:")
		for _, key := range sortedStringKeys(pt.Environment) {
			fmt.Fprintf(out, "    %s=%s\n", key, pt.Environment[key])
		}
	}
	fmt.Fprintln(out, "  targets:")
	for _, target := range sortedKeys(pt.Targets) {
		fmt.Fprintf(out, "    %s:\n", target)
		for _, step := range pt.Targets[target] {
			fmt.Fprintf(out, "      %s\n", step)
		}
	}
	if len(pt.DependsOn) > 0 {
		fmt.Fprintln(out, "  depends_on:")
		for _, target := range sortedKeys(pt.DependsOn) {
			fmt.Fprintf(out, "    %s: %s\n", target, strings.Join(pt.DependsOn[target], ", "))
		}
	}
	if s := status(*found); s != "" {
		fmt.Fprintf(out, "  status: %s\n", s)
	}
	return nil
}

func installedNames(installed []installedType) []string {
	seen := map[string]bool{}
	var names []string
	for _, it := range installed {
		if !seen[it.ref.Name] {
			seen[it.ref.Name] = true
			names = append(names, it.ref.Name)
		}
	}
	return names
}

func capabilityStrings(caps []projecttype.Capability) []string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}

func sortedStringKeysParams(m map[string]projecttype.Param) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedStringKeys sorts the keys of a string map (shared by the types
// views and the test fixtures).
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
