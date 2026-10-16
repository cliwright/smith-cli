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
	envs   [][]string
	failOn string
}

var _ targetrun.Runner = (*fakeExec)(nil)

func (f *fakeExec) Run(_ context.Context, opts runner.Options, _ string, args ...string) error {
	step := args[1]
	f.runs = append(f.runs, step)
	f.dirs = append(f.dirs, opts.Dir)
	f.envs = append(f.envs, opts.Env)
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
	name   string
	typ    string   // single type ref, written as the sugar scalar form
	types  []string // several type refs, written as the array form (overrides typ when set)
	deps   []manifest.Dependency
	dotenv bool // opt the project into dotenv loading (smith.yml dotenv: true)
	params map[string]string
}

type typeSpec struct {
	targets map[string][]string
	descs   map[string]string // short descriptions per target name (optional)
	deps    map[string][]string
	wd      projecttype.WorkingDir
	tools   []string
	env     map[string]string // type-level environment entries
	params  map[string]projecttype.Param
	files   map[string]string // extra files packed into the installed ingot (e.g. help/*.md)
}

// writeDispatchRepo builds an initialized repo with the given manifests and
// installs (into the repo's .smith/types) the types they reference, plus a
// matching lock. All types are pinned as registry "cliwright". The optional
// final argument declares repo_targets (attachments) and installs their
// types too.
func writeDispatchRepo(t *testing.T, repo string, manifests map[string]manifestSpec, typeOverrides map[string]typeSpec, repoTargets ...map[string]config.RepoTarget) {
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
			Targets:     targetsFromSteps(ts.targets, ts.descs),
			DependsOn:   ts.deps,
			Environment: ts.env,
			Params:      ts.params,
		}
		data, err := projecttype.Marshal(pt)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{"project-type.json": string(data)}
		for name, content := range ts.files {
			files[name] = content
		}
		hash := installIngotForTest(t, repo, ref, files)
		pins[ref.String()] = lock.TypePin{Registry: "cliwright", Hash: hash}
	}
	for dir, spec := range manifests {
		full := filepath.Join(repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "smith.yml"), []byte(renderManifest(spec)), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, ref := range spec.refs() {
			install(ref, typeSpecPtr(typeOverrides, ref))
		}
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

// typeSpecPtr returns a pointer to the override for ref, or nil when the
// fixture uses the default type shape.
func typeSpecPtr(overrides map[string]typeSpec, ref string) *typeSpec {
	if ts, ok := overrides[ref]; ok {
		return &ts
	}
	return nil
}

// targetsFromSteps adapts the fixture's bare step lists to typed targets,
// attaching the optional per-target short descriptions.
func targetsFromSteps(steps map[string][]string, descs map[string]string) map[string]projecttype.Target {
	out := make(map[string]projecttype.Target, len(steps))
	for name, ss := range steps {
		out[name] = projecttype.Target{Steps: ss, Description: descs[name]}
	}
	return out
}

// refs lists every type ref the spec declares: the array form when set, the
// scalar ref otherwise.
func (s manifestSpec) refs() []string {
	if len(s.types) > 0 {
		return s.types
	}
	return []string{s.typ}
}

func renderManifest(spec manifestSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nname: %s\n", spec.name)
	if len(spec.types) > 0 {
		b.WriteString("type:\n")
		for _, ref := range spec.types {
			fmt.Fprintf(&b, "  - %s\n", ref)
		}
	} else {
		fmt.Fprintf(&b, "type: %s\n", spec.typ)
	}
	if spec.dotenv {
		b.WriteString("dotenv: true\n")
	}
	if len(spec.params) > 0 {
		b.WriteString("params:\n")
		for _, key := range sortedStringKeys(spec.params) {
			fmt.Fprintf(&b, "  %s: %q\n", key, spec.params[key])
		}
	}
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

func twoLibsFixture(t *testing.T, repo string) {
	t.Helper()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	twoLibsFixture(t, repo)
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
	repo := t.TempDir()
	twoLibsFixture(t, repo)
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	twoLibsFixture(t, repo)
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
	repo := t.TempDir()
	// A project NAMED "test" whose type also declares a "test" target.
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "nosuch", "list")
	if err == nil || !strings.Contains(err.Error(), `unknown project "nosuch" in namespace "libs"`) ||
		!strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("error = %v, want unknown-project listing", err)
	}
}

func TestNsUnknownTargetOnProject(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "alpha", "nosuch")
	if err == nil || !strings.Contains(err.Error(), `has no target "nosuch"`) ||
		!strings.Contains(err.Error(), "build, test") {
		t.Fatalf("error = %v, want unknown-target listing", err)
	}
}

func TestNsNoProjectHasTarget(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "lint")
	if err == nil || !strings.Contains(err.Error(), `no project in namespace "libs" has a "lint" target`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNsParallelWarning(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
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

// TestNsStepRendersAndExportsSmithTypeDir: steps template {{.SMITH_TYPE_DIR}}
// against the installed type directory, and the same path is exported to the
// step environment for shell-side use.
func TestNsStepRendersAndExportsSmithTypeDir(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo,
		map[string]manifestSpec{"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"}},
		map[string]typeSpec{"mock/std/lib@1": {
			targets: map[string][]string{"build": {`echo {{.SMITH_TYPE_DIR}}`}},
		}},
	)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "libs", "alpha", "build"); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantDir := filepath.Join(repo, ".smith", "types", "mock", "std", "lib@v1")
	if !slices.Equal(f.runs, []string{"echo " + wantDir}) {
		t.Errorf("runs = %v, want the step rendered with SMITH_TYPE_DIR", f.runs)
	}
	if len(f.envs) != 1 || !slices.Contains(f.envs[0], "SMITH_TYPE_DIR="+wantDir) {
		t.Errorf("step env missing SMITH_TYPE_DIR export: %v", f.envs)
	}
}

func TestNsLockMismatchIsHardError(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	// Tamper with the unpacked tree after the lock was written: the tree
	// drifts from the ingot, which is a hard error pointing at smith sync.
	installPath := filepath.Join(installedDir(t, repo, "mock/std/lib@v1"), "project-type.json")
	if err := os.WriteFile(installPath, []byte(`{"name":"mock/lib"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "libs", "alpha", "test")
	if err == nil || !strings.Contains(err.Error(), "drifted from the ingot") ||
		!strings.Contains(err.Error(), "smith sync") {
		t.Fatalf("error = %v, want custody hard error", err)
	}
}

func TestNsMissingLockIsHardError(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	markers := filepath.Join(t.TempDir(), "markers")
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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

func TestNsListTargetsAnnotatedWithDescriptions(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{
				"build": {"echo build"},
				"lint":  {"echo lint"},
				"clean": {"echo clean"},
			},
			descs: map[string]string{
				"build": "build the wheel",
				"lint":  "run the ruff linter",
			},
		},
	})
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "list")
	if err != nil {
		t.Fatalf("libs alpha list: %v\noutput:\n%s", err, out)
	}
	// Described targets pad to a common column and carry " -- description";
	// bare targets stay single-column.
	want := "build -- build the wheel\nclean\nlint  -- run the ruff linter\n"
	if out != want {
		t.Errorf("listing = %q, want %q", out, want)
	}
}

// multiTypeFixture builds a repo with one project composed of two disjoint
// types: mock/std/lib (build/test) and mock/std/docs (docs, described).
func multiTypeFixture(t *testing.T, repo string) {
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", types: []string{"mock/std/lib@1", "mock/std/docs@1"}},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo lib-build"}, "test": {"echo lib-test"}},
		},
		"mock/std/docs@1": {
			targets: map[string][]string{"docs": {"echo docs"}},
			descs:   map[string]string{"docs": "build the docs"},
		},
	})
}

// TestNsListTargetsMultiTypeGroupsByType: a multi-type project's target list
// groups targets under per-type headings (blank-line separated), padding per
// section; single-type output stays flat.
func TestNsListTargetsMultiTypeGroupsByType(t *testing.T) {
	repo := t.TempDir()
	multiTypeFixture(t, repo)
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "list")
	if err != nil {
		t.Fatalf("libs alpha list: %v\noutput:\n%s", err, out)
	}
	want := "mock/std/lib@1\n\nbuild\ntest\n\nmock/std/docs@1\n\ndocs -- build the docs\n"
	if out != want {
		t.Errorf("multi-type listing = %q, want %q", out, want)
	}

	// The single-type project shape is untouched.
	repo2 := t.TempDir()
	twoLibsFixture(t, repo2)
	t.Chdir(repo2)
	out, err = run(t, "libs", "alpha", "list")
	if err != nil {
		t.Fatalf("single-type list: %v", err)
	}
	if out != "build\ntest\n" {
		t.Errorf("single-type listing = %q, want flat build/test", out)
	}
}

// TestNsMultiTypeRunExecutesWithOwnerTypeDir: targets from both composed
// types run through the ordinary machinery; each step sees the OWNING
// type's SMITH_TYPE_DIR.
func TestNsMultiTypeRunExecutesWithOwnerTypeDir(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", types: []string{"mock/std/lib@1", "mock/std/docs@1"}},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {`echo {{.SMITH_TYPE_DIR}}`}},
		},
		"mock/std/docs@1": {
			targets: map[string][]string{"docs": {`echo {{.SMITH_TYPE_DIR}}`}},
		},
	})
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "libs", "alpha", "docs"); err != nil {
		t.Fatalf("libs alpha docs: %v", err)
	}
	if _, err := run(t, "libs", "alpha", "build"); err != nil {
		t.Fatalf("libs alpha build: %v", err)
	}
	libDir := filepath.Join(repo, ".smith", "types", "mock", "std", "lib@v1")
	docsDir := filepath.Join(repo, ".smith", "types", "mock", "std", "docs@v1")
	if !slices.Equal(f.runs, []string{"echo " + docsDir, "echo " + libDir}) {
		t.Errorf("runs = %v, want per-target owning type dirs (docs, then build)", f.runs)
	}
	if len(f.envs) != 2 ||
		!slices.Contains(f.envs[0], "SMITH_TYPE_DIR="+docsDir) ||
		!slices.Contains(f.envs[1], "SMITH_TYPE_DIR="+libDir) {
		t.Errorf("env SMITH_TYPE_DIR exports = %v, want owning type dirs", f.envs)
	}
}

// TestNsMultiTypeNamespaceRunSkipsAndReports: a namespace-wide target run
// picks up only projects whose composed types declare the target.
func TestNsMultiTypeNamespaceRunSkipsAndReports(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", types: []string{"mock/std/lib@1", "mock/std/docs@1"}},
		"libs/beta":  {name: "beta", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo lib-build"}},
		},
		"mock/std/docs@1": {
			targets: map[string][]string{"docs": {"echo docs"}},
		},
	})
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "libs", "docs")
	if err != nil {
		t.Fatalf("libs docs: %v\noutput:\n%s", err, out)
	}
	if !slices.Equal(f.runs, []string{"echo docs"}) {
		t.Errorf("runs = %v, want only alpha's docs", f.runs)
	}
	if !strings.Contains(out, "ran 1, skipped 1 (type has no 'docs' target)") {
		t.Errorf("output missing skip summary:\n%s", out)
	}
}

// TestNsMultiTypeUnknownTargetNamesTypes: an unknown target error lists what
// the composed types DO declare, naming all refs.
func TestNsMultiTypeUnknownTargetNamesTypes(t *testing.T) {
	repo := t.TempDir()
	multiTypeFixture(t, repo)
	t.Chdir(repo)

	_, err := run(t, "libs", "alpha", "nosuch")
	if err == nil || !strings.Contains(err.Error(), `has no target "nosuch"`) ||
		!strings.Contains(err.Error(), "build, docs, test") ||
		!strings.Contains(err.Error(), "types mock/std/lib@1, mock/std/docs@1") {
		t.Fatalf("error = %v, want unknown-target listing naming all refs", err)
	}
}

// TestNsMultiTypeCollisionIsHardError: two types declaring the same target
// fail loudly at first resolution, naming the project and both types.
func TestNsMultiTypeCollisionIsHardError(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", types: []string{"mock/std/lib@1", "mock/std/other@1"}},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo a"}},
		},
		"mock/std/other@1": {
			targets: map[string][]string{"build": {"echo b"}},
		},
	})
	t.Chdir(repo)

	_, err := run(t, "libs", "alpha", "list")
	if err == nil || !strings.Contains(err.Error(), `project "alpha"`) ||
		!strings.Contains(err.Error(), `both declare target "build"`) {
		t.Fatalf("error = %v, want composition collision naming project and types", err)
	}
}

// Namespace-free project addressing ("crispy" targets): a uniquely named
// project is reachable as `smith <name> ...` without its namespace. The
// tests below pin the precedence rules documented on dispatchPositional:
// with 2+ args an exact project-name match beats the namespace reading of
// arg0 (PROJECT WINS); with 1 arg the order is namespace → repo-wide target
// → project target listing. Every ns-free form produces byte-identical
// output to its `smith <ns> <name>` counterpart.

// TestCrispyRunTargetMatchesNsForm: `smith alpha build` runs the same plan
// with the same output as `smith libs alpha build`.
func TestCrispyRunTargetMatchesNsForm(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	nsOut, nsRuns := runWithFake(t, "libs", "alpha", "build")
	crispyOut, crispyRuns := runWithFake(t, "alpha", "build")
	if crispyOut != nsOut {
		t.Errorf("crispy output differs from ns form:\ncrispy:\n%s\nns:\n%s", crispyOut, nsOut)
	}
	if !slices.Equal(crispyRuns, nsRuns) || len(crispyRuns) == 0 {
		t.Errorf("crispy steps = %v, ns steps = %v", crispyRuns, nsRuns)
	}
	if !strings.Contains(crispyOut, "==> alpha:build") {
		t.Errorf("output missing node header:\n%s", crispyOut)
	}
}

// runWithFake executes args with a fresh recording fake and returns the
// captured output and step commands.
func runWithFake(t *testing.T, args ...string) (string, []string) {
	t.Helper()
	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\noutput:\n%s", args, err, out)
	}
	return out, f.runs
}

// TestCrispyListTargetsMatchesNsForm: `smith alpha list` and the bare
// `smith alpha` list the project's targets exactly like `smith libs alpha
// list`.
func TestCrispyListTargetsMatchesNsForm(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "list")
	if err != nil {
		t.Fatalf("libs alpha list: %v", err)
	}
	for _, args := range [][]string{{"alpha", "list"}, {"alpha"}} {
		got, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != out {
			t.Errorf("%v = %q, want ns-form output %q", args, got, out)
		}
	}
	if out != "build\ntest\n" {
		t.Errorf("ns-form listing = %q, want build/test", out)
	}
}

// TestCrispyHelpMatchesNsForm: `smith alpha help` and `smith alpha build
// help` render byte-identically to the namespace forms.
func TestCrispyHelpMatchesNsForm(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)
	plainDisplay(t)

	typeHelpNS, err := run(t, "libs", "alpha", "help")
	if err != nil {
		t.Fatalf("libs alpha help: %v", err)
	}
	typeHelpCrispy, err := run(t, "alpha", "help")
	if err != nil {
		t.Fatalf("alpha help: %v", err)
	}
	if typeHelpCrispy != typeHelpNS {
		t.Errorf("crispy type help differs from ns form:\ncrispy:\n%s\nns:\n%s", typeHelpCrispy, typeHelpNS)
	}

	targetHelpNS, err := run(t, "libs", "alpha", "build", "help")
	if err != nil {
		t.Fatalf("libs alpha build help: %v", err)
	}
	targetHelpCrispy, err := run(t, "alpha", "build", "help")
	if err != nil {
		t.Fatalf("alpha build help: %v", err)
	}
	if targetHelpCrispy != targetHelpNS {
		t.Errorf("crispy target help differs from ns form:\ncrispy:\n%s\nns:\n%s", targetHelpCrispy, targetHelpNS)
	}
}

// TestCrispyProjectWinsOverNamespace: with 2+ args an exact project-name
// match beats the namespace reading of arg0. A project named exactly like a
// namespace shadows `smith <ns> <target>`; the namespace listing itself is
// untouched (1-arg tier order: namespace first).
func TestCrispyProjectWinsOverNamespace(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/libs":  {name: "libs", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo build"}, "test": {"echo test"}},
			deps:    map[string][]string{"build": {"test"}},
		},
	})
	t.Chdir(repo)

	// `smith libs build` runs PROJECT libs's build — not the namespace-wide
	// build (which would also run alpha's).
	out, runs := runWithFake(t, "libs", "build")
	if !slices.Equal(runs, []string{"echo test", "echo build"}) {
		t.Errorf("steps = %v, want project libs's own closure only", runs)
	}
	if !strings.Contains(out, "==> libs:build") || strings.Contains(out, "==> alpha:build") {
		t.Errorf("output = %q, want project libs only (namespace-wide run must not run)", out)
	}

	// `smith libs` keeps its 1-arg meaning: the namespace listing.
	out, err := run(t, "libs")
	if err != nil {
		t.Fatalf("libs: %v", err)
	}
	if out != "alpha\nlibs\n" {
		t.Errorf("namespace listing = %q, want alpha/libs", out)
	}
}

// TestCrispyNamespaceWideMeaningIntact: with no project named like the
// namespace, `smith <ns> <target>` keeps its namespace-wide meaning.
func TestCrispyNamespaceWideMeaningIntact(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
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

	out, runs := runWithFake(t, "libs", "test")
	if !slices.Equal(runs, []string{"echo test"}) {
		t.Errorf("steps = %v, want the test target across the namespace", runs)
	}
	if !strings.Contains(out, "ran 1, skipped 1 (type has no 'test' target)") {
		t.Errorf("output missing skip summary:\n%s", out)
	}
}

// TestCrispyTargetNameDoesNotShadowRepoWideRun: a project named like a
// common target does not shadow the established repo-wide run...
func TestCrispyTargetNameDoesNotShadowRepoWideRun(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/build": {name: "build", typ: "mock/std/notarget@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo build"}, "test": {"echo test"}},
			deps:    map[string][]string{"build": {"test"}},
		},
		"mock/std/notarget@1": {
			targets: map[string][]string{"test": {"echo test"}},
		},
	})
	t.Chdir(repo)

	out, runs := runWithFake(t, "build")
	if !slices.Equal(runs, []string{"echo test", "echo build"}) {
		t.Errorf("steps = %v, want alpha's build closure (repo-wide run)", runs)
	}
	if !strings.Contains(out, "ran 1, skipped 1 (type has no 'build' target)") {
		t.Errorf("output missing skip summary:\n%s", out)
	}
}

// ...but when NO project declares the target, a bare project name lists that
// project's targets instead of a useless error.
func TestCrispyBareProjectNameListsTargets(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/build": {name: "build", typ: "mock/std/notarget@1"},
	}, map[string]typeSpec{
		"mock/std/notarget@1": {
			targets: map[string][]string{"test": {"echo test"}},
		},
	})
	t.Chdir(repo)

	out, err := run(t, "build")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if out != "test\n" {
		t.Errorf("output = %q, want the project build's target listing", out)
	}
}

// TestCrispyCommandWordShadowedBySubcommand: cobra routes top-level command
// names before the positional grammar, so a project named like a command
// word ("tree") is shadowed by the command — `smith tree` prints the
// discovered project tree (annotated with the type), not the project's
// target listing.
func TestCrispyCommandWordShadowedBySubcommand(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/tree":  {name: "tree", typ: "mock/std/lib@1"},
	}, nil)
	t.Chdir(repo)

	out, err := run(t, "tree")
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if !strings.Contains(out, "tree") || !strings.Contains(out, "(mock/std/lib@1)") {
		t.Errorf("output = %q, want the tree command's project tree (a target listing would be \"build\\ntest\\n\")", out)
	}

	// The project itself stays reachable through the namespace grammar.
	out, err = run(t, "libs", "tree", "list")
	if err != nil {
		t.Fatalf("libs tree list: %v", err)
	}
	if out != "build\n" {
		t.Errorf("targets = %q, want build", out)
	}
}

// TestCrispyUnknownArg0: unknown names keep the established error style —
// the 2-arg fallthrough suggests `smith list`; the 1-arg repo-wide form is
// unchanged.
func TestCrispyUnknownArg0(t *testing.T) {
	repo := t.TempDir()
	twoLibsFixture(t, repo)
	t.Chdir(repo)

	_, err := run(t, "frobnicate", "extra")
	if err == nil || !strings.Contains(err.Error(), `unknown command "frobnicate" for "smith"`) ||
		!strings.Contains(err.Error(), `smith list`) {
		t.Fatalf("error = %v, want unknown-command error suggesting smith list", err)
	}

	_, err = run(t, "frobnicate")
	if err == nil || !strings.Contains(err.Error(), `no project in this repository has a "frobnicate" target`) {
		t.Fatalf("error = %v, want unchanged repo-wide unknown-target error", err)
	}
}

// TestCrispyMultiType: the rewrite is plain name resolution, so a multi-type
// project works ns-free with the grouped listing and cross-type targets.
func TestCrispyMultiType(t *testing.T) {
	repo := t.TempDir()
	multiTypeFixture(t, repo)
	t.Chdir(repo)

	out, err := run(t, "alpha", "list")
	if err != nil {
		t.Fatalf("alpha list: %v", err)
	}
	want := "mock/std/lib@1\n\nbuild\ntest\n\nmock/std/docs@1\n\ndocs -- build the docs\n"
	if out != want {
		t.Errorf("multi-type listing = %q, want %q", out, want)
	}

	_, runs := runWithFake(t, "alpha", "docs")
	if !slices.Equal(runs, []string{"echo docs"}) {
		t.Errorf("steps = %v, want the docs target from the second type", runs)
	}
}

// TestCrispyRootLevelProject: a project whose smith.yml sits directly at the
// repo root (Dir ".") has no namespace segment; the rewrite synthesizes "."
// and the ordinary machinery keeps working.
func TestCrispyRootLevelProject(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo,
		map[string]manifestSpec{"libs/placeholder": {name: "placeholder", typ: "mock/std/lib@1"}},
		nil)
	// Re-root discovery at the repo root: replace the seeded roots with "."
	// and put the manifest directly at the repo root.
	repoYML := filepath.Join(repo, ".smith", "repo.yml")
	data, err := os.ReadFile(repoYML)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), "    - libs\n    - services\n", "    - .\n", 1)
	if replaced == string(data) {
		t.Fatal("test setup: seeded project_roots not found in repo.yml")
	}
	if err := os.WriteFile(repoYML, []byte(replaced), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, "libs")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "smith.yml"), []byte("version: 1\nname: rootproj\ntype: mock/std/lib@1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, runs := runWithFake(t, "rootproj", "build")
	if !slices.Equal(runs, []string{"echo build"}) {
		t.Errorf("steps = %v, want rootproj's build", runs)
	}
	if !strings.Contains(out, "==> rootproj:build") {
		t.Errorf("output missing node header:\n%s", out)
	}

	out, err = run(t, "rootproj", "list")
	if err != nil {
		t.Fatalf("rootproj list: %v", err)
	}
	if out != "build\n" {
		t.Errorf("targets = %q, want build", out)
	}

	// The bare 1-arg form lists its targets too (no project declares a
	// "rootproj" target).
	out, err = run(t, "rootproj")
	if err != nil {
		t.Fatalf("rootproj: %v", err)
	}
	if out != "build\n" {
		t.Errorf("bare listing = %q, want build", out)
	}
}
