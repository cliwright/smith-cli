package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/manifest"
)

func project(dir, name, typ string) discovery.Project {
	return discovery.Project{
		Dir:      dir,
		Manifest: &manifest.Manifest{Name: name, Type: typ},
	}
}

// projectWithDeps builds a project whose manifest declares the given
// dependencies in sugar form — including the [build] defaults that
// manifest.Load fills in (defaultTarget is unexported there).
func projectWithDeps(dir, name, typ string, deps ...string) discovery.Project {
	dependsOn := make([]manifest.Dependency, len(deps))
	for i, dep := range deps {
		dependsOn[i] = manifest.Dependency{
			Project: dep,
			Targets: []string{"build"},
			Before:  []string{"build"},
		}
	}
	return discovery.Project{
		Dir:      dir,
		Manifest: &manifest.Manifest{Name: name, Type: typ, DependsOn: dependsOn},
	}
}

func TestRenderTree(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs", "services", "tools"}},
	}
	projects := []discovery.Project{
		project("libs/my-lib", "my-lib", "python/astral/lib@1"),
		project("services/payments/app", "payments", "python/astral/service@2"),
		project("services/payments/infra", "payments-infra", "tofu/std/module@1"),
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "smith-test-repo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `smith-test-repo
├── libs
│   └── my-lib   (python/astral/lib@1)
├── services
│   └── payments
│       ├── app     (python/astral/service@2)
│       └── infra   (tofu/std/module@1)
└── tools
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// TestRenderTreeDependencies covers the spam case from the user's repo: the
// same dependency declared twice (sugar + explicit) renders exactly once.
func TestRenderTreeDependencies(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs"}},
	}
	spam := discovery.Project{
		Dir: "libs/spam",
		Manifest: &manifest.Manifest{
			Name: "spam",
			Type: "python/astral/service@2",
			DependsOn: []manifest.Dependency{
				{Project: "foobar", Targets: []string{"build"}, Before: []string{"build"}},
				{Project: "foobar", Targets: []string{"test"}, Before: []string{"build"}},
			},
		},
	}
	projects := []discovery.Project{
		project("libs/foobar", "foobar", "python/astral/service@2"),
		spam,
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "smith-test-repo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `smith-test-repo
└── libs
    ├── foobar   (python/astral/service@2)
    └── spam     (python/astral/service@2 -> foobar)
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// TestRenderTreeMultiDeps covers several dependencies: sorted, comma-joined.
func TestRenderTreeMultiDeps(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs"}},
	}
	projects := []discovery.Project{
		project("libs/auth", "auth", "python/astral/lib@1"),
		project("libs/zebra", "zebra", "python/astral/lib@1"),
		projectWithDeps("libs/x", "x", "go/std/lib@1", "zebra", "auth"),
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "demo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `demo
└── libs
    ├── auth    (python/astral/lib@1)
    ├── x       (go/std/lib@1 -> auth, zebra)
    └── zebra   (python/astral/lib@1)
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// TestRenderTreeRepoTargets pins the synthetic repo node: it lists each
// attachment alias annotated with its type, after the directory roots.
func TestRenderTreeRepoTargets(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs"}},
		RepoTargets: map[string]config.RepoTarget{
			"python": {Type: "repo/uv/workspace@1"},
		},
	}
	projects := []discovery.Project{
		project("libs/alpha", "alpha", "mock/std/lib@1"),
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "demo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `demo
├── libs
│   └── alpha   (mock/std/lib@1)
└── repo
    └── python   (repo/uv/workspace@1)
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestRenderTreeProjectAtRoot(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs"}},
	}
	projects := []discovery.Project{
		project("libs", "libs", "python/astral/lib@1"),
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "demo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `demo
└── libs   (python/astral/lib@1)
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// buildMockRepo creates an initialized repo with the given declared roots and
// projects (dir -> {name, type}) and returns its path.
func buildMockRepo(t *testing.T, roots []string, projects map[string][2]string) string {
	t.Helper()
	repo := t.TempDir()
	var rootLines strings.Builder
	for _, r := range roots {
		fmt.Fprintf(&rootLines, "    - %s\n", r)
	}
	content := "version: 1\nname: mock\n" +
		"registries:\n" +
		"  - name: r\n" +
		"    types: {git: https://example.com/types}\n" +
		"    templates: {git: https://example.com/templates}\n" +
		"workspace:\n  project_roots:\n" + rootLines.String()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, nt := range projects {
		full := filepath.Join(repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf("version: 1\nname: %s\ntype: %s\n", nt[0], nt[1])
		if err := os.WriteFile(filepath.Join(full, "smith.yml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestTreeCommandEndToEnd(t *testing.T) {
	repo := buildMockRepo(t,
		[]string{"libs", "services", "tools"},
		map[string][2]string{
			"libs/my-lib":             {"my-lib", "python/astral/lib@1"},
			"services/payments/app":   {"payments", "python/astral/service@2"},
			"services/payments/infra": {"payments-infra", "tofu/std/module@1"},
		})
	t.Chdir(repo)

	out, err := run(t, "tree")
	if err != nil {
		t.Fatalf("tree: %v\noutput:\n%s", err, out)
	}

	want := fmt.Sprintf(`%s
├── libs
│   └── my-lib   (python/astral/lib@1)
├── services
│   └── payments
│       ├── app     (python/astral/service@2)
│       └── infra   (tofu/std/module@1)
└── tools
`, filepath.Base(repo))
	if out != want {
		t.Errorf("tree output mismatch:\n%s\nwant:\n%s", out, want)
	}
}

// writeSpamRepo builds an initialized mock repo with libs/foobar and
// libs/spam, where spam declares foobar twice: sugar form plus explicit
// {targets: [test], before: [build]}. It returns the repo path.
func writeSpamRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"registries:\n" +
		"  - name: r\n" +
		"    types: {git: https://example.com/types}\n" +
		"    templates: {git: https://example.com/templates}\n" +
		"workspace:\n  project_roots:\n    - libs\n"
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}

	foobar := filepath.Join(repo, "libs", "foobar")
	if err := os.MkdirAll(foobar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foobar, "smith.yml"), []byte("version: 1\nname: foobar\ntype: python/astral/service@2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spam := filepath.Join(repo, "libs", "spam")
	if err := os.MkdirAll(spam, 0o755); err != nil {
		t.Fatal(err)
	}
	spamYML := "version: 1\nname: spam\ntype: python/astral/service@2\n" +
		"depends_on:\n" +
		"  - foobar\n" +
		"  - project: foobar\n" +
		"    targets: [test]\n" +
		"    before: [build]\n"
	if err := os.WriteFile(filepath.Join(spam, "smith.yml"), []byte(spamYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestTreeCommandDependencies proves the dependency annotation flows from an
// on-disk smith.yml through discovery into the rendered tree.
func TestTreeCommandDependencies(t *testing.T) {
	repo := writeSpamRepo(t)
	t.Chdir(repo)
	out, err := run(t, "tree")
	if err != nil {
		t.Fatalf("tree: %v\noutput:\n%s", err, out)
	}

	want := fmt.Sprintf(`%s
└── libs
    ├── foobar   (python/astral/service@2)
    └── spam     (python/astral/service@2 -> foobar)
`, filepath.Base(repo))
	if out != want {
		t.Errorf("tree output mismatch:\n%s\nwant:\n%s", out, want)
	}
}

// TestTreeCommandShowDeps covers --show-deps end to end: the tree is
// unchanged and the Dependencies section spells out both foobar edges,
// including the sugar entry's build → build defaults.
func TestTreeCommandShowDeps(t *testing.T) {
	repo := writeSpamRepo(t)
	t.Chdir(repo)
	out, err := run(t, "tree", "--show-deps")
	if err != nil {
		t.Fatalf("tree --show-deps: %v\noutput:\n%s", err, out)
	}

	want := fmt.Sprintf(`%s
└── libs
    ├── foobar   (python/astral/service@2)
    └── spam     (python/astral/service@2 -> foobar)

Dependencies:
  spam → foobar   build → build
  spam → foobar   test → build
`, filepath.Base(repo))
	if out != want {
		t.Errorf("tree --show-deps output mismatch:\n%s\nwant:\n%s", out, want)
	}
}

// TestTreeCommandShowDepsWithoutFlag proves the default output has no
// Dependencies section.
func TestTreeCommandShowDepsWithoutFlag(t *testing.T) {
	repo := writeSpamRepo(t)
	t.Chdir(repo)
	out, err := run(t, "tree")
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if strings.Contains(out, "Dependencies:") {
		t.Errorf("default tree output contains a Dependencies section:\n%s", out)
	}
}

// TestRenderDepsSorted covers the section's ordering across projects:
// project, then dependency, then targets.
func TestRenderDepsSorted(t *testing.T) {
	projects := []discovery.Project{
		projectWithDeps("services/web", "web", "python/astral/service@2",
			"database"),
		projectWithDeps("libs/auth", "auth", "python/astral/lib@1", "util"),
		project("libs/util", "util", "python/astral/lib@1"),
	}
	// web → database is explicit: migrate on the dependency before build here.
	projects[0].Manifest.DependsOn[0] = manifest.Dependency{
		Project: "database", Targets: []string{"migrate"}, Before: []string{"build"},
	}

	var buf bytes.Buffer
	renderDeps(&buf, projects)

	want := `
Dependencies:
  auth → util   build → build
  web → database   migrate → build
`
	if buf.String() != want {
		t.Errorf("renderDeps output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// TestRenderDepsNoDeps proves the section is omitted when no project has
// dependencies — the flag then changes nothing.
func TestRenderDepsNoDeps(t *testing.T) {
	projects := []discovery.Project{
		project("libs/a", "a", "python/astral/lib@1"),
		project("libs/b", "b", "python/astral/lib@1"),
	}
	var buf bytes.Buffer
	renderDeps(&buf, projects)
	if buf.Len() != 0 {
		t.Errorf("renderDeps with no deps = %q, want empty", buf.String())
	}
}

func TestTreeNotARepo(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := run(t, "tree")
	if err == nil {
		t.Fatal("tree outside a repo: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not a Smith repository") {
		t.Errorf("error = %v, want 'not a Smith repository'", err)
	}
}

// TestOutputChannels locks in the discipline that normal output goes to
// stdout and errors/diagnostics go to stderr, with separate buffers.
func TestOutputChannels(t *testing.T) {
	t.Run("tree output on stdout", func(t *testing.T) {
		repo := buildMockRepo(t, []string{"libs"}, map[string][2]string{
			"libs/only": {"only", "python/astral/lib@1"},
		})
		t.Chdir(repo)
		cmd := newRootCmd()
		var so, se bytes.Buffer
		cmd.SetOut(&so)
		cmd.SetErr(&se)
		cmd.SetArgs([]string{"tree"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("tree: %v", err)
		}
		if !strings.Contains(so.String(), "libs") {
			t.Errorf("stdout missing tree output:\n%s", so.String())
		}
		if se.Len() != 0 {
			t.Errorf("stderr = %q, want empty", se.String())
		}
	})

	t.Run("error on stderr", func(t *testing.T) {
		t.Chdir(t.TempDir())
		cmd := newRootCmd()
		var so, se bytes.Buffer
		cmd.SetOut(&so)
		cmd.SetErr(&se)
		cmd.SetArgs([]string{"tree"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("tree outside a repo: want error, got nil")
		}
		if !strings.Contains(se.String(), "not a Smith repository") {
			t.Errorf("stderr missing error:\n%s", se.String())
		}
		if so.Len() != 0 {
			t.Errorf("stdout = %q, want empty on failure", so.String())
		}
	})
}
