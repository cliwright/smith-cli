package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/typeref"
)

// impactProjects is the fruit line, upstream first: oranges <- {apples,
// grapes} <- bananas, plus an unrelated project outside the closure.
func impactProjects() []discovery.Project {
	return []discovery.Project{
		project("libs/oranges", "oranges", "python/astral/lib@1"),
		projectWithDeps("libs/apples", "apples", "python/astral/lib@1", "oranges"),
		projectWithDeps("libs/grapes", "grapes", "python/astral/lib@1", "oranges"),
		projectWithDeps("services/bananas", "bananas", "python/astral/service@2", "apples", "grapes"),
		project("tools/standalone", "standalone", "go/std/tool@1"),
	}
}

func closureNames(t *testing.T, projects []discovery.Project, seedName string) []string {
	t.Helper()
	var seed discovery.Project
	for _, p := range projects {
		if p.Manifest.Name == seedName {
			seed = p
			break
		}
	}
	if seed.Manifest == nil {
		t.Fatalf("no seed named %q in fixture", seedName)
	}
	closure, err := impactClosure(projects, seed)
	if err != nil {
		t.Fatalf("impactClosure(%s): %v", seedName, err)
	}
	return projectNames(closure)
}

func TestImpactClosure(t *testing.T) {
	t.Run("transitive closure dependency-first with diamond", func(t *testing.T) {
		got := closureNames(t, impactProjects(), "oranges")
		want := []string{"oranges", "apples", "grapes", "bananas"}
		if !slices.Equal(got, want) {
			t.Errorf("closure = %v, want %v (seed first, then dependents)", got, want)
		}
	})

	t.Run("mid-chain seed pulls only its own downstream", func(t *testing.T) {
		got := closureNames(t, impactProjects(), "apples")
		want := []string{"apples", "bananas"}
		if !slices.Equal(got, want) {
			t.Errorf("closure = %v, want %v (grapes is not downstream of apples)", got, want)
		}
	})

	t.Run("seed without dependents is alone", func(t *testing.T) {
		got := closureNames(t, impactProjects(), "standalone")
		if !slices.Equal(got, []string{"standalone"}) {
			t.Errorf("closure = %v, want [standalone]", got)
		}
	})

	t.Run("order is independent of input order", func(t *testing.T) {
		projects := impactProjects()
		// Reverse the slice: the closure order must not change.
		for i, j := 0, len(projects)-1; i < j; i, j = i+1, j-1 {
			projects[i], projects[j] = projects[j], projects[i]
		}
		got := closureNames(t, projects, "oranges")
		want := []string{"oranges", "apples", "grapes", "bananas"}
		if !slices.Equal(got, want) {
			t.Errorf("closure = %v, want %v regardless of input order", got, want)
		}
	})
}

// fruitOverrides gives every fruit-line project its own type with a
// distinct build step so the fake runner can prove execution order.
func fruitOverrides() map[string]typeSpec {
	step := func(s string) map[string][]string { return map[string][]string{"build": {s}} }
	return map[string]typeSpec{
		"mock/fruit/oranges@1": {targets: step("echo oranges-build")},
		"mock/fruit/apples@1":  {targets: step("echo apples-build")},
		"mock/fruit/grapes@1":  {targets: step("echo grapes-build")},
		"mock/fruit/bananas@1": {targets: step("echo bananas-build")},
	}
}

// impactRepo builds a repo with the fruit line: apples and grapes depend on
// oranges, bananas (in services/, cross-namespace) depends on both.
func impactRepo(t *testing.T, overrides map[string]typeSpec) string {
	t.Helper()
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/oranges": {name: "oranges", typ: "mock/fruit/oranges@1"},
		"libs/apples": {name: "apples", typ: "mock/fruit/apples@1",
			deps: []manifest.Dependency{{Project: "oranges", Targets: []string{"build"}, Before: []string{"build"}}}},
		"libs/grapes": {name: "grapes", typ: "mock/fruit/grapes@1",
			deps: []manifest.Dependency{{Project: "oranges", Targets: []string{"build"}, Before: []string{"build"}}}},
		"services/bananas": {name: "bananas", typ: "mock/fruit/bananas@1",
			deps: []manifest.Dependency{
				{Project: "apples", Targets: []string{"build"}, Before: []string{"build"}},
				{Project: "grapes", Targets: []string{"build"}, Before: []string{"build"}},
			}},
	}, overrides)
	return repo
}

func TestImpactPrintForm(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)

	want := "oranges\napples\ngrapes\nbananas\n"
	for _, args := range [][]string{{"libs", "oranges", "impact"}, {"oranges", "impact"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != want {
			t.Errorf("%v output = %q, want %q", args, out, want)
		}
	}
}

// The print form is pure graph inspection: no types are installed and no
// lock exists, and it still works (and never touches trust or custody).
func TestImpactPrintWithoutTypes(t *testing.T) {
	repo := t.TempDir()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"registries:\n  - name: r\n    types: {git: https://example.com/types}\n    templates: {git: https://example.com/templates}\n" +
		"workspace:\n  project_roots:\n    - libs\n    - services\n"
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}
	manifests := map[string][2]string{
		"libs/oranges":     {"oranges", "python/astral/lib@1"},
		"libs/apples":      {"apples", "python/astral/lib@1"},
		"services/bananas": {"bananas", "python/astral/lib@1"},
	}
	deps := map[string][]string{"apples": {"oranges"}, "bananas": {"apples"}}
	for dir, nt := range manifests {
		full := filepath.Join(repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("version: 1\nname: %s\ntype: %s\n", nt[0], nt[1])
		if len(deps[nt[0]]) > 0 {
			body += "depends_on:\n"
			for _, dep := range deps[nt[0]] {
				body += fmt.Sprintf("  - %s\n", dep)
			}
		}
		if err := os.WriteFile(filepath.Join(full, "smith.yml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)

	out, err := run(t, "oranges", "impact")
	if err != nil {
		t.Fatalf("impact with no types installed: %v", err)
	}
	if want := "oranges\napples\nbananas\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestImpactRunOrder(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)

	fake := &fakeExec{}
	useFakeExec(t, fake)
	out, err := run(t, "oranges", "impact", "build")
	if err != nil {
		t.Fatalf("impact build: %v\noutput:\n%s", err, out)
	}
	want := []string{"echo oranges-build", "echo apples-build", "echo grapes-build", "echo bananas-build"}
	if !slices.Equal(fake.runs, want) {
		t.Errorf("execution order = %v, want %v (seed first, cross-namespace closure, one plan)", fake.runs, want)
	}
}

func TestImpactRunSkipSummary(t *testing.T) {
	overrides := fruitOverrides()
	// bananas declares no build target: skipped and reported.
	overrides["mock/fruit/bananas@1"] = typeSpec{targets: map[string][]string{"package": {"echo bananas-package"}}}
	repo := impactRepo(t, overrides)
	t.Chdir(repo)

	fake := &fakeExec{}
	useFakeExec(t, fake)
	out, err := run(t, "libs", "oranges", "impact", "build")
	if err != nil {
		t.Fatalf("impact build: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, `ran 3, skipped 1 (type has no 'build' target) downstream of "oranges"`) {
		t.Errorf("output missing the scoped skip summary:\n%s", out)
	}
	if got := len(fake.runs); got != 3 {
		t.Errorf("%d steps ran, want 3 (bananas skipped)", got)
	}
}

func TestImpactRunNoDeclarers(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)

	_, err := run(t, "libs", "oranges", "impact", "deploy")
	if err == nil || !strings.Contains(err.Error(), `no project downstream of "oranges" declares target "deploy"`) {
		t.Fatalf("error = %v, want the no-declarers error", err)
	}
}

func TestImpactRunTrustGate(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)
	// A closure member's type is no longer trusted: the run refuses before
	// anything executes.
	removeTrustMarker(t, repo, typeref.TypeRef{Name: "mock/fruit/bananas", Version: 1})

	fake := &fakeExec{}
	useFakeExec(t, fake)
	_, err := run(t, "oranges", "impact", "build")
	if err == nil || !strings.Contains(err.Error(), "refusing to run") ||
		!strings.Contains(err.Error(), "mock/fruit/bananas@1") {
		t.Fatalf("error = %v, want the trust gate naming bananas's type", err)
	}
	if len(fake.runs) != 0 {
		t.Errorf("%d steps ran despite the gate, want 0", len(fake.runs))
	}
}

func TestImpactGrammarTooManyArguments(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)

	_, err := run(t, "libs", "oranges", "impact", "build", "extra")
	if err == nil || !strings.Contains(err.Error(), "too many arguments: oranges impact build extra") {
		t.Fatalf("error = %v, want the arity error", err)
	}
}

func TestImpactUnknownProject(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)

	_, err := run(t, "libs", "nope", "impact")
	if err == nil || !strings.Contains(err.Error(), `unknown project "nope" in namespace "libs"`) {
		t.Fatalf("print form error = %v, want unknownProjectError", err)
	}
	_, err = run(t, "libs", "nope", "impact", "build")
	if err == nil || !strings.Contains(err.Error(), `unknown project "nope" in namespace "libs"`) {
		t.Fatalf("run form error = %v, want unknownProjectError", err)
	}
}

// TestImpactReservedWord: `impact` in the target position is reserved, with
// TARGET WINS for the bare `smith <ns> impact` form and the print/run forms
// shadowing a project's own target literally named "impact" — the same
// shadowing rule as list/help.
func TestImpactReservedWord(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/impact":  {name: "impact", typ: "mock/std/lib@1"},
		"libs/oranges": {name: "oranges", typ: "mock/fruit/oranges@1"},
	}, map[string]typeSpec{
		// oranges declares a target literally named "impact".
		"mock/fruit/oranges@1": {targets: map[string][]string{"impact": {"echo impact-ran"}}},
	})
	t.Chdir(repo)

	// Bare `smith libs impact`: TARGET WINS — the project named "impact"
	// does not shadow the namespace-wide target run (nothing declares an
	// "impact" target on THAT project; oranges's own is addressed below).
	fake := &fakeExec{}
	useFakeExec(t, fake)
	out, err := run(t, "libs", "impact")
	if err != nil {
		t.Fatalf("libs impact: %v\noutput:\n%s", err, out)
	}
	if !slices.Equal(fake.runs, []string{"echo impact-ran"}) {
		t.Errorf("steps = %v, want [echo impact-ran] (oranges's literal impact target ran)", fake.runs)
	}

	// `smith libs oranges impact` shadows that literal target: the reserved
	// print form wins and nothing runs.
	fake.runs = nil
	out, err = run(t, "libs", "oranges", "impact")
	if err != nil {
		t.Fatalf("oranges impact: %v", err)
	}
	if want := "oranges\n"; out != want {
		t.Errorf("output = %q, want %q (print form, not the impact target)", out, want)
	}
	if len(fake.runs) != 0 {
		t.Errorf("steps ran despite the print form: %v", fake.runs)
	}
}

// TestImpactHelpPosition: [name, impact, help] reads as the impact RUN of a
// target literally named "help" (the impact branch precedes the target-help
// branch), not as help for a target named "impact".
func TestImpactHelpPosition(t *testing.T) {
	repo := impactRepo(t, fruitOverrides())
	t.Chdir(repo)

	_, err := run(t, "libs", "oranges", "impact", "help")
	if err == nil || !strings.Contains(err.Error(), `no project downstream of "oranges" declares target "help"`) {
		t.Fatalf("error = %v, want the impact-run reading of a target named help", err)
	}
}
