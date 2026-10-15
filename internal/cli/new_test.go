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
func useFakeRunner(t *testing.T, f commandRunner) {
	t.Helper()
	old := newCommandRunner
	newCommandRunner = f
	t.Cleanup(func() { newCommandRunner = old })
}

// writingRunner fakes cookiecutter by materializing files under the
// --output-dir named in the invocation (always the trailing argument pair).
type writingRunner struct {
	*fakeRunner
	files map[string]string // slash-separated paths relative to the output dir
}

func (w *writingRunner) Run(ctx context.Context, opts runner.Options, name string, args ...string) error {
	if err := w.fakeRunner.Run(ctx, opts, name, args...); err != nil {
		return err
	}
	outDir := args[len(args)-1]
	for rel, content := range w.files {
		p := filepath.Join(outDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
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
		{args: []string{"new", "python/widget", "--version", "1"}, wantErr: `unknown kind "widget"`},
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

func TestNewTemplatesPathSourceIsUsed(t *testing.T) {
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
		"    templates: {path: ../local-templates}\n" + // local dev source, no git URL
		"workspace:\n  project_roots:\n    - libs\n"
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, err := run(t, "new", "python/lib", "--version", "1")
	if err != nil {
		t.Fatalf("new: %v\noutput:\n%s", err, out)
	}
	if len(f.ran) != 1 {
		t.Fatalf("cookiecutter invocations = %d, want 1", len(f.ran))
	}
	// cookiecutter accepts a local directory in place of a repo URL.
	if got := f.ran[0].args[0]; got != "../local-templates" {
		t.Errorf("cookiecutter source = %q, want ../local-templates", got)
	}
}

const validTypeJSON = `{"name": "python/acme/lib", "version": 1, "description": "acme python lib", "tools": ["uv"], "targets": {"build": ["uv build"]}}`

func TestNewTypeKindScaffoldsValidatesAndHintsSmelt(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, &writingRunner{fakeRunner: f, files: map[string]string{
		"python/acme/lib/v1/project-type.json": validTypeJSON,
	}})

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
	t.Chdir(repo)

	out, err := run(t, "new", "smith/type", "--version", "1")
	if err != nil {
		t.Fatalf("new: %v\noutput:\n%s", err, out)
	}
	if got := f.ran[0].args[2]; got != "smith/type/v1" {
		t.Errorf("--directory arg = %q, want smith/type/v1", got)
	}
	if !strings.Contains(out, "Validated python/acme/lib@1") {
		t.Errorf("output missing validation line:\n%s", out)
	}
	if !strings.Contains(out, "next: smith smelt --path types/python/acme/lib/v1") {
		t.Errorf("output missing smelt hint:\n%s", out)
	}

	cfg, err := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Contains(cfg.Workspace.ProjectRoots, "types") {
		t.Errorf("project_roots = %v, want types appended", cfg.Workspace.ProjectRoots)
	}
}

func TestNewTypeKindValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "version disagrees with v-dir",
			content: `{"name": "python/acme/lib", "version": 2, "description": "x", "tools": ["uv"], "targets": {"build": ["uv build"]}}`,
			wantErr: "registry-layout check",
		},
		{
			name:    "schema violation",
			content: `{"name": "python/acme/lib", "version": 1}`,
			wantErr: "scaffolded type is invalid",
		},
		{
			name:    "not JSON",
			content: `{not json`,
			wantErr: "scaffolded type is invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeRunner{}
			useFakeRunner(t, &writingRunner{fakeRunner: f, files: map[string]string{
				"python/acme/lib/v1/project-type.json": tt.content,
			}})
			repo := t.TempDir()
			writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
			t.Chdir(repo)

			_, err := run(t, "new", "smith/type", "--version", "1")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
			if len(f.ran) != 1 {
				t.Errorf("cookiecutter invocations = %d, want 1 (it ran; the check failed)", len(f.ran))
			}
			cfg, loadErr := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
			if loadErr != nil {
				t.Fatalf("config.Load: %v", loadErr)
			}
			if slices.Contains(cfg.Workspace.ProjectRoots, "types") {
				t.Errorf("project_roots = %v, want types NOT appended on validation failure", cfg.Workspace.ProjectRoots)
			}
		})
	}
}

func TestNewTypeKindNoManifestIsANote(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, &writingRunner{fakeRunner: f, files: map[string]string{}})

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
	t.Chdir(repo)

	out, err := run(t, "new", "smith/type", "--version", "1")
	if err != nil {
		t.Fatalf("new: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "no new project-type.json") {
		t.Errorf("output missing note:\n%s", out)
	}
	cfg, err := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Contains(cfg.Workspace.ProjectRoots, "types") {
		t.Errorf("project_roots = %v, want types appended (cookiecutter succeeded)", cfg.Workspace.ProjectRoots)
	}
}

func TestNewTypeKindMultipleManifestsFail(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, &writingRunner{fakeRunner: f, files: map[string]string{
		"python/acme/lib/v1/project-type.json":  validTypeJSON,
		"python/acme/tool/v1/project-type.json": strings.Replace(validTypeJSON, `"kind"`, `"kind"`, 1),
	}})

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
	t.Chdir(repo)

	_, err := run(t, "new", "smith/type", "--version", "1")
	if err == nil || !strings.Contains(err.Error(), "expected exactly one new project-type.json") {
		t.Fatalf("error = %v, want multiple-manifests error", err)
	}
}

func TestNewTypeKindV2NextToExistingV1(t *testing.T) {
	f := &fakeRunner{}
	useFakeRunner(t, &writingRunner{fakeRunner: f, files: map[string]string{
		"python/acme/lib/v2/project-type.json": strings.Replace(validTypeJSON, `"version": 1`, `"version": 2`, 1),
	}})

	repo := t.TempDir()
	writeNewRepo(t, repo, "https://github.com/cliwright/smith-project-templates")
	t.Chdir(repo)
	// An earlier scaffold already lives in the tree; only v2 is new.
	v1 := filepath.Join(repo, "types", "python", "acme", "lib", "v1")
	if err := os.MkdirAll(v1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v1, "project-type.json"), []byte(validTypeJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "new", "smith/type", "--version", "1")
	if err != nil {
		t.Fatalf("new: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Validated python/acme/lib@2") {
		t.Errorf("output should validate the NEW version:\n%s", out)
	}
	if !strings.Contains(out, "next: smith smelt --path types/python/acme/lib/v2") {
		t.Errorf("output missing v2 smelt hint:\n%s", out)
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
