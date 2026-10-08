package targetrun

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/runner"
)

type recordedRun struct {
	dir  string
	step string
}

// fakeRunner records every step; steps equal to failOn error out.
type fakeRunner struct {
	runs   []recordedRun
	failOn string
}

func (f *fakeRunner) Run(_ context.Context, opts runner.Options, _ string, args ...string) error {
	step := args[1]
	f.runs = append(f.runs, recordedRun{dir: opts.Dir, step: step})
	if step == f.failOn {
		return fmt.Errorf("exited with status 3")
	}
	return nil
}

func mkProject(dir, name, typ string, deps ...manifest.Dependency) discovery.Project {
	return discovery.Project{
		Dir:      dir,
		Manifest: &manifest.Manifest{Name: name, Type: typ, DependsOn: deps},
	}
}

func mkType(targets map[string][]string, deps map[string][]string, wd projecttype.WorkingDir) *projecttype.ProjectType {
	return &projecttype.ProjectType{
		Name:       "mock/lib",
		Version:    1,
		Targets:    targets,
		DependsOn:  deps,
		WorkingDir: wd,
	}
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
		mkProject("libs/alpha", "alpha", "mock/lib@1",
			manifest.Dependency{Project: "beta", Targets: []string{"build"}, Before: []string{"build"}}),
		mkProject("libs/beta", "beta", "mock/lib@1"),
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
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	// Roots select only alpha:build; the closure pulls in alpha:test (type
	// dep) and beta:build (cross-project dep) as prerequisites.
	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}})
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
		mkProject("libs/alpha", "alpha", "mock/lib@1"),
		mkProject("libs/beta", "beta", "mock/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo alpha-build"}}, nil, ""),
		"beta":  mkType(map[string][]string{"build": {"echo beta-build"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"beta", "build"}, {"alpha", "build"}})
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
		mkProject("services/web", "web", "mock/lib@1",
			manifest.Dependency{Project: "shared", Targets: []string{"build"}, Before: []string{"build"}}),
		mkProject("libs/shared", "shared", "mock/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"web":    mkType(map[string][]string{"build": {"echo web-build"}}, nil, ""),
		"shared": mkType(map[string][]string{"build": {"echo shared-build"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	plan, err := Build(projects, typeFor, []Node{{"web", "build"}})
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
	projects := []discovery.Project{mkProject("libs/alpha", "alpha", "mock/lib@1")}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(
			map[string][]string{"build": {"echo b"}, "test": {"echo t"}},
			map[string][]string{"build": {"test"}, "test": {"build"}},
			""),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}})
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") ||
		!strings.Contains(err.Error(), "alpha:build") || !strings.Contains(err.Error(), "alpha:test") {
		t.Fatalf("Build error = %v, want cycle naming alpha:build/alpha:test", err)
	}
}

func TestBuildDanglingTypeTarget(t *testing.T) {
	projects := []discovery.Project{mkProject("libs/alpha", "alpha", "mock/lib@1")}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo b"}}, map[string][]string{"build": {"nope"}}, ""),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}})
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("Build error = %v, want dangling target named with project and type", err)
	}
}

func TestBuildDanglingManifestTarget(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/alpha", "alpha", "mock/lib@1",
			manifest.Dependency{Project: "beta", Targets: []string{"nope"}, Before: []string{"build"}}),
		mkProject("libs/beta", "beta", "mock/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo b"}}, nil, ""),
		"beta":  mkType(map[string][]string{"build": {"echo bb"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	_, err := Build(projects, typeFor, []Node{{"alpha", "build"}})
	if err == nil || !strings.Contains(err.Error(), `manifest of project "alpha" depends on "beta" with target "nope"`) {
		t.Fatalf("Build error = %v, want manifest dangling-target error", err)
	}
}

func TestExecuteFailFast(t *testing.T) {
	projects := []discovery.Project{
		mkProject("libs/alpha", "alpha", "mock/lib@1",
			manifest.Dependency{Project: "beta", Targets: []string{"build"}, Before: []string{"build"}}),
		mkProject("libs/beta", "beta", "mock/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"alpha": mkType(map[string][]string{"build": {"echo alpha-build"}}, nil, ""),
		"beta":  mkType(map[string][]string{"build": {"echo beta-build", "exit 3"}, "test": {"echo beta-test"}}, nil, ""),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	plan, err := Build(projects, typeFor, []Node{{"alpha", "build"}, {"beta", "test"}})
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
		mkProject("libs/repojob", "repojob", "mock/lib@1"),
		mkProject("libs/projjob", "projjob", "mock/lib@1"),
	}
	types := map[string]*projecttype.ProjectType{
		"repojob": mkType(map[string][]string{"build": {"echo r"}}, nil, projecttype.WorkingDirRepo),
		"projjob": mkType(map[string][]string{"build": {"echo p"}}, nil, projecttype.WorkingDirProject),
	}
	typeFor := func(p discovery.Project) (*projecttype.ProjectType, error) { return types[p.Manifest.Name], nil }

	plan, err := Build(projects, typeFor, []Node{{"repojob", "build"}, {"projjob", "build"}})
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
