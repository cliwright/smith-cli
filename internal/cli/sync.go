package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/projecttype"
	syncpkg "github.com/cliwright/smith/internal/sync"
	"github.com/cliwright/smith/internal/typeref"
)

// rawBaseURL maps a registry git URL to its raw-content base URL; it is a
// variable so tests can point it at an httptest server.
var rawBaseURL = syncpkg.RawBase

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [<name>@<version>...]",
		Short: "Fetch and verify the project types this repo uses",
		Long: `Fetch and verify the project types this repo uses.

With no arguments, discovers the repo's projects and fetches the type each
manifest references. With arguments, fetches those explicit type refs
regardless of manifests.

Types are fetched over plain HTTP from raw.githubusercontent.com
(GitHub-only for now): the project-type.json and its checked-in .sha256
sidecar. The sidecar must hash the content. Types already installed whose
hash matches the lock are reported "up to date". Everything is staged in
memory; installed files and .smith/lock.json are written only if every
fetch succeeded.`,
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, args)
		},
	}
}

func runSync(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith sync: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(filepath.Join(repoRoot, ".smith", "repo.yml"))
	if err != nil {
		return err
	}
	refs, err := resolveRefs(repoRoot, cfg, args)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Fprintln(stdout(cmd), "no project types to sync")
		return nil
	}

	lockPath := filepath.Join(repoRoot, cacheDirName, lockJSONName)
	// The lock is consulted lazily, only when a skip decision or the final
	// merge needs it: fetch failures then surface before complaints about a
	// stale on-disk lock, and a repo that has nothing installed yet doesn't
	// need a valid lock to make progress.
	var lk *lock.Lock
	getLock := func() (*lock.Lock, error) {
		if lk == nil {
			loaded, err := loadExistingLock(lockPath)
			if err != nil {
				return nil, err
			}
			lk = loaded
		}
		return lk, nil
	}

	home, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	installRoot := filepath.Join(home, cacheDirName, "project-types")
	if err := os.MkdirAll(installRoot, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", installRoot, err)
	}

	bases, err := registryBases(cfg)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	out := stdout(cmd)

	var staged []stagedInstall
	var fetchedRefs []typeref.TypeRef
	fetched := 0
	for _, ref := range refs {
		dest := filepath.Join(installRoot, installRelPath(ref))
		if data, err := os.ReadFile(dest); err == nil {
			lockState, err := getLock()
			if err != nil {
				return err
			}
			if pin, ok := lockState.Types[ref.String()]; ok && pin.Hash == hashBytes(data) {
				fmt.Fprintf(out, "%s: up to date\n", ref)
				continue
			}
		}

		ft, registry, err := fetchWithFallback(context.Background(), client, bases, ref)
		if err != nil {
			return err
		}
		lockState, err := getLock()
		if err != nil {
			return err
		}
		staged = append(staged, stagedInstall{path: dest, content: ft.Content})
		lockState.Types[ref.String()] = lock.TypePin{Registry: registry, Hash: ft.Hash}
		fetchedRefs = append(fetchedRefs, ref)
		fetched++
		fmt.Fprintf(out, "%s: fetched from %s\n", ref, registry)
	}

	if fetched == 0 {
		fmt.Fprintln(out, "all types up to date")
		return nil
	}

	// Every fetch succeeded: commit installs, union the new types' tools
	// into repo.yml, then the lock (atomically).
	for _, s := range staged {
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			return fmt.Errorf("installing %s: %w", s.path, err)
		}
		if err := os.WriteFile(s.path, s.content, 0o644); err != nil {
			return fmt.Errorf("installing %s: %w", s.path, err)
		}
	}
	repoYMLPath := filepath.Join(repoRoot, cacheDirName, repoYMLName)
	added, err := unionFetchedTools(repoYMLPath, cfg, installRoot, fetchedRefs)
	if err != nil {
		return err
	}
	if len(added) > 0 {
		fmt.Fprintf(out, "%s: added tools: %s\n", filepath.Join(cacheDirName, repoYMLName), strings.Join(added, ", "))
	}
	lockState, err := getLock()
	if err != nil {
		return err
	}
	if err := lock.WriteAtomic(lockPath, lockState); err != nil {
		return err
	}
	fmt.Fprintf(out, "lock written to %s (%d type(s) pinned)\n",
		filepath.Join(cacheDirName, lockJSONName), len(lockState.Types))
	return nil
}

// unionFetchedTools adds the tools declared by each freshly fetched type to
// the repo config's tools list (BYOT — doctor checks them against PATH). The
// write goes through the comment-preserving config round-trip, and only
// happens when the set actually changed.
func unionFetchedTools(repoYMLPath string, cfg *config.RepoConfig, installRoot string, refs []typeref.TypeRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	existing := make(map[string]bool, len(cfg.Tools))
	for _, tool := range cfg.Tools {
		existing[tool] = true
	}
	var added []string
	for _, ref := range refs {
		pt, err := projecttype.Load(filepath.Join(installRoot, installRelPath(ref)))
		if err != nil {
			return nil, fmt.Errorf("installed type %s: %w", ref, err)
		}
		for _, tool := range pt.Tools {
			if !existing[tool] {
				existing[tool] = true
				added = append(added, tool)
			}
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	cfg.Tools = append(cfg.Tools, added...)
	if err := config.Save(repoYMLPath, cfg); err != nil {
		return nil, err
	}
	return added, nil
}

// resolveRefs returns the sorted, deduplicated set of type refs to sync:
// parsed from the args, or collected from the manifests of discovered
// projects when no args are given.
func resolveRefs(repoRoot string, cfg *config.RepoConfig, args []string) ([]typeref.TypeRef, error) {
	seen := map[string]bool{}
	var refs []typeref.TypeRef
	add := func(ref typeref.TypeRef) {
		if !seen[ref.String()] {
			seen[ref.String()] = true
			refs = append(refs, ref)
		}
	}

	if len(args) > 0 {
		for _, arg := range args {
			ref, err := typeref.Parse(arg)
			if err != nil {
				return nil, err
			}
			add(ref)
		}
	} else {
		projects, err := discovery.FindProjects(repoRoot, cfg)
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			ref, err := typeref.Parse(p.Manifest.Type)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.ToSlash(filepath.Join(p.Dir, "smith.yml")), err)
			}
			add(ref)
		}
		// Attachments (repo-level targets) join the fetch set; their custody
		// through the lock is identical to manifest-referenced types.
		for alias, target := range cfg.RepoTargets {
			ref, err := typeref.Parse(target.Type)
			if err != nil {
				return nil, fmt.Errorf("repo_targets.%s: %w", alias, err)
			}
			add(ref)
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	return refs, nil
}

// loadExistingLock loads the repo's lock, treating a missing file as a fresh
// empty lock. An existing file must validate against the lock schema.
func loadExistingLock(lockPath string) (*lock.Lock, error) {
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{}}
	if _, err := os.Stat(lockPath); errors.Is(err, os.ErrNotExist) {
		return lk, nil
	} else if err != nil {
		return nil, fmt.Errorf("checking %s: %w", lockPath, err)
	}
	loaded, err := lock.Load(lockPath)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

type namedBase struct {
	name string
	base string
}

// registryBases maps the repo's registries (repo.yml priority order) to raw
// base URLs. Path sources are a dev escape hatch that sync does not support
// yet — error rather than silently skipping a priority registry.
func registryBases(cfg *config.RepoConfig) ([]namedBase, error) {
	var bases []namedBase
	for _, reg := range cfg.Registries {
		if reg.Types.Path != "" {
			return nil, fmt.Errorf("registry %q uses a local path source, which sync does not support yet", reg.Name)
		}
		base, err := rawBaseURL(reg.Types.Git)
		if err != nil {
			return nil, fmt.Errorf("registry %q: %w", reg.Name, err)
		}
		bases = append(bases, namedBase{name: reg.Name, base: base})
	}
	return bases, nil
}

// fetchWithFallback tries each registry in priority order; only a 404 moves
// on to the next. When every registry 404s, the error names the ref.
func fetchWithFallback(ctx context.Context, client *http.Client, bases []namedBase, ref typeref.TypeRef) (*syncpkg.FetchedType, string, error) {
	for _, b := range bases {
		ft, err := syncpkg.Fetch(ctx, client, b.base, ref)
		if errors.Is(err, syncpkg.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return ft, b.name, nil
	}
	return nil, "", fmt.Errorf("type %s not found in any configured registry (check name/version or that it's published)", ref)
}

type stagedInstall struct {
	path    string
	content []byte
}

// installRelPath is the cache path of one installed type, relative to
// ~/.smith/project-types.
func installRelPath(ref typeref.TypeRef) string {
	return filepath.FromSlash(fmt.Sprintf("%s@v%d.json", ref.Name, ref.Version))
}

// hashBytes renders the sha256 of data in the lock's "sha256:<hex>" form.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
