package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/lock"
)

func artifactPaths(dir string) (repoYML, lockJSON string) {
	return filepath.Join(dir, cacheDirName, repoYMLName),
		filepath.Join(dir, cacheDirName, lockJSONName)
}

func TestInitFreshDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	out, err := run(t, "init")
	if err != nil {
		t.Fatalf("init: %v\noutput:\n%s", err, out)
	}

	repoYML, lockJSON := artifactPaths(dir)
	for _, p := range []string{repoYML, lockJSON} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("artifact %s missing: %v", p, err)
		}
	}

	// repo.yml loads through internal/config and names the directory.
	cfg, err := config.Load(repoYML)
	if err != nil {
		t.Fatalf("config.Load(%q): %v", repoYML, err)
	}
	if cfg.Name != filepath.Base(dir) {
		t.Errorf("repo name = %q, want directory basename %q", cfg.Name, filepath.Base(dir))
	}
	if !slices.Equal(cfg.Workspace.ProjectRoots, []string{"libs", "tools", "services", "images"}) {
		t.Errorf("project_roots = %v", cfg.Workspace.ProjectRoots)
	}
	if !slices.Equal(cfg.Tools, []string{"cookiecutter"}) {
		t.Errorf("tools = %v, want [cookiecutter]", cfg.Tools)
	}

	// The template's explanatory comments made it into the file.
	data, err := os.ReadFile(repoYML)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{
		"# .smith/repo.yml — Smith repository configuration",
		"# Registry sources, in priority order: first match wins when a type or",
		"# Tools required to work in this repo, checked by `smith doctor` against PATH",
		"# Smith ONLY traverses these directories looking for smith.yml projects —",
	} {
		if !strings.Contains(string(data), comment) {
			t.Errorf("repo.yml missing comment %q", comment)
		}
	}

	// lock.json loads through internal/lock: nothing pinned yet.
	lk, err := lock.Load(lockJSON)
	if err != nil {
		t.Fatalf("lock.Load(%q): %v", lockJSON, err)
	}
	if len(lk.Types) != 0 {
		t.Errorf("pinned types = %v, want none", lk.Types)
	}

	// Summary mentions what was created and what to do next.
	for _, want := range []string{"Initialized Smith repository", repoYMLName, lockJSONName, "commit .smith/", "smith sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %q:\n%s", want, out)
		}
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	repoYML, lockJSON := artifactPaths(dir)

	if _, err := run(t, "init"); err != nil {
		t.Fatalf("first init: %v", err)
	}
	repoBefore, err := os.ReadFile(repoYML)
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(lockJSON)
	if err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "init")
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if !strings.Contains(out, "Nothing to do") {
		t.Errorf("second init output = %q, want a no-op message", out)
	}

	repoAfter, err := os.ReadFile(repoYML)
	if err != nil {
		t.Fatal(err)
	}
	lockAfter, err := os.ReadFile(lockJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repoAfter, repoBefore) {
		t.Errorf("second init modified %s", repoYML)
	}
	if !bytes.Equal(lockAfter, lockBefore) {
		t.Errorf("second init modified %s", lockJSON)
	}
}
