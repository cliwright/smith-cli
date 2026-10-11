package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/manifest"
)

// writeServiceFiles writes services/*.yml into the repo's services root.
func writeServiceFiles(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(repo, servicesRootName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const authServiceYML = `version: 1
name: auth
components:
  - project: auth-core
  - project: auth-lambda
    targets: {package: build}
  - project: auth-tofu
    targets: {apply: deploy, destroy: destroy}
`

// authServiceFixture is the walkthrough from the services design: deploy
// builds auth-core, packages auth-lambda, and applies auth-tofu. Projects
// deliberately live under both libs/ and services/ (the service namespace
// root) to prove projects and service files coexist there.
func authServiceFixture(t *testing.T, home, repo string) {
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/shared":          {name: "shared", typ: "mock/python/lib@1"},
		"libs/auth-core":       {name: "auth-core", typ: "mock/python/lib@1", deps: []manifest.Dependency{{Project: "shared", Targets: []string{"build"}, Before: []string{"build"}}}},
		"services/auth-lambda": {name: "auth-lambda", typ: "mock/docker/image@1", deps: []manifest.Dependency{{Project: "auth-core", Targets: []string{"build"}, Before: []string{"package"}}}},
		"services/auth-tofu":   {name: "auth-tofu", typ: "mock/tofu/module@1", deps: []manifest.Dependency{{Project: "auth-lambda", Targets: []string{"package"}, Before: []string{"apply"}}}},
	}, map[string]typeSpec{
		"mock/python/lib@1":   {targets: map[string][]string{"build": {"echo py-build"}, "test": {"echo py-test"}, "lint": {"echo py-lint"}}},
		"mock/docker/image@1": {targets: map[string][]string{"package": {"echo img-package"}, "push": {"echo img-push"}}},
		"mock/tofu/module@1":  {targets: map[string][]string{"plan": {"echo tofu-plan"}, "apply": {"echo tofu-apply"}, "destroy": {"echo tofu-destroy"}}},
	})
	writeServiceFiles(t, repo, map[string]string{"auth.yml": authServiceYML})
}

func TestServicesList(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	writeServiceFiles(t, repo, map[string]string{
		"zz.yml": "version: 1\nname: zeta\ncomponents:\n  - project: shared\n",
	})
	t.Chdir(repo)

	out, err := run(t, "services")
	if err != nil {
		t.Fatalf("services: %v\noutput:\n%s", err, out)
	}
	if out != "auth\nzeta\n" {
		t.Errorf("list = %q, want sorted service names (projects under services/ must NOT appear)", out)
	}
}

func TestServicesListEmpty(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, nil)
	t.Chdir(repo)

	out, err := run(t, "services")
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	if !strings.Contains(out, "no services defined") {
		t.Errorf("output = %q, want the no-services message", out)
	}
}

func TestServicesUnknownServiceListsKnown(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	_, err := run(t, "services", "ghost")
	if err == nil || !strings.Contains(err.Error(), `unknown service "ghost" (services: auth)`) {
		t.Fatalf("error = %v", err)
	}
}

func TestServicesShow(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	out, err := run(t, "services", "auth")
	if err != nil {
		t.Fatalf("services auth: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"service auth (3 components)",
		"auth-core (mock/python/lib@1)",
		"targets: build, test, lint",
		"auth-lambda (mock/docker/image@1)",
		"targets: build (target: package), push",
		"auth-tofu (mock/tofu/module@1)",
		"targets: plan, deploy (target: apply), destroy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestServicesRunDeployWalkthrough(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "services", "auth", "deploy")
	if err != nil {
		t.Fatalf("services auth deploy: %v\noutput:\n%s", err, out)
	}
	// shared and auth-core share the python type, so both steps read
	// "echo py-build"; depends_on pulls both into the closure.
	want := []string{"echo py-build", "echo py-build", "echo img-package", "echo tofu-apply"}
	if !slices.Equal(f.runs, want) {
		t.Errorf("steps = %v, want %v (closure runs auth-core and shared via depends_on)", f.runs, want)
	}
	if !strings.Contains(out, "==> auth-tofu:apply") {
		t.Errorf("output missing node headers:\n%s", out)
	}
}

func TestServicesRunBuildMapsAndSkips(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "services", "auth", "build")
	if err != nil {
		t.Fatalf("services auth build: %v\noutput:\n%s", err, out)
	}
	want := []string{"echo py-build", "echo py-build", "echo img-package"}
	if !slices.Equal(f.runs, want) {
		t.Errorf("steps = %v, want %v (name-match core, mapped package for lambda, shared via depends_on)", f.runs, want)
	}
	if !strings.Contains(out, `skipped 1 component(s) without a "build" target: auth-tofu`) {
		t.Errorf("output missing skip report:\n%s", out)
	}
}

func TestServicesRunTestSkipsNonImplementors(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "services", "auth", "test")
	if err != nil {
		t.Fatalf("services auth test: %v\noutput:\n%s", err, out)
	}
	if !slices.Equal(f.runs, []string{"echo py-test"}) {
		t.Errorf("steps = %v, want [py-test]", f.runs)
	}
	if !strings.Contains(out, `skipped 2 component(s) without a "test" target: auth-lambda, auth-tofu`) {
		t.Errorf("output missing skip report:\n%s", out)
	}
}

func TestServicesRunRejectsOutOfVocabularyTarget(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	_, err := run(t, "services", "auth", "frisbee")
	if err == nil || !strings.Contains(err.Error(), `unknown service target "frisbee"`) {
		t.Fatalf("error = %v, want vocabulary gate", err)
	}
}

func TestServicesRunNoImplementor(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	writeServiceFiles(t, repo, map[string]string{
		"core.yml": "version: 1\nname: core-only\ncomponents:\n  - project: auth-core\n",
	})
	t.Chdir(repo)

	_, err := run(t, "services", "core-only", "push")
	if err == nil || !strings.Contains(err.Error(), `no component in service "core-only" implements target "push" (targets: build, test, lint)`) {
		t.Fatalf("error = %v, want no-implementor listing", err)
	}
}

func TestServicesGraphTargetPrintsOrderWithoutRunning(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "services", "auth", "graph", "deploy")
	if err != nil {
		t.Fatalf("graph deploy: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		`"deploy" execution order (4 nodes)`,
		"1. shared:build",
		"2. auth-core:build",
		"3. auth-lambda:package",
		"4. auth-tofu:apply",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("graph output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "skipped") {
		t.Errorf("every component participates via roots or prerequisites; no skip line expected:\n%s", out)
	}
	if len(f.runs) != 0 {
		t.Errorf("graph executed steps %v; want nothing executed", f.runs)
	}
}

func TestServicesGraphBareShowsComponentDependencies(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	out, err := run(t, "services", "auth", "graph")
	if err != nil {
		t.Fatalf("graph: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"service auth — component dependency graph",
		"auth-core\n  after: shared (external)",
		"auth-lambda\n  after: auth-core",
		"auth-tofu\n  after: auth-lambda",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("graph output missing %q:\n%s", want, out)
		}
	}
}

func TestServicesGraphDetectsProjectLevelCycle(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/cyc-a": {name: "cyc-a", typ: "mock/std/lib@1", deps: []manifest.Dependency{{Project: "cyc-b", Targets: []string{"build"}, Before: []string{"build"}}}},
		"libs/cyc-b": {name: "cyc-b", typ: "mock/std/lib@1", deps: []manifest.Dependency{{Project: "cyc-a", Targets: []string{"build"}, Before: []string{"build"}}}},
	}, nil)
	writeServiceFiles(t, repo, map[string]string{
		"cyc.yml": "version: 1\nname: cyc\ncomponents:\n  - project: cyc-a\n  - project: cyc-b\n",
	})
	t.Chdir(repo)

	_, err := run(t, "services", "cyc", "graph")
	if err == nil || !strings.Contains(err.Error(), `service "cyc" has a dependency cycle: cyc-a -> cyc-b -> cyc-a`) {
		t.Fatalf("bare graph error = %v, want project-level cycle", err)
	}

	_, err = run(t, "services", "cyc", "graph", "build")
	if err == nil || !strings.Contains(err.Error(), "dependency cycle: cyc-a:build -> cyc-b:build -> cyc-a:build") {
		t.Fatalf("target graph error = %v, want target-level cycle from the closure build", err)
	}
}

func TestServicesBrokenFileFailsLoudly(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, nil)
	writeServiceFiles(t, repo, map[string]string{
		"broken.yml": "version: 1\nname: broken\ncomponents:\n  - project: alpha\nbogus: true\n",
	})
	t.Chdir(repo)

	// A broken service file is a load-time error for the dispatch surface
	// (namespace/repo/services grammar, doctor), matching how attachments
	// behave — tree/list/sync use their own lighter loading and are
	// unaffected, since a user-authored file is not fixed by sync anyway.
	_, err := run(t, "libs")
	if err == nil || !strings.Contains(err.Error(), "schema validation failed") {
		t.Fatalf("libs error = %v, want service schema failure", err)
	}
}

func TestServicesUnknownComponentFailsLoudly(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, nil)
	writeServiceFiles(t, repo, map[string]string{
		"ghost.yml": "version: 1\nname: ghost-svc\ncomponents:\n  - project: ghost\n",
	})
	t.Chdir(repo)

	_, err := run(t, "services")
	if err == nil || !strings.Contains(err.Error(), `unknown component "ghost"`) {
		t.Fatalf("error = %v, want unknown component", err)
	}
}

func TestServicesTooManyArguments(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	authServiceFixture(t, home, repo)
	t.Chdir(repo)

	_, err := run(t, "services", "auth", "graph", "deploy", "extra")
	if err == nil || !strings.Contains(err.Error(), "too many arguments") {
		t.Fatalf("error = %v, want too many arguments", err)
	}
}
