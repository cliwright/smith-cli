package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/runner"
)

// fakeRunner records invocations instead of running them.
type fakeRunner struct {
	lookPathErr error
	runErr      error
	lookedFor   []string
	ran         []recordedRun
}

type recordedRun struct {
	name string
	args []string
	opts runner.Options
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	f.lookedFor = append(f.lookedFor, name)
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRunner) Run(_ context.Context, opts runner.Options, name string, args ...string) error {
	f.ran = append(f.ran, recordedRun{name: name, args: args, opts: opts})
	return f.runErr
}

// useFakeRunner installs the fake and returns it.
func useFakeRunner(t *testing.T, f *fakeRunner) {
	t.Helper()
	old := newCommandRunner
	newCommandRunner = f
	t.Cleanup(func() { newCommandRunner = old })
}

// writeNewRepo creates an initialized repo whose repo.yml lists the given
// registry sources.
func writeNewRepo(t *testing.T, repo, templatesGit string) {
	t.Helper()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"# Registry sources, in priority order: first match wins.\n" +
		"registries:\n" +
		"  - name: cliwright\n" +
		"    types: {git: https://github.com/cliwright/smith-project-types}\n" +
		fmt.Sprintf("    templates: {git: %s}\n", templatesGit) +
		"workspace:\n  project_roots:\n    - libs\n"
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewScaffoldsAndDeclaresRoot(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, f)

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
	t.Chdir(repo)

	out, err := run(t, "new", "python/lib", "--version", "1")
	if err != nil {
		t.Fatalf("new: %v\noutput:\n%s", err, out)
	}

	// Root dir created, cookiecutter invoked with the right arguments.
	if info, err := os.Stat(filepath.Join(repo, "libs")); err != nil || !info.IsDir() {
		t.Errorf("libs root missing: %v", err)
	}
	if len(f.ran) != 1 {
		t.Fatalf("cookiecutter invocations = %d, want 1", len(f.ran))
	}
	inv := f.ran[0]
	if inv.name != "cookiecutter" {
		t.Errorf("ran %q, want cookiecutter", inv.name)
	}
	wantArgs := []string{
		"https://github.com/cliwright/smith-project-templates",
		"--directory", "python/lib/v1",
		"--output-dir", filepath.Join(repo, "libs"),
	}
	if !slices.Equal(inv.args, wantArgs) {
		t.Errorf("args = %v, want %v", inv.args, wantArgs)
	}
	if inv.opts.Stdin == nil || inv.opts.Stdout == nil || inv.opts.Stderr == nil {
		t.Errorf("stdio not wired for interactive use: %+v", inv.opts)
	}

	// repo.yml: libs already declared (seeded), so no duplicate, no rewrite
	// of tools; comments survive a load.
	cfg, err := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if count := countOccurrences(cfg.Workspace.ProjectRoots, "libs"); count != 1 {
		t.Errorf("project_roots = %v, want libs exactly once", cfg.Workspace.ProjectRoots)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# Registry sources, in priority order") {
		t.Error("repo.yml lost its comments")
	}

	if !strings.Contains(out, "Created project from template python/lib/v1") {
		t.Errorf("output missing summary:\n%s", out)
	}
}

func TestNewAppendsRootWhenMissing(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, f)

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
	// The fixture's project_roots already contain only "libs"; scaffolding a
	// tool must append "tools".
	t.Chdir(repo)
	repoYMLPath := filepath.Join(repo, ".smith", "repo.yml")
	out, err := run(t, "new", "go/tool", "--version", "3")
	if err != nil {
		t.Fatalf("new: %v\noutput:\n%s", err, out)
	}

	cfg, err := config.Load(repoYMLPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Contains(cfg.Workspace.ProjectRoots, "tools") {
		t.Errorf("project_roots = %v, want tools appended", cfg.Workspace.ProjectRoots)
	}
	if got := f.ran[0].args[2]; got != "go/tool/v3" {
		t.Errorf("--directory arg = %q, want go/tool/v3", got)
	}
	if !strings.Contains(out, "Added tools to workspace.project_roots") {
		t.Errorf("output missing root-append notice:\n%s", out)
	}
	// tools appended but comments intact.
	raw, _ := os.ReadFile(repoYMLPath)
	if !strings.Contains(string(raw), "# Registry sources, in priority order: first match wins.") {
		t.Error("repo.yml lost its registry comment")
	}
}

func TestNewArgValidation(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, f)
	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/templates")
	t.Chdir(repo)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{args: []string{"new", "python", "--version", "1"}, wantErr: "want <language>/<kind>"},
		{args: []string{"new", "python/lib/extra", "--version", "1"}, wantErr: "want <language>/<kind>"},
		{args: []string{"new", "python/", "--version", "1"}, wantErr: "want <language>/<kind>"},
		{args: []string{"new", "python/widget", "--version", "1"}, wantErr: `unknown kind "widget" (known kinds: image, lib, service, tool)`},
		{args: []string{"new", "python/lib", "--version", "0"}, wantErr: "positive integer"},
		{args: []string{"new", "python/lib", "--version", "-2"}, wantErr: "positive integer"},
		{args: []string{"new", "python/lib"}, wantErr: "required flag"}, // --version missing
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			_, err := run(t, tt.args...)
			if err == nil {
				t.Fatalf("%v succeeded, want error containing %q", tt.args, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
			if len(f.ran) != 0 {
				t.Errorf("cookiecutter ran despite invalid args: %v", f.ran)
			}
		})
	}
}

func TestNewCookiecutterMissingIsBYOTError(t *testing.T) {
	f := &fakeRunner{lookPathErr: errors.New("not found")}
	useFakeRunner(t, f)

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/templates")
	t.Chdir(repo)

	_, err := run(t, "new", "python/lib", "--version", "1")
	if err == nil || !strings.Contains(err.Error(), "cookiecutter is required on PATH") {
		t.Fatalf("error = %v, want BYOT cookiecutter error", err)
	}
	// Preflight: no filesystem mutation happened.
	if _, statErr := os.Stat(filepath.Join(repo, "libs")); !os.IsNotExist(statErr) {
		t.Errorf("libs was created despite preflight failure (stat err = %v)", statErr)
	}
	if len(f.ran) != 0 {
		t.Errorf("cookiecutter ran despite preflight failure: %v", f.ran)
	}
}

func TestNewPropagatesCookiecutterFailure(t *testing.T) {
	f := &fakeRunner{runErr: errors.New("cookiecutter: exited with status 1")}
	useFakeRunner(t, f)

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/templates")
	t.Chdir(repo)

	_, err := run(t, "new", "python/lib", "--version", "1")
	if err == nil || !strings.Contains(err.Error(), "cookiecutter failed") {
		t.Fatalf("error = %v, want cookiecutter failure propagated", err)
	}
	// repo.yml untouched: root not declared on failure.
	cfg, loadErr := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	if count := countOccurrences(cfg.Workspace.ProjectRoots, "libs"); count != 1 {
		t.Errorf("project_roots = %v, want libs exactly once (seeded)", cfg.Workspace.ProjectRoots)
	}
}

func TestNewOutsideRepo(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, f)
	t.Chdir(t.TempDir())
	_, err := run(t, "new", "python/lib", "--version", "1")
	if err == nil || !strings.Contains(err.Error(), "not a Smith repository") {
		t.Fatalf("error = %v, want not-a-repo", err)
	}
}

func TestNewNoTemplatesRegistry(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, f)

	repo := t.TempDir()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"registries:\n" +
		"  - name: cliwright\n" +
		"    types: {git: https://github.com/cliwright/smith-project-types}\n" +
		"    templates: {path: ../local-templates}\n" + // path source, no git URL
		"workspace:\n  project_roots:\n    - libs\n"
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	_, err := run(t, "new", "python/lib", "--version", "1")
	if err == nil || !strings.Contains(err.Error(), "no registry with a templates git source") {
		t.Fatalf("error = %v, want no-templates-registry error", err)
	}
	if len(f.ran) != 0 {
		t.Errorf("cookiecutter ran without a templates source: %v", f.ran)
	}
}

func countOccurrences(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}
