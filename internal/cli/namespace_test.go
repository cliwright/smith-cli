package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/runner"
	"github.com/cliwright/smith/internal/targetrun"
	"github.com/cliwright/smith/internal/typeref"
)

// fakeExec records every step instead of running it.
type fakeExec struct {
	runs   []string
	dirs   []string
	failOn string
}

var _ targetrun.Runner = (*fakeExec)(nil)

func (f *fakeExec) Run(_ context.Context, opts runner.Options, _ string, args ...string) error {
	step := args[1]
	f.runs = append(f.runs, step)
	f.dirs = append(f.dirs, opts.Dir)
	if step == f.failOn {
		return errors.New("exited with status 7")
	}
	return nil
}

func useFakeExec(t *testing.T, f *fakeExec) {
	t.Helper()
	old := nsExecRunner
	nsExecRunner = f
	t.Cleanup(func() { nsExecRunner = old })
}

type manifestSpec struct {
	name string
	typ  string
	deps []manifest.Dependency
}

type typeSpec struct {
	targets map[string][]string
	deps    map[string][]string
	wd      projecttype.WorkingDir
	tools   []string
}

// writeDispatchRepo builds an initialized repo with the given manifests and
// installs (into the injected home) the types they reference, plus a
// matching lock. All types are pinned as registry "cliwright". The optional
// final argument declares repo_targets (attachments) and installs their
// types too.
func writeDispatchRepo(t *testing.T, home, repo string, manifests map[string]manifestSpec, typeOverrides map[string]typeSpec, repoTargets ...map[string]config.RepoTarget) {
	t.Helper()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"registries:\n" +
		"  - name: cliwright\n" +
		"    types: {git: https://github.com/cliwright/types}\n" +
		"    templates: {git: https://github.com/cliwright/templates}\n" +
		"workspace:\n  project_roots:\n    - libs\n    - services\n"
	if len(repoTargets) > 0 && repoTargets[0] != nil {
		repoYML += renderRepoTargets(repoTargets[0])
	}
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}

	pins := map[string]lock.TypePin{}
	install := func(refS string, override *typeSpec) {
		ref, err := typeref.Parse(refS)
		if err != nil {
			t.Fatal(err)
		}
		if _, done := pins[ref.String()]; done {
			return
		}
		ts := typeSpec{
			targets: map[string][]string{"build": {"echo build"}},
			wd:      projecttype.WorkingDirProject,
			tools:   []string{"sh"},
		}
		if override != nil {
			ts = *override
		}
		tools := ts.tools
		if tools == nil {
			tools = []string{"sh"}
		}
		pt := &projecttype.ProjectType{
			Name:        ref.Name,
			Version:     ref.Version,
			Description: "test type",
			Tools:       tools,
			WorkingDir:  ts.wd,
			Targets:     ts.targets,
			DependsOn:   ts.deps,
		}
		data, err := projecttype.Marshal(pt)
		if err != nil {
			t.Fatal(err)
		}
		installPath := filepath.Join(home, cacheDirName, "project-types", installRelPath(ref))
		if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(installPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		pins[ref.String()] = lock.TypePin{Registry: "cliwright", Hash: hashBytes(data)}
	}
	for dir, spec := range manifests {
		full := filepath.Join(repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "smith.yml"), []byte(renderManifest(spec)), 0o644); err != nil {
			t.Fatal(err)
		}
		install(spec.typ, typeSpecPtr(typeOverrides, spec.typ))
	}
	if len(repoTargets) > 0 && repoTargets[0] != nil {
		for _, rt := range repoTargets[0] {
			install(rt.Type, typeSpecPtr(typeOverrides, rt.Type))
		}
	}

	lk := &lock.Lock{Version: 1, Types: pins}
	if err := lock.Save(filepath.Join(smithDir, "lock.json"), lk); err != nil {
		t.Fatal(err)
	}
}

func renderRepoTargets(rts map[string]config.RepoTarget) string {
	aliases := make([]string, 0, len(rts))
	for alias := range rts {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	var b strings.Builder
	b.WriteString("repo_targets:\n")
	for _, alias := range aliases {
		rt := rts[alias]
		fmt.Fprintf(&b, "  %s:\n    type: %s\n", alias, rt.Type)
		if len(rt.Params) > 0 {
			b.WriteString("    params:\n")
			for _, key := range sortedStringKeys(rt.Params) {
				fmt.Fprintf(&b, "      %s: %q\n", key, rt.Params[key])
			}
		}
		if len(rt.Environment) > 0 {
			b.WriteString("    environment:\n")
			for _, key := range sortedStringKeys(rt.Environment) {
				fmt.Fprintf(&b, "      %s: %q\n", key, rt.Environment[key])
			}
		}
	}
	return b.String()
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// typeSpecPtr returns a pointer to the override for ref, or nil when the
// fixture uses the default type shape.
func typeSpecPtr(overrides map[string]typeSpec, ref string) *typeSpec {
	if ts, ok := overrides[ref]; ok {
		return &ts
	}
	return nil
}

func renderManifest(spec manifestSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nname: %s\ntype: %s\n", spec.name, spec.typ)
	if len(spec.deps) > 0 {
		b.WriteString("depends_on:\n")
		for _, d := range spec.deps {
			if len(d.Targets) == 1 && d.Targets[0] == "build" && len(d.Before) == 1 && d.Before[0] == "build" {
				fmt.Fprintf(&b, "  - %s\n", d.Project)
				continue
			}
			fmt.Fprintf(&b, "  - project: %s\n", d.Project)
			fmt.Fprintf(&b, "    targets: [%s]\n", strings.Join(d.Targets, ", "))
			fmt.Fprintf(&b, "    before: [%s]\n", strings.Join(d.Before, ", "))
		}
	}
	return b.String()
}

func twoLibsFixture(t *testing.T, home, repo string) {
	t.Helper()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/beta":  {name: "beta", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo alpha-or-beta-build"}, "test": {"echo test"}},
			deps:    map[string][]string{"build": {"test"}},
		},
	})
}

func TestNsListProjects(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	for _, args := range [][]string{{"libs"}, {"libs", "list"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != "alpha\nbeta\n" {
			t.Errorf("%v output = %q, want alpha/beta", args, out)
		}
	}
}

func TestNsListTargets(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "list")
	if err != nil {
		t.Fatalf("libs alpha list: %v", err)
	}
	if out != "build\ntest\n" {
		t.Errorf("targets = %q, want build/test sorted", out)
	}
}

func TestNsRunSingleRunsClosure(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/beta": {name: "beta", typ: "mock/std/lib@1",
			deps: []manifest.Dependency{{Project: "alpha", Targets: []string{"build"}, Before: []string{"build"}}}},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo build"}, "test": {"echo test"}},
			deps:    map[string][]string{"build": {"test"}},
		},
	})
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "libs", "beta", "build")
	if err != nil {
		t.Fatalf("libs beta build: %v\noutput:\n%s", err, out)
	}
	want := []string{"echo test", "echo build", "echo test", "echo build"}
	if !slices.Equal(f.runs, want) {
		t.Errorf("steps = %v, want closure order %v (both projects share the type, so beta's own test runs before its build)", f.runs, want)
	}
	// alpha's own test runs before alpha:build; beta:build last. Headers show
	// which node each step belongs to.
	if !strings.Contains(out, "==> alpha:test") || !strings.Contains(out, "==> beta:build") {
		t.Errorf("output missing node headers:\n%s", out)
	}
}

func TestNsRunNsWideSkipsAndReports(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/beta":  {name: "beta", typ: "mock/std/other@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"test": {"echo test"}},
		},
		"mock/std/other@1": {
			targets: map[string][]string{"build": {"echo build"}},
		},
	})
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "libs", "test")
	if err != nil {
		t.Fatalf("libs test: %v\noutput:\n%s", err, out)
	}
	if !slices.Equal(f.runs, []string{"echo test"}) {
		t.Errorf("steps = %v, want only alpha's test", f.runs)
	}
	if !strings.Contains(out, "ran 1, skipped 1 (type has no 'test' target)") {
		t.Errorf("output missing skip summary:\n%s", out)
	}
}

func TestRunRepoWide(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "build"); err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(f.runs) != 4 { // alpha: test+build, beta: test+build
		t.Errorf("steps = %v (%d), want 4", f.runs, len(f.runs))
	}
}

func TestTargetWinsOverProjectName(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	// A project NAMED "test" whose type also declares a "test" target.
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/test":  {name: "test", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"test": {"echo test-run"}, "build": {"echo build"}},
		},
	})
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	// `smith libs test` runs the TARGET on every project, not the project.
	out, err := run(t, "libs", "test")
	if err != nil {
		t.Fatalf("libs test: %v\noutput:\n%s", err, out)
	}
	if len(f.runs) != 2 || !slices.Equal(f.runs, []string{"echo test-run", "echo test-run"}) {
		t.Errorf("steps = %v, want the test target on every project", f.runs)
	}

	// `smith libs test list` reaches the project named "test".
	out, err = run(t, "libs", "test", "list")
	if err != nil {
		t.Fatalf("libs test list: %v", err)
	}
	if out != "build\ntest\n" {
		t.Errorf("targets of project test = %q", out)
	}
}

func TestNsUnknownProjectListsExisting(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "nosuch", "list")
	if err == nil || !strings.Contains(err.Error(), `unknown project "nosuch" in namespace "libs"`) ||
		!strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("error = %v, want unknown-project listing", err)
	}
}

func TestNsUnknownTargetOnProject(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "alpha", "nosuch")
	if err == nil || !strings.Contains(err.Error(), `has no target "nosuch"`) ||
		!strings.Contains(err.Error(), "build, test") {
		t.Fatalf("error = %v, want unknown-target listing", err)
	}
}

func TestNsNoProjectHasTarget(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "lint")
	if err == nil || !strings.Contains(err.Error(), `no project in namespace "libs" has a "lint" target`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNsParallelWarning(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "--parallel", "4", "libs", "alpha", "test")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "warning: parallel execution not implemented yet; running serially") {
		t.Errorf("output missing parallel warning:\n%s", out)
	}
	if !slices.Equal(f.runs, []string{"echo test"}) {
		t.Errorf("steps = %v, want serial execution regardless", f.runs)
	}
}

func TestNsLockMismatchIsHardError(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)

	// Tamper with the installed type after the lock was written.
	installPath := filepath.Join(home, cacheDirName, "project-types", filepath.FromSlash("mock/std/lib@v1.json"))
	if err := os.WriteFile(installPath, []byte(`{"name":"mock/lib"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "libs", "alpha", "test")
	if err == nil || !strings.Contains(err.Error(), "failed hash verification") ||
		!strings.Contains(err.Error(), "smith sync") {
		t.Fatalf("error = %v, want hash-verification hard error", err)
	}
}

func TestNsMissingLockIsHardError(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	twoLibsFixture(t, home, repo)
	t.Chdir(repo)
	if err := os.Remove(filepath.Join(repo, ".smith", "lock.json")); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "libs", "alpha", "test")
	if err == nil || !strings.Contains(err.Error(), "run smith sync") {
		t.Fatalf("error = %v, want missing-lock error", err)
	}
}

func TestNsCycleErrorSurfaces(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo b"}, "test": {"echo t"}},
			deps:    map[string][]string{"build": {"test"}, "test": {"build"}},
		},
	})
	t.Chdir(repo)

	_, err := run(t, "libs", "alpha", "build")
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("error = %v, want cycle error", err)
	}
}

// TestNsRealExecution runs real shell steps end to end: order, closure,
// fail-fast, and stdio come from the actual runner.
func TestNsRealExecution(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	markers := filepath.Join(t.TempDir(), "markers")
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/beta": {name: "beta", typ: "mock/std/lib@1",
			deps: []manifest.Dependency{{Project: "alpha", Targets: []string{"build"}, Before: []string{"build"}}}},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{
				"build": {fmt.Sprintf(`echo "$(basename "$PWD"):build" >> %s`, markers)},
				"test":  {fmt.Sprintf(`echo "$(basename "$PWD"):test" >> %s`, markers)},
			},
			deps: map[string][]string{"build": {"test"}},
		},
	})
	t.Chdir(repo)

	out, err := run(t, "libs", "beta", "build")
	if err != nil {
		t.Fatalf("libs beta build: %v\noutput:\n%s", err, out)
	}
	data, err := os.ReadFile(markers)
	if err != nil {
		t.Fatalf("markers file: %v", err)
	}
	want := "alpha:test\nalpha:build\nbeta:test\nbeta:build\n" // shared type: each project's build closes over its own test
	if string(data) != want {
		t.Errorf("markers = %q, want %q", data, want)
	}
}

var workspaceTarget = map[string]config.RepoTarget{
	"python": {Type: "repo/uv/workspace@1"},
}

var workspaceTypeSpec = map[string]typeSpec{
	"repo/uv/workspace@1": {
		targets: map[string][]string{
			"setup": {"echo setup", "echo sync"},
			"clean": {"echo clean"},
		},
		wd: projecttype.WorkingDirProject,
	},
}

func TestRepoListsAttachments(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, nil, workspaceTarget)
	t.Chdir(repo)

	for _, args := range [][]string{{"repo"}, {"repo", "list"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != "python\n" {
			t.Errorf("%v output = %q, want the attachment alias", args, out)
		}
	}
}

func TestRepoListsAttachmentTargets(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, workspaceTypeSpec, workspaceTarget)
	t.Chdir(repo)

	out, err := run(t, "repo", "python")
	if err != nil {
		t.Fatalf("repo python: %v", err)
	}
	if out != "clean\nsetup\n" {
		t.Errorf("targets = %q, want clean/setup sorted", out)
	}

	// "list" is reserved in target position, mirroring project namespaces.
	out, err = run(t, "repo", "python", "list")
	if err != nil {
		t.Fatalf("repo python list: %v", err)
	}
	if out != "clean\nsetup\n" {
		t.Errorf("targets via list = %q, want clean/setup sorted", out)
	}
}

func TestRepoRunsAttachmentAtRepoRoot(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, workspaceTypeSpec, workspaceTarget)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "repo", "python", "setup")
	if err != nil {
		t.Fatalf("repo python setup: %v\noutput:\n%s", err, out)
	}
	if !slices.Equal(f.runs, []string{"echo setup", "echo sync"}) {
		t.Errorf("steps = %v", f.runs)
	}
	for _, dir := range f.dirs {
		if dir != repo {
			t.Errorf("attachment step ran in %q, want repo root %q", dir, repo)
		}
	}
}

func TestRepoUnknownAliasListsKnown(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, workspaceTypeSpec, workspaceTarget)
	t.Chdir(repo)

	_, err := run(t, "repo", "nosuch", "setup")
	if err == nil || !strings.Contains(err.Error(), `unknown repo target "nosuch"`) ||
		!strings.Contains(err.Error(), "python") {
		t.Fatalf("error = %v, want unknown-alias error listing aliases", err)
	}
}

func TestRepoUnknownTargetListsTargets(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, workspaceTypeSpec, workspaceTarget)
	t.Chdir(repo)

	_, err := run(t, "repo", "python", "nosuch")
	if err == nil || !strings.Contains(err.Error(), `has no target "nosuch"`) ||
		!strings.Contains(err.Error(), "clean, setup") {
		t.Fatalf("error = %v, want unknown-target error listing targets", err)
	}
}

func TestRepoNoAttachments(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, nil)
	t.Chdir(repo)

	out, err := run(t, "repo")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	if !strings.Contains(out, "no repo targets defined") ||
		!strings.Contains(out, "repo_targets") {
		t.Errorf("output = %q, want helpful no-attachments message", out)
	}
}

// TestRepoReservedNamespaceShadowsRoot: "repo" dispatches to attachments
// even when a project_root directory is literally named "repo".
func TestRepoReservedNamespaceShadowsRoot(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"repo/inner": {name: "inner", typ: "mock/std/lib@1"},
	}, nil, workspaceTarget)
	// Declare a "repo" project root (shadowed by the synthetic namespace).
	repoYMLPath := filepath.Join(repo, ".smith", "repo.yml")
	data, err := os.ReadFile(repoYMLPath)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), "    - services\n", "    - services\n    - repo\n", 1)
	if replaced == string(data) {
		t.Fatal("test setup: - services root not found in repo.yml")
	}
	if err := os.WriteFile(repoYMLPath, []byte(replaced), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, err := run(t, "repo")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	if out != "python\n" {
		t.Errorf("output = %q, want attachments (synthetic namespace wins)", out)
	}
}
