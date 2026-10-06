// Package discovery locates the enclosing Smith repository and the projects
// inside it. Smith only ever traverses the workspace.project_roots declared
// in .smith/repo.yml — never the repository at large.
package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/manifest"
)

// notARepoMessage is returned by FindRepoRoot when no ancestor contains
// .smith/repo.yml.
const notARepoMessage = "not a Smith repository (no .smith/repo.yml found in this directory or any parent); run smith init"

// skipDirs are never descended into during project discovery. They can exist
// inside project directories (node_modules, .venv, …), so the skip check runs
// on every directory visited.
var skipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".tox":          true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	"dist":          true,
	"target":        true,
}

// Project is one discovered project: its directory relative to the repo root
// (slash-separated) and its parsed manifest.
type Project struct {
	Dir      string
	Manifest *manifest.Manifest
}

// FindRepoRoot walks up from startDir to the nearest ancestor containing
// .smith/repo.yml. It returns an error mentioning smith init when there is
// none.
func FindRepoRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", startDir, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".smith", "repo.yml")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New(notARepoMessage)
		}
		dir = parent
	}
}

// FindProjects discovers every project under the repo's declared
// workspace.project_roots, in deterministic (sorted) order. A root that does
// not exist on disk is skipped silently. Duplicate project names across the
// repo are an error naming both directories.
func FindProjects(repoRoot string, cfg *config.RepoConfig) ([]Project, error) {
	var projects []Project
	seen := map[string]string{} // project name -> relative dir

	roots := slices.Clone(cfg.Workspace.ProjectRoots)
	sort.Strings(roots)

	for _, root := range roots {
		rootAbs := filepath.Join(repoRoot, filepath.FromSlash(root))
		info, err := os.Stat(rootAbs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", rootAbs, err)
		}
		if !info.IsDir() {
			continue
		}

		err = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if path != rootAbs && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			smithYML := filepath.Join(path, "smith.yml")
			if _, err := os.Stat(smithYML); errors.Is(err, os.ErrNotExist) {
				return nil
			} else if err != nil {
				return fmt.Errorf("checking %s: %w", smithYML, err)
			}
			m, err := manifest.Load(smithYML)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return fmt.Errorf("relativizing %s: %w", path, err)
			}
			rel = filepath.ToSlash(rel)
			if prev, dup := seen[m.Name]; dup {
				return fmt.Errorf("duplicate project name %q: %s and %s", m.Name, prev, rel)
			}
			seen[m.Name] = rel
			projects = append(projects, Project{Dir: rel, Manifest: m})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", rootAbs, err)
		}
	}

	sort.Slice(projects, func(i, j int) bool { return projects[i].Dir < projects[j].Dir })
	if err := validateDependencies(projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// validateDependencies checks every project's depends_on entries against the
// discovered projects: no dangling references, no self-dependencies. projects
// must be sorted so the first error reported is deterministic.
func validateDependencies(projects []Project) error {
	names := make(map[string]bool, len(projects))
	for _, p := range projects {
		names[p.Manifest.Name] = true
	}
	for _, p := range projects {
		manifestPath := filepath.ToSlash(filepath.Join(p.Dir, "smith.yml"))
		for _, dep := range p.Manifest.DependsOn {
			if dep.Project == p.Manifest.Name {
				return fmt.Errorf("%s: project %q must not depend on itself", manifestPath, p.Manifest.Name)
			}
			if !names[dep.Project] {
				return fmt.Errorf("%s: dependency %q does not match any project in this repository", manifestPath, dep.Project)
			}
		}
	}
	return nil
}
