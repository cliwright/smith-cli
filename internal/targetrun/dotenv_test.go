package targetrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/projecttype"
)

// writeDotenvFile writes one file under dir/dotenv/, creating the directory.
func writeDotenvFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, "dotenv", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildDotenvPlan builds a one-project ("alpha", dir libs/alpha) plan rooted
// at repo; projectDotenv opts the project level in via its manifest.
func buildDotenvPlan(t *testing.T, repo string, projectDotenv bool, typeEnv, manifestEnv map[string]string) *Plan {
	t.Helper()
	proj := discovery.Project{
		Dir: "libs/alpha",
		Manifest: &manifest.Manifest{
			Version:     1,
			Name:        "alpha",
			Type:        "mock/lib@1",
			Dotenv:      projectDotenv,
			Environment: manifestEnv,
		},
	}
	typ := &projecttype.ProjectType{
		Name:        "mock/lib",
		Version:     1,
		Description: "x",
		Tools:       []string{"sh"},
		Environment: typeEnv,
		Targets:     map[string]projecttype.Target{"build": {Steps: []string{"echo ok"}}},
		WorkingDir:  projecttype.WorkingDirRepo,
	}
	typeFor := func(discovery.Project) (*projecttype.ProjectType, error) { return typ, nil }
	plan, err := Build([]discovery.Project{proj}, typeFor, []Node{{"alpha", "build"}}, repo)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return plan
}

func runDotenvPlan(t *testing.T, plan *Plan, repo string) []string {
	t.Helper()
	f := &fakeRunner{}
	if err := plan.Execute(context.Background(), f, repo, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(f.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(f.runs))
	}
	return f.runs[0].env
}

func TestPrepareDotenvFullOrdering(t *testing.T) {
	t.Setenv("CHAIN", "os") // the lowest layer; everything must override it
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "CHAIN=repo-base\nREPO_BASE_ONLY=1\n")
	writeDotenvFile(t, repo, ".dev.env", "CHAIN=repo-overlay\nREPO_OVERLAY_ONLY=1\n")
	projDir := filepath.Join(repo, "libs", "alpha")
	writeDotenvFile(t, projDir, ".env", "CHAIN=proj-base\nPROJ_BASE_ONLY=1\n")
	writeDotenvFile(t, projDir, ".dev.env", "CHAIN=proj-overlay\nPROJ_OVERLAY_ONLY=1\n")

	plan := buildDotenvPlan(t, repo, true,
		map[string]string{"CHAIN": "type", "TYPE_ONLY": "1"},
		map[string]string{"CHAIN": "manifest", "MANIFEST_ONLY": "1"})
	plan.RepoDotenv = true
	plan.SmithEnv = "dev"
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("PrepareDotenv: %v", err)
	}
	env := runDotenvPlan(t, plan, repo)

	cases := []struct{ key, want string }{
		{"CHAIN", "manifest"}, // os < dotenv×4 < type < manifest
		{"REPO_BASE_ONLY", "1"},
		{"REPO_OVERLAY_ONLY", "1"},
		{"PROJ_BASE_ONLY", "1"},
		{"PROJ_OVERLAY_ONLY", "1"},
		{"TYPE_ONLY", "1"},
		{"MANIFEST_ONLY", "1"},
		{"SMITH_ENV", "dev"},
	}
	for _, c := range cases {
		if got, ok := lastEnvValue(env, c.key); !ok || got != c.want {
			t.Errorf("%s = %q (present %v), want %q", c.key, got, ok, c.want)
		}
	}
}

func TestPrepareDotenvLayersOrderWithoutDeclaredEnv(t *testing.T) {
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "CHAIN=repo-base\n")
	writeDotenvFile(t, repo, ".dev.env", "CHAIN=repo-overlay\n")
	projDir := filepath.Join(repo, "libs", "alpha")
	writeDotenvFile(t, projDir, ".env", "CHAIN=proj-base\n")
	writeDotenvFile(t, projDir, ".dev.env", "CHAIN=proj-overlay\n")

	plan := buildDotenvPlan(t, repo, true, nil, nil)
	plan.RepoDotenv = true
	plan.SmithEnv = "dev"
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("PrepareDotenv: %v", err)
	}
	if got, _ := lastEnvValue(runDotenvPlan(t, plan, repo), "CHAIN"); got != "proj-overlay" {
		t.Errorf("CHAIN = %q, want proj-overlay (project overlay is the top dotenv layer)", got)
	}
}

func TestPrepareDotenvBaseOnlyWhenNoEnvSelected(t *testing.T) {
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "CHAIN=repo-base\n")
	writeDotenvFile(t, repo, ".dev.env", "CHAIN=repo-overlay\nREPO_OVERLAY_ONLY=1\n")
	projDir := filepath.Join(repo, "libs", "alpha")
	writeDotenvFile(t, projDir, ".env", "CHAIN=proj-base\n")
	writeDotenvFile(t, projDir, ".dev.env", "CHAIN=proj-overlay\n")

	plan := buildDotenvPlan(t, repo, true, nil, nil)
	plan.RepoDotenv = true
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("PrepareDotenv: %v", err)
	}
	env := runDotenvPlan(t, plan, repo)
	if got, _ := lastEnvValue(env, "CHAIN"); got != "proj-base" {
		t.Errorf("CHAIN = %q, want proj-base (no environment selected: overlays never load)", got)
	}
	if _, ok := lastEnvValue(env, "REPO_OVERLAY_ONLY"); ok {
		t.Error("REPO_OVERLAY_ONLY present although it only exists in an unselected overlay file")
	}
}

func TestPrepareDotenvOptOutContributesNothing(t *testing.T) {
	repo := t.TempDir() // no dotenv/ dirs at all
	plan := buildDotenvPlan(t, repo, false, nil, nil)
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("PrepareDotenv with no opt-in: %v", err)
	}
	if got, ok := lastEnvValue(runDotenvPlan(t, plan, repo), "REPO_BASE_ONLY"); ok {
		t.Errorf("REPO_BASE_ONLY = %q present without opt-in", got)
	}
}

func TestPrepareDotenvMissingBase(t *testing.T) {
	repo := t.TempDir()

	plan := buildDotenvPlan(t, repo, false, nil, nil)
	plan.RepoDotenv = true
	err := plan.PrepareDotenv(repo)
	if err == nil || !strings.Contains(err.Error(), "repo.yml") || !strings.Contains(err.Error(), "dotenv") {
		t.Errorf("missing repo base error = %v, want it to name repo.yml and the dotenv path", err)
	}

	plan = buildDotenvPlan(t, repo, true, nil, nil) // project opted in, no dir
	err = plan.PrepareDotenv(repo)
	if err == nil || !strings.Contains(err.Error(), `"alpha"`) {
		t.Errorf("missing project base error = %v, want it to name the project", err)
	}
}

func TestPrepareDotenvSelectedEnvNeedsAnOverlaySomewhere(t *testing.T) {
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "CHAIN=repo-base\n")

	plan := buildDotenvPlan(t, repo, false, nil, nil)
	plan.RepoDotenv = true
	plan.SmithEnv = "dev"
	if err := plan.PrepareDotenv(repo); err == nil || !strings.Contains(err.Error(), "dotenv/.dev.env") {
		t.Errorf("selected env without overlay error = %v, want it to name dotenv/.dev.env", err)
	}

	// A dotenv-disabled run is fine, though: SMITH_ENV still reaches the steps.
	plan = buildDotenvPlan(t, repo, false, nil, nil)
	plan.SmithEnv = "dev"
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("PrepareDotenv with dotenv disabled everywhere: %v", err)
	}
	env := runDotenvPlan(t, plan, repo)
	if got, ok := lastEnvValue(env, "SMITH_ENV"); !ok || got != "dev" {
		t.Errorf("SMITH_ENV = %q (present %v), want dev — the selector exports even without dotenv files", got, ok)
	}
}

func TestPrepareDotenvProjectOverlaySuffices(t *testing.T) {
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "CHAIN=repo-base\n") // repo has no overlay
	projDir := filepath.Join(repo, "libs", "alpha")
	writeDotenvFile(t, projDir, ".env", "CHAIN=proj-base\n")
	writeDotenvFile(t, projDir, ".dev.env", "CHAIN=proj-overlay\n")

	plan := buildDotenvPlan(t, repo, true, nil, nil)
	plan.RepoDotenv = true
	plan.SmithEnv = "dev"
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("PrepareDotenv: %v", err)
	}
	if got, _ := lastEnvValue(runDotenvPlan(t, plan, repo), "CHAIN"); got != "proj-overlay" {
		t.Errorf("CHAIN = %q, want proj-overlay", got)
	}
}

func TestPrepareDotenvParseError(t *testing.T) {
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "GOOD=1\nnot a kv line\n")

	plan := buildDotenvPlan(t, repo, false, nil, nil)
	plan.RepoDotenv = true
	err := plan.PrepareDotenv(repo)
	if err == nil || !strings.Contains(err.Error(), "not a KEY=VALUE") {
		t.Errorf("parse error = %v, want it to name the malformed line", err)
	}
}

func TestPrepareDotenvIdempotent(t *testing.T) {
	repo := t.TempDir()
	writeDotenvFile(t, repo, ".env", "CHAIN=repo-base\n")

	plan := buildDotenvPlan(t, repo, false, nil, nil)
	plan.RepoDotenv = true
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("first PrepareDotenv: %v", err)
	}
	if err := plan.PrepareDotenv(repo); err != nil {
		t.Fatalf("second PrepareDotenv: %v", err)
	}
	if got, _ := lastEnvValue(runDotenvPlan(t, plan, repo), "CHAIN"); got != "repo-base" {
		t.Errorf("CHAIN = %q, want repo-base", got)
	}
}
