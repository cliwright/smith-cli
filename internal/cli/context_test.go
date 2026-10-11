package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contextFixture builds a repo with: repo-level dotenv (opted in, default
// env dev), a project alpha (opted in, manifest-level entries), and a
// project beta (not opted in). The shared type sets REGISTRY in its
// environment so type-vs-dotenv precedence is visible.
func contextFixture(t *testing.T) (home, repo string) {
	t.Helper()
	home = setupHome(t)
	repo = t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1", dotenv: true},
		"libs/beta":  {name: "beta", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo build"}},
			env:     map[string]string{"REGISTRY": "type-registry", "TYPE_ONLY": "1"},
		},
	})
	repoYML := filepath.Join(repo, ".smith", "repo.yml")
	data, err := os.ReadFile(repoYML)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repoYML, append(data, []byte("dotenv: true\nsmith_env:\n  default: dev\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, content string) {
		t.Helper()
		path := filepath.Join(dir, "dotenv", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, ".env", "REGISTRY=repo-base\nREPO_BASE_ONLY=1\n")
	write(repo, ".dev.env", "REGISTRY=repo-dev\n")
	write(filepath.Join(repo, "libs", "alpha"), ".env", "REGISTRY=alpha-base\nALPHA_ONLY=1\n")
	// alpha has no overlay; beta opts out entirely
	return home, repo
}

func TestContextProjectView(t *testing.T) {
	_, repo := contextFixture(t)
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "--context")
	if err != nil {
		t.Fatalf("libs alpha --context: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	wantSubstrings := []string{
		"ALPHA_ONLY=1", "# libs/alpha/dotenv/.env",
		"REPO_BASE_ONLY=1", "# dotenv/.env",
		"SMITH_ENV=dev", "# selector (repo.yml smith_env.default)",
		"SMITH_PROJECT=alpha", "# computed",
		"SMITH_REPO_ROOT=" + repo, "# computed",
		"SMITH_TARGET=<per target>", "# computed (set per run)",
		"TYPE_ONLY=1", "# type mock/std/lib@1 environment",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Effective REGISTRY: dotenv files set it, but the TYPE environment sits
	// above all dotenv layers — so the winning source is the type.
	var registryLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "REGISTRY=") {
			registryLine = line
		}
	}
	if registryLine != "REGISTRY=type-registry  # type mock/std/lib@1 environment" {
		// exact spacing depends on column alignment; assert the essentials
		if !strings.HasPrefix(registryLine, "REGISTRY=type-registry") ||
			!strings.Contains(registryLine, "# type mock/std/lib@1 environment") {
			t.Errorf("REGISTRY line = %q, want type-registry from the type environment", registryLine)
		}
	}
	// Sorted keys.
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		keys = append(keys, strings.SplitN(line, "=", 2)[0])
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] < keys[i-1] {
			t.Errorf("keys not sorted: %v", keys)
		}
	}
}

func TestContextProjectWithTarget(t *testing.T) {
	_, repo := contextFixture(t)
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "build", "--context")
	if err != nil {
		t.Fatalf("libs alpha build --context: %v", err)
	}
	if !strings.Contains(out, "SMITH_TARGET=build") {
		t.Errorf("SMITH_TARGET not concrete:\n%s", out)
	}

	if _, err := run(t, "libs", "alpha", "nope", "--context"); err == nil ||
		!strings.Contains(err.Error(), `has no target "nope"`) {
		t.Errorf("unknown target error = %v, want has-no-target naming targets", err)
	}
}

func TestContextRepoWideTargetView(t *testing.T) {
	_, repo := contextFixture(t)
	t.Chdir(repo)

	out, err := run(t, "build", "--context")
	if err != nil {
		t.Fatalf("build --context: %v", err)
	}
	if !strings.Contains(out, "# alpha (mock/std/lib@1)") || !strings.Contains(out, "# beta (mock/std/lib@1)") {
		t.Errorf("missing per-project headers:\n%s", out)
	}
	betaSection := strings.Split(out, "# beta")[1]
	// beta opted out of PROJECT dotenv, but the repo layer applies to every
	// project; the type environment still wins REGISTRY.
	if !strings.Contains(betaSection, "REPO_BASE_ONLY=1") {
		t.Errorf("beta should see repo-level dotenv keys:\n%s", betaSection)
	}
	if !strings.Contains(betaSection, "REGISTRY=type-registry") {
		t.Errorf("beta REGISTRY should be the type value:\n%s", betaSection)
	}

	if _, err := run(t, "nope", "--context"); err == nil ||
		!strings.Contains(err.Error(), `has a "nope" target`) {
		t.Errorf("unknown target error = %v", err)
	}
}

func TestContextBareRepoView(t *testing.T) {
	_, repo := contextFixture(t)
	t.Chdir(repo)

	// Default env: base + overlay listed literally (REGISTRY appears twice).
	out, err := run(t, "--context")
	if err != nil {
		t.Fatalf("bare --context: %v", err)
	}
	if strings.Count(out, "REGISTRY=") != 2 {
		t.Errorf("bare view should list base and overlay literally:\n%s", out)
	}
	if !strings.Contains(out, "SMITH_ENV=dev") || !strings.Contains(out, "REGISTRY=repo-dev") {
		t.Errorf("bare view missing selector or overlay:\n%s", out)
	}

	// Explicit env with a matching overlay: fine.
	if _, err := run(t, "--env", "dev", "--context"); err != nil {
		t.Fatalf("bare --env dev --context: %v", err)
	}
	// Explicit env with NO overlay anywhere: the bare view still succeeds
	// (unlike project views) — a project file may satisfy a real run.
	if out, err := run(t, "--env", "test", "--context"); err != nil {
		t.Fatalf("bare --env test --context should not fail: %v", err)
	} else if strings.Contains(out, "dotenv/.test.env") {
		t.Errorf("bare view should not list a missing overlay:\n%s", out)
	}
}

func TestContextErrorParityWithRuns(t *testing.T) {
	_, repo := contextFixture(t)
	t.Chdir(repo)

	// --env test has no overlay at either level: a project view fails with
	// the same error a run gives, before anything executes.
	if _, err := run(t, "--env", "test", "libs", "alpha", "--context"); err == nil ||
		!strings.Contains(err.Error(), "dotenv/.test.env") {
		t.Errorf("project view error = %v, want the no-overlay error a run gives", err)
	}
	// With the default env, the repo-level overlay satisfies the policy even
	// for a project that opted out of project-level dotenv.
	if _, err := run(t, "libs", "beta", "--context"); err != nil {
		t.Errorf("beta --context: %v", err)
	}
	// Invalid selector is rejected the same way.
	if _, err := run(t, "--env", "BAD", "libs", "alpha", "--context"); err == nil {
		t.Error("invalid --env: no error")
	}
}

func TestContextNamespaceNeedsProjectOrTarget(t *testing.T) {
	_, repo := contextFixture(t)
	t.Chdir(repo)
	if _, err := run(t, "libs", "--context"); err == nil ||
		!strings.Contains(err.Error(), "needs a project or a target") {
		t.Errorf("libs --context error = %v", err)
	}
}
