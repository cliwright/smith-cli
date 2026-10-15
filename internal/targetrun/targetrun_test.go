package targetrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/ingot"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/runner"
	"github.com/cliwright/smith/internal/typeref"
)

type recordedRun struct {
	dir  string
	step string
	env  []string
}

// fakeRunner records every step; steps equal to failOn error out.
type fakeRunner struct {
	runs   []recordedRun
	failOn string
}

func (f *fakeRunner) Run(_ context.Context, opts runner.Options, _ string, args ...string) error {
	step := args[1]
	f.runs = append(f.runs, recordedRun{dir: opts.Dir, step: step, env: opts.Env})
	if step == f.failOn {
		return fmt.Errorf("exited with status 3")
	}
	return nil
}

// mustRefs parses one fixture ref string into TypeRefs; the fixture strings
// are constants, so a parse failure is a programmer error.
func mustRefs(s string) manifest.TypeRefs {
	ref, err := typeref.Parse(s)
	if err != nil {
		panic(err)
	}
	return manifest.TypeRefs{ref}
}

// resolvedOfE wraps fixture types in a Resolved the way cli.typeFor builds
// one: composition owner indexes plus canonical installed dirs under
// typesRoot. Composition failures surface as an error (a fixture with
// deliberately conflicting types can exercise them through Build).
func resolvedOfE(typesRoot string, ts ...*projecttype.ProjectType) (*Resolved, error) {
	byTarget, byParam, err := projecttype.Compose(ts)
	if err != nil {
		return nil, err
	}
	rt := &Resolved{Types: ts, ByTarget: byTarget, ByParam: byParam, Dirs: map[string]string{}}
	for _, t := range ts {
		rt.Dirs[ref(t)] = filepath.Join(typesRoot, ingot.RefName(t.Name, t.Version))
	}
	return rt, nil
}

// resolvedOf is resolvedOfE for well-formed fixtures: a composition failure
// is a programmer error and panics.
func resolvedOf(typesRoot string, ts ...*projecttype.ProjectType) *Resolved {
	rt, err := resolvedOfE(typesRoot, ts...)
	if err != nil {
		panic(err)
	}
	return rt
}

func mkProject(dir, name, typ string, deps ...manifest.Dependency) discovery.Project {
	return discovery.Project{
		Dir:      dir,
		Manifest: &manifest.Manifest{Name: name, Types: mustRefs(typ), DependsOn: deps},
	}
}

func mkProjectTypes(dir, name string, refs []string, deps ...manifest.Dependency) discovery.Project {
	types := make(manifest.TypeRefs, len(refs))
	for i, s := range refs {
		types[i] = mustRefs(s)[0]
	}
	return discovery.Project{
		Dir:      dir,
		Manifest: &manifest.Manifest{Name: name, Types: types, DependsOn: deps},
	}
}

func mkProjectFull(dir, name, typ string, params, env map[string]string, deps ...manifest.Dependency) discovery.Project {
	return discovery.Project{
		Dir: dir,
		Manifest: &manifest.Manifest{
			Name: name, Types: mustRefs(typ), DependsOn: deps,
			Params: params, Environment: env,
		},
	}
}

func mkType(targets map[string][]string, deps map[string][]string, wd projecttype.WorkingDir) *projecttype.ProjectType {
	typed := make(map[string]projecttype.Target, len(targets))
	for name, steps := range targets {
		typed[name] = projecttype.Target{Steps: steps}
	}
	return &projecttype.ProjectType{
		Name:       "mock/std/lib",
		Version:    1,
		Targets:    typed,
		DependsOn:  deps,
		WorkingDir: wd,
	}
}

// typedTargets adapts a bare step-list fixture to typed targets.
func typedTargets(steps map[string][]string) map[string]projecttype.Target {
	out := make(map[string]projecttype.Target, len(steps))
	for name, ss := range steps {
		out[name] = projecttype.Target{Steps: ss}
	}
	return out
}

func stepsOf(runs []recordedRun) []string {
	steps := make([]string, len(runs))
	for i, r := range runs {
		steps[i] = r.step
	}
	return steps
}

func TestBuildClosureAndOrder(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/alpha", "alpha", "mock/std/lib@1",
			manifest.Dependency{Project: "beta", Targets: []string{"build"}, Before: []string{"build"}}),
		mkProject("libs/beta", "beta", "mock/std/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(
			map[string][]string{"build": {"echo alpha-build"}, "test": {"echo alpha-test"}},
			map[string][]string{"build": {"test"}},
			projecttype.WorkingDirProject),
		"beta": mkType(
			map[string][]string{"build": {"echo beta-build"}},
			nil,
			projecttype.WorkingDirProject),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	// Roots select only alpha:build; the closure pulls in alpha:test (type
	// dep) and beta:build (cross-project dep) as prerequisites.
	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"echo alpha-test", "echo beta-build", "echo alpha-build"}
	if got := stepsOf(f.runs); !slices.Equal(got, want) {
		t.Errorf("execution order = %v, want %v", got, want)
	}
}

func TestBuildDeduplicatesAcrossRoots(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/alpha", "alpha", "mock/std/lib@1"),
		mkProject("libs/beta", "beta", "mock/std/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo alpha-build"}}, nil, ""),
		"beta":  mkType(map[string][]string{"build": {"echo beta-build"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"beta", "build"}, {"alpha", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(f.runs) != 2 {
		t.Errorf("%d runs, want 2 (each node at most once)", len(f.runs))
	}
}

func TestBuildCrossNamespaceClosure(t *testing.T) {
	projects := []discovery.Project{
		mkProject("services/web", "web", "mock/std/lib@1",
			manifest.Dependency{Project: "shared", Targets: []string{"build"}, Before: []string{"build"}}),
		mkProject("libs/shared", "shared", "mock/std/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"web":    mkType(map[string][]string{"build": {"echo web-build"}}, nil, ""),
		"shared": mkType(map[string][]string{"build": {"echo shared-build"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"web", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var names []string
	for _, n := range plan.Order() {
		names = append(names, n.String())
	}
	if !slices.Equal(names, []string{"shared:build", "web:build"}) {
		t.Errorf("closure = %v, want cross-namespace prerequisite included", names)
	}
}

func TestBuildCycleError(t *testing.T) {
	projects := []discovery.Project{mkProject("libs/alpha", "alpha", "mock/std/lib@1")}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(
			map[string][]string{"build": {"echo b"}, "test": {"echo t"}},
			map[string][]string{"build": {"test"}, "test": {"build"}},
			""),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") ||
		!strings.Contains(err.Error(), "alpha:build") || !strings.Contains(err.Error(), "alpha:test") {
		t.Fatalf("Build error = %v, want cycle naming alpha:build/alpha:test", err)
	}
}

// TestBuildDanglingTypeTarget: a type depends_on entry pointing at a target
// the same type does not declare is a hard error — enforced at composition,
// so it surfaces while the project's types resolve.
func TestBuildDanglingTypeTarget(t *testing.T) {
	projects := []discovery.Project{mkProject("libs/alpha", "alpha", "mock/std/lib@1")}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo b"}}, map[string][]string{"build": {"nope"}}, ""),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOfE("/repo/.smith/types", types[p.Manifest.Name])
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "depends_on must stay within one type") {
		t.Fatalf("Build error = %v, want dangling type depends_on named", err)
	}
}

func TestBuildDanglingManifestTarget(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/alpha", "alpha", "mock/std/lib@1",
			manifest.Dependency{Project: "beta", Targets: []string{"nope"}, Before: []string{"build"}}),
		mkProject("libs/beta", "beta", "mock/std/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo b"}}, nil, ""),
		"beta":  mkType(map[string][]string{"build": {"echo bb"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), `manifest of project "alpha" depends on "beta" with target "nope"`) {
		t.Fatalf("Build error = %v, want manifest dangling-target error", err)
	}
}

func TestExecuteFailFast(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/alpha", "alpha", "mock/std/lib@1",
			manifest.Dependency{Project: "beta", Targets: []string{"build"}, Before: []string{"build"}}),
		mkProject("libs/beta", "beta", "mock/std/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo alpha-build"}}, nil, ""),
		"beta":  mkType(map[string][]string{"build": {"echo beta-build", "exit 3"}, "test": {"echo beta-test"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"beta", "test"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{failOn: "exit 3"}
	err = plan.Execute(context.Background(), f, "/repo", &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "beta:build") || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("Execute error = %v, want project:target and status", err)
	}
	for _, r := range f.runs {
		if r.step == "echo alpha-build" {
			t.Error("node after the failure ran; want fail-fast")
		}
	}
}

func TestExecuteWorkingDir(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/repojob", "repojob", "mock/std/lib@1"),
		mkProject("libs/projjob", "projjob", "mock/std/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"repojob": mkType(map[string][]string{"build": {"echo r"}}, nil, projecttype.WorkingDirRepo),
		"projjob": mkType(map[string][]string{"build": {"echo p"}}, nil, projecttype.WorkingDirProject),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"repojob", "build"}, {"projjob", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	byStep := map[string]string{}
	for _, r := range f.runs {
		byStep[r.step] = r.dir
	}
	if got := byStep["echo r"]; got != "/repo" {
		t.Errorf("working_dir repo ran in %q, want /repo", got)
	}
	if got, want := byStep["echo p"], filepath.Join("/repo", "libs", "projjob"); got != want {
		t.Errorf("working_dir project ran in %q, want %q", got, want)
	}
}

func TestExecuteWorkingDirManifestOverridesType(t *testing.T) {
	up, down := mkProject("libs/up", "up", "mock/std/lib@1"), mkProject("libs/down", "down", "mock/std/lib@1")
	up.Manifest.WorkingDir = "repo"      // type says project, manifest says repo
	down.Manifest.WorkingDir = "project" // type says repo, manifest says project
	projects := []discovery.Project{up, down}
	types := map[string]*projecttype.ProjectType{
		"up":   mkType(map[string][]string{"build": {"echo up"}}, nil, projecttype.WorkingDirProject),
		"down": mkType(map[string][]string{"build": {"echo down"}}, nil, projecttype.WorkingDirRepo),
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"up", "build"}, {"down", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	byStep := map[string]string{}
	for _, r := range f.runs {
		byStep[r.step] = r.dir
	}
	if got := byStep["echo up"]; got != "/repo" {
		t.Errorf("manifest working_dir repo over type project: ran in %q, want /repo", got)
	}
	if got, want := byStep["echo down"], filepath.Join("/repo", "libs", "down"); got != want {
		t.Errorf("manifest working_dir project over type repo: ran in %q, want %q", got, want)
	}
}

func TestBuildRejectsUndeclaredManifestParam(t *testing.T) {
	projects := []discovery.Project{
		mkProjectFull("libs/alpha", "alpha", "mock/std/lib@1", map[string]string{"speed": "fast"}, nil),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:     map[string]projecttype.Param{"config_path": {Default: "pyproject.toml"}},
			Targets:    map[string]projecttype.Target{"build": {Steps: []string{"echo ok"}}},
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), `project "alpha" (type mock/std/lib@1) sets param "speed", which the type does not declare`) {
		t.Fatalf("Build error = %v, want undeclared-param error naming project, key, and type", err)
	}
}

func TestExecuteRendersStepsWithOverrides(t *testing.T) {
	projects := []discovery.Project{
		mkProjectFull("libs/alpha", "alpha", "mock/std/lib@1",
			map[string]string{"greeting": "hi there"}, nil),
		mkProject("libs/beta", "beta", "mock/std/lib@1"),
	}
	greetingType := func() *projecttype.ProjectType {
		return &projecttype.ProjectType{
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:     map[string]projecttype.Param{"greeting": {Default: "hello"}},
			Targets:    map[string]projecttype.Target{"build": {Steps: []string{`echo '{{.greeting}} world'`}}},
			WorkingDir: projecttype.WorkingDirProject,
		}
	}
	types := map[string]*projecttype.ProjectType{"alpha": greetingType(), "beta": greetingType()}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"beta", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := stepsOf(f.runs)
	// The manifest override lands inside the author's single quotes; the
	// project without an override renders the default.
	want := []string{"echo 'hi there world'", "echo 'hello world'"}
	if !slices.Equal(got, want) {
		t.Errorf("rendered steps = %v, want %v", got, want)
	}
}

// lastEnvValue returns the effective value of key (the last occurrence wins,
// matching exec.Cmd semantics).
func lastEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	value := ""
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			value = strings.TrimPrefix(entry, prefix)
			found = true
		}
	}
	return value, found
}

func TestExecuteEnvPrecedence(t *testing.T) {
	t.Setenv("MOCK_LEVEL", "base")

	projects := []discovery.Project{
		mkProjectFull("libs/alpha", "alpha", "mock/std/lib@1",
			map[string]string{"config_path": "ci.toml"},
			map[string]string{
				"MOCK_LEVEL":    "manifest",
				"TEMPLATE_VAR":  "man={{.config_path}}",
				"SMITH_PROJECT": "shadow-attempt",
			}),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:      map[string]projecttype.Param{"config_path": {Default: "pyproject.toml"}},
			Environment: map[string]string{"MOCK_LEVEL": "type", "TEMPLATE_VAR": "cfg={{.config_path}}"},
			Targets:     typedTargets(map[string][]string{"build": {"echo ok"}}),
			WorkingDir:  projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(f.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(f.runs))
	}
	env := f.runs[0].env

	if got, _ := lastEnvValue(env, "MOCK_LEVEL"); got != "manifest" {
		t.Errorf("MOCK_LEVEL = %q, want manifest (base < type < manifest)", got)
	}
	if got, _ := lastEnvValue(env, "TEMPLATE_VAR"); got != "man=ci.toml" {
		t.Errorf("TEMPLATE_VAR = %q, want man=ci.toml (manifest layer, rendered with merged params)", got)
	}
	if got, ok := lastEnvValue(env, "SMITH_PROJECT"); !ok || got != "alpha" {
		t.Errorf("SMITH_PROJECT = %q (present %v), want alpha — computed vars cannot be shadowed", got, ok)
	}
	if got, ok := lastEnvValue(env, "SMITH_TARGET"); !ok || got != "build" {
		t.Errorf("SMITH_TARGET = %q, want build", got)
	}
	if got, ok := lastEnvValue(env, "SMITH_REPO_ROOT"); !ok || got != "/repo" {
		t.Errorf("SMITH_REPO_ROOT = %q, want absolute /repo", got)
	}
}

// TestExecuteRealShellEnv runs real shell steps to prove the rendered
// environment actually arrives in the subprocess.
func TestExecuteRealShellEnv(t *testing.T) {
	dir := t.TempDir()
	markers := filepath.Join(dir, "markers")

	projects := []discovery.Project{
		mkProjectFull("proj", "alpha", "mock/std/lib@1",
			map[string]string{"suffix": "ok"},
			map[string]string{"EXTRA": "manifest-layer"}),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:      map[string]projecttype.Param{"suffix": {Default: "ok"}},
			Environment: map[string]string{"EXTRA": "type-layer", "TEMPLATED": "cfg-{{.suffix}}"},
			Targets: typedTargets(map[string][]string{
				"build": {fmt.Sprintf(`echo "$SMITH_PROJECT:$SMITH_TARGET:$EXTRA:$TEMPLATED" >> %s`, markers)},
			}),
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := plan.Execute(context.Background(), runner.Default, dir, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(markers)
	if err != nil {
		t.Fatalf("markers: %v", err)
	}
	if got, want := string(data), "alpha:build:manifest-layer:cfg-ok\n"; got != want {
		t.Errorf("markers = %q, want %q", got, want)
	}
}

// TestExecuteParamValueUsesComputedVars covers the user's case: a manifest
// param value referencing SMITH_REPO_ROOT renders into the step with the
// absolute repo root.
func TestExecuteParamValueUsesComputedVars(t *testing.T) {
	projects := []discovery.Project{
		mkProjectFull("libs/deep/alpha", "alpha", "mock/std/lib@1",
			map[string]string{"config_path": "{{.SMITH_REPO_ROOT}}/pyproject.toml"}, nil),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:     map[string]projecttype.Param{"config_path": {Default: "pyproject.toml"}},
			Targets:    typedTargets(map[string][]string{"build": {`cat '{{.config_path}}'`}, "info": {`echo {{.SMITH_PROJECT_DIR}}`}}),
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"alpha", "info"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := stepsOf(f.runs)
	want := []string{"cat '/repo/pyproject.toml'", "echo /repo/libs/deep/alpha"}
	if !slices.Equal(got, want) {
		t.Errorf("rendered steps = %v, want %v", got, want)
	}
}

func TestExecuteEnvUsesParamsAndComputedVars(t *testing.T) {
	projects := []discovery.Project{
		mkProjectFull("libs/alpha", "alpha", "mock/std/lib@1",
			map[string]string{"config_path": "ci.toml"},
			map[string]string{"CTX": "cfg={{.config_path}} root={{.SMITH_REPO_ROOT}} proj={{.SMITH_PROJECT}}"}),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:     map[string]projecttype.Param{"config_path": {Default: "pyproject.toml"}},
			Targets:    map[string]projecttype.Target{"build": {Steps: []string{"echo ok"}}},
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, ok := lastEnvValue(f.runs[0].env, "CTX")
	if !ok || got != "cfg=ci.toml root=/repo proj=alpha" {
		t.Errorf("CTX = %q (present %v), want params and computed vars together", got, ok)
	}
}

func TestBuildPhaseARejectsParamReferencingParam(t *testing.T) {
	projects := []discovery.Project{
		mkProjectFull("libs/alpha", "alpha", "mock/std/lib@1",
			map[string]string{"config_path": "{{.base_dir}}/pyproject.toml", "base_dir": "."}, nil),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params: map[string]projecttype.Param{
				"config_path": {Default: "pyproject.toml"},
				"base_dir":    {Default: "."},
			},
			Targets:    map[string]projecttype.Target{"build": {Steps: []string{"echo ok"}}},
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), `project "alpha" (type mock/std/lib@1) param "config_path"`) ||
		!strings.Contains(err.Error(), "param values may reference computed vars (SMITH_*) only, not other params") {
		t.Fatalf("Build error = %v, want the param-references-param hard error", err)
	}
}

func TestBuildPhaseARejectsUnknownVar(t *testing.T) {
	projects := []discovery.Project{
		mkProjectFull("libs/alpha", "alpha", "mock/std/lib@1",
			map[string]string{"config_path": "{{.SMITH_FOO}}/x"}, nil),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": {
			Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:     map[string]projecttype.Param{"config_path": {Default: "pyproject.toml"}},
			Targets:    map[string]projecttype.Target{"build": {Steps: []string{"echo ok"}}},
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), `param "config_path"`) ||
		!strings.Contains(err.Error(), `unknown var "SMITH_FOO"`) {
		t.Fatalf("Build error = %v, want the unknown-computed-var hard error", err)
	}
}

// TestAttachmentClosureAndComputedVars: an attachment is a synthetic project
// (dir = repo root) routed through the ordinary machinery — type-level
// closure, two-phase params, and computed vars all apply.
func TestAttachmentClosureAndComputedVars(t *testing.T) {
	attachment := discovery.Project{
		Dir: ".",
		Manifest: &manifest.Manifest{
			Name:   "python",
			Types:  mustRefs("repo/uv/workspace@1"),
			Params: map[string]string{"extras": "{{.SMITH_PROJECT}}-extras"},
		},
	}
	types := map[string]*projecttype.ProjectType{
		"python": {
			Name: "repo/uv/workspace", Version: 1, Description: "x", Tools: []string{"sh"},
			Params:     map[string]projecttype.Param{"extras": {Default: "dev"}},
			Targets:    typedTargets(map[string][]string{"setup": {"echo setup {{.extras}} {{.SMITH_PROJECT}} {{.SMITH_PROJECT_DIR}}"}, "fetch": {"echo fetch"}}),
			DependsOn:  map[string][]string{"setup": {"fetch"}},
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build([]discovery.Project{attachment}, typeFor, []Node{{"python", "setup"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := stepsOf(f.runs)
	want := []string{"echo fetch", "echo setup python-extras python /repo"}
	if !slices.Equal(got, want) {
		t.Errorf("steps = %v, want %v", got, want)
	}
	for i, run := range f.runs {
		if run.dir != "/repo" {
			t.Errorf("attachment step %d ran in %q, want repo root", i, run.dir)
		}
	}
}

// TestAttachmentRealShell runs real shell steps for an attachment: computed
// vars and layered env land in the subprocess, cwd is the repo root.
func TestAttachmentRealShell(t *testing.T) {
	dir := t.TempDir()
	markers := filepath.Join(dir, "markers")

	attachment := discovery.Project{
		Dir: ".",
		Manifest: &manifest.Manifest{
			Name:        "python",
			Types:       mustRefs("repo/uv/workspace@1"),
			Environment: map[string]string{"LAYER": "manifest"},
		},
	}
	types := map[string]*projecttype.ProjectType{
		"python": {
			Name: "repo/uv/workspace", Version: 1, Description: "x", Tools: []string{"sh"},
			Environment: map[string]string{"LAYER": "type"},
			Targets: typedTargets(map[string][]string{
				// SMITH_PROJECT_DIR is template-only (not an env var), so it
				// is rendered into the command; LAYER comes from the env.
				"setup": {fmt.Sprintf(`echo "$SMITH_PROJECT|{{.SMITH_PROJECT_DIR}}|$LAYER" >> %s`, markers)},
			}),
			WorkingDir: projecttype.WorkingDirProject,
		},
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", types[p.Manifest.Name]), nil
	}

	plan, err := Build([]discovery.Project{attachment}, typeFor, []Node{{"python", "setup"}}, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := plan.Execute(context.Background(), runner.Default, dir, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(markers)
	if err != nil {
		t.Fatalf("markers: %v", err)
	}
	want := fmt.Sprintf("python|%s|manifest\n", dir)
	if string(data) != want {
		t.Errorf("markers = %q, want %q", data, want)
	}
}

// TestExecuteMultiTypeOwnerDirs: a project composed of two types runs targets
// from both, and SMITH_TYPE_DIR (template var AND exported env) is the
// OWNING type's installed dir per target.
func TestExecuteMultiTypeOwnerDirs(t *testing.T) {
	projects := []discovery.Project{
		mkProjectTypes("libs/alpha", "alpha", []string{"mock/std/lib@1", "mock/std/docs@1"}),
	}
	typesRoot := "/repo/.smith/types"
	buildType := &projecttype.ProjectType{
		Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
		Targets: typedTargets(map[string][]string{
			"build": {"echo build {{.SMITH_TYPE_DIR}}"},
		}),
		WorkingDir: projecttype.WorkingDirProject,
	}
	docsType := &projecttype.ProjectType{
		Name: "mock/std/docs", Version: 1, Description: "x", Tools: []string{"sh"},
		Targets: typedTargets(map[string][]string{
			"docs": {"echo docs {{.SMITH_TYPE_DIR}}"},
		}),
		WorkingDir: projecttype.WorkingDirProject,
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf(typesRoot, buildType, docsType), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"alpha", "docs"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(f.runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(f.runs))
	}
	buildDir := filepath.Join(typesRoot, "mock", "std", "lib@v1")
	docsDir := filepath.Join(typesRoot, "mock", "std", "docs@v1")
	if got, want := f.runs[0].step, "echo build "+buildDir; got != want {
		t.Errorf("build step = %q, want %q", got, want)
	}
	if got, want := f.runs[1].step, "echo docs "+docsDir; got != want {
		t.Errorf("docs step = %q, want %q", got, want)
	}
	// The exported env matches the template var, per target.
	if got, _ := lastEnvValue(f.runs[0].env, "SMITH_TYPE_DIR"); got != buildDir {
		t.Errorf("build SMITH_TYPE_DIR = %q, want %q", got, buildDir)
	}
	if got, _ := lastEnvValue(f.runs[1].env, "SMITH_TYPE_DIR"); got != docsDir {
		t.Errorf("docs SMITH_TYPE_DIR = %q, want %q", got, docsDir)
	}
}

// TestResolveParamsOwnerDirPhaseA: a manifest param renders in phase A with
// SMITH_TYPE_DIR bound to the OWNING type's installed dir, across a
// multi-type project.
func TestResolveParamsOwnerDirPhaseA(t *testing.T) {
	projects := []discovery.Project{
		mkProjectTypes("libs/alpha", "alpha", []string{"mock/std/lib@1", "mock/std/docs@1"}),
	}
	projects[0].Manifest.Params = map[string]string{
		"config_path": "{{.SMITH_TYPE_DIR}}/pyproject.toml", // owned by lib
		"output_dir":  "{{.SMITH_TYPE_DIR}}/site",           // owned by docs
	}
	libType := &projecttype.ProjectType{
		Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
		Params:     map[string]projecttype.Param{"config_path": {Default: "pyproject.toml"}},
		Targets:    typedTargets(map[string][]string{"build": {"echo b"}}),
		WorkingDir: projecttype.WorkingDirProject,
	}
	docsType := &projecttype.ProjectType{
		Name: "mock/std/docs", Version: 1, Description: "x", Tools: []string{"sh"},
		Params:     map[string]projecttype.Param{"output_dir": {Default: "docs"}},
		Targets:    typedTargets(map[string][]string{"docs": {"echo d"}}),
		WorkingDir: projecttype.WorkingDirProject,
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", libType, docsType), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	params := plan.params["alpha"]
	if got, want := params["config_path"], "/repo/.smith/types/mock/std/lib@v1/pyproject.toml"; got != want {
		t.Errorf("config_path = %q, want the lib type's dir (%q)", got, want)
	}
	if got, want := params["output_dir"], "/repo/.smith/types/mock/std/docs@v1/site"; got != want {
		t.Errorf("output_dir = %q, want the docs type's dir (%q)", got, want)
	}
}

// TestBuildMultiTypeTargetCollision: two types declaring the same target do
// not compose; the collision surfaces while the project resolves.
func TestBuildMultiTypeTargetCollision(t *testing.T) {
	projects := []discovery.Project{
		mkProjectTypes("libs/alpha", "alpha", []string{"mock/std/lib@1", "mock/std/other@1"}),
	}
	a := mkType(map[string][]string{"build": {"echo a"}}, nil, "")
	b := mkType(map[string][]string{"build": {"echo b"}}, nil, "")
	b.Name = "mock/std/other" // distinct name: the target collision is the conflict under test
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOfE("/repo/.smith/types", a, b)
	}

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}}, "/repo")
	if err == nil || !strings.Contains(err.Error(), `both declare target "build"`) {
		t.Fatalf("Build error = %v, want the target-collision composition error", err)
	}
}

// TestExecuteMultiTypeDependsOnWithinOwningType: in-project depends_on walks
// the OWNING type's edges only; a docs target can prereq a lib target within
// the same project when the owning type declares it.
func TestExecuteMultiTypeDependsOnWithinOwningType(t *testing.T) {
	projects := []discovery.Project{
		mkProjectTypes("libs/alpha", "alpha", []string{"mock/std/lib@1", "mock/std/docs@1"}),
	}
	libType := &projecttype.ProjectType{
		Name: "mock/std/lib", Version: 1, Description: "x", Tools: []string{"sh"},
		Targets: typedTargets(map[string][]string{
			"build": {"echo lib-build"},
			"test":  {"echo lib-test"},
		}),
		DependsOn:  map[string][]string{"build": {"test"}},
		WorkingDir: projecttype.WorkingDirProject,
	}
	docsType := &projecttype.ProjectType{
		Name: "mock/std/docs", Version: 1, Description: "x", Tools: []string{"sh"},
		Targets:    typedTargets(map[string][]string{"docs": {"echo docs"}}),
		WorkingDir: projecttype.WorkingDirProject,
	}
	typeFor := func(p discovery.Project) (*Resolved, error) {
		return resolvedOf("/repo/.smith/types", libType, docsType), nil
	}

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"alpha", "docs"}}, "/repo")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, "/repo", &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"echo lib-test", "echo lib-build", "echo docs"}
	if got := stepsOf(f.runs); !slices.Equal(got, want) {
		t.Errorf("steps = %v, want %v (owning type's test before its build)", got, want)
	}
}
