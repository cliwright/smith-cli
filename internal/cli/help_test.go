package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/projecttype"
)

// useFakeLookPath substitutes the display-tool lookup: names in found resolve
// to their path, everything else is not on PATH.
func useFakeLookPath(t *testing.T, found map[string]string) {
	t.Helper()
	old := lookPath
	lookPath = func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = old })
}

// writeFakeTool drops an executable shell script into a fresh temp dir and
// returns its full path. Log destinations are passed through the environment
// (the scripts reference $GLOW_LOG / $PAGER_LOG).
func writeFakeTool(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// helpFixture builds a repo whose type ships help files and declares a
// manifest-overridden param.
func helpFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeDispatchRepo(t, repo,
		map[string]manifestSpec{
			"libs/alpha": {
				name:   "alpha",
				typ:    "mock/std/lib@1",
				params: map[string]string{"config_path": "ci/pyproject.toml"},
			},
		},
		map[string]typeSpec{
			"mock/std/lib@1": {
				targets: map[string][]string{
					"build": {"uv run ruff check --config '{{.config_path}}' .", "uv build"},
					"show":  {"echo hi"},
				},
				descs: map[string]string{"build": "lint and build"},
				env:   map[string]string{"PYTHONUNBUFFERED": "1"},
				params: map[string]projecttype.Param{
					"config_path": {Default: "pyproject.toml", Description: "ruff config location"},
				},
				files: map[string]string{
					"help/project.md": "# Alpha project\n\nProject prose here.\n",
					"help/build.md":   "Build prose here.\n",
				},
			},
		})
	return repo
}

// plainDisplay forces the stdout path: no glow, no less/more, no SMITH_PAGER.
func plainDisplay(t *testing.T) {
	t.Helper()
	useFakeLookPath(t, nil)
	t.Setenv("SMITH_PAGER", "")
}

func TestNsTypeHelpPlainOutput(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)
	plainDisplay(t)

	out, err := run(t, "libs", "alpha", "help")
	if err != nil {
		t.Fatalf("libs alpha help: %v\noutput:\n%s", err, out)
	}
	typeDir := filepath.Join(repo, ".smith", "types", "mock", "std", "lib@v1")
	for _, want := range []string{
		"# alpha (type mock/std/lib@1)",
		"test type",
		"- working_dir: `project`",
		"| `config_path` | ci/pyproject.toml — ruff config location |",
		"| `SMITH_TYPE_DIR` | " + typeDir + " |",
		"PYTHONUNBUFFERED=1",
		"# Alpha project",
		"Project prose here.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("type help missing %q:\n%s", want, out)
		}
	}
}

func TestNsTargetHelpRendersStepsVarsAndProse(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)
	plainDisplay(t)

	out, err := run(t, "libs", "alpha", "build", "help")
	if err != nil {
		t.Fatalf("libs alpha build help: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"# alpha:build",
		"lint and build",
		"```sh\nuv run ruff check --config 'ci/pyproject.toml' .\nuv build\n```",
		"## Vars used",
		"- `config_path` = ci/pyproject.toml",
		"Build prose here.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("target help missing %q:\n%s", want, out)
		}
	}
	// Only referenced vars appear in "Vars used".
	if strings.Contains(out, "- `SMITH_REPO_ROOT` =") {
		t.Errorf("Vars used lists an unreferenced var:\n%s", out)
	}
}

func TestNsTargetHelpWithoutFileShowsGeneratedOnly(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)
	plainDisplay(t)

	out, err := run(t, "libs", "alpha", "show", "help")
	if err != nil {
		t.Fatalf("libs alpha show help: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "```sh\necho hi\n```") {
		t.Errorf("target help missing the rendered steps:\n%s", out)
	}
	if !strings.Contains(out, "_This type ships no help/show.md") {
		t.Errorf("target help missing the no-file note:\n%s", out)
	}
	if strings.Contains(out, "## Vars used") {
		t.Errorf("echo hi references no vars, want no Vars used section:\n%s", out)
	}
}

func TestNsTargetHelpUnknownTarget(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)
	plainDisplay(t)

	_, err := run(t, "libs", "alpha", "nope", "help")
	if err == nil || !strings.Contains(err.Error(), `has no target "nope"`) ||
		!strings.Contains(err.Error(), "build") {
		t.Fatalf("error = %v, want unknown-target error listing targets", err)
	}
}

func TestNsHelpPipesThroughGlowAndPager(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)

	glowLog := filepath.Join(t.TempDir(), "glow.log")
	pagerLog := filepath.Join(t.TempDir(), "pager.log")
	t.Setenv("GLOW_LOG", glowLog)
	t.Setenv("PAGER_LOG", pagerLog)
	glow := writeFakeTool(t, "glow", "tee \"$GLOW_LOG\"\necho GLOW-MARKER >> \"$GLOW_LOG\"\n")
	pager := writeFakeTool(t, "pager", "cat >> \"$PAGER_LOG\"\n")
	useFakeLookPath(t, map[string]string{"glow": glow})
	t.Setenv("SMITH_PAGER", pager)

	out, err := run(t, "libs", "alpha", "help")
	if err != nil {
		t.Fatalf("libs alpha help: %v\noutput:\n%s", err, out)
	}
	paged, err := os.ReadFile(pagerLog)
	if err != nil {
		t.Fatalf("pager log missing: %v", err)
	}
	if !strings.Contains(string(paged), "# alpha (type mock/std/lib@1)") {
		t.Errorf("pager received:\n%s", paged)
	}
	if strings.Contains(string(paged), "GLOW-MARKER") {
		t.Errorf("pager received glow's log marker:\n%s", paged)
	}
	glowed, err := os.ReadFile(glowLog)
	if err != nil {
		t.Fatalf("glow log missing: %v", err)
	}
	if !strings.Contains(string(glowed), "GLOW-MARKER") {
		t.Errorf("glow never ran: log = %q", glowed)
	}
	if out != "" {
		t.Errorf("stdout = %q, want everything routed through the pager", out)
	}
}

func TestNsHelpUsesSmithPagerOverride(t *testing.T) {
	repo := helpFixture(t)
	t.Chdir(repo)

	pagerLog := filepath.Join(t.TempDir(), "pager.log")
	t.Setenv("PAGER_LOG", pagerLog)
	pager := writeFakeTool(t, "pager", "cat >> \"$PAGER_LOG\"\n")
	useFakeLookPath(t, nil) // no glow, no less/more
	t.Setenv("SMITH_PAGER", pager)

	out, err := run(t, "libs", "alpha", "show", "help")
	if err != nil {
		t.Fatalf("libs alpha show help: %v\noutput:\n%s", err, out)
	}
	paged, err := os.ReadFile(pagerLog)
	if err != nil {
		t.Fatalf("pager log missing: %v", err)
	}
	if !strings.Contains(string(paged), "# alpha:show") {
		t.Errorf("pager received:\n%s", paged)
	}
}

func TestRepoAttachmentHelp(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo,
		map[string]manifestSpec{},
		map[string]typeSpec{
			"repo/uv/workspace@1": {
				targets: map[string][]string{"setup": {"echo setup"}},
				files:   map[string]string{"help/project.md": "Workspace prose.\n"},
			},
		},
		map[string]config.RepoTarget{"python": {Type: "repo/uv/workspace@1"}},
	)
	t.Chdir(repo)
	plainDisplay(t)

	out, err := run(t, "repo", "python", "help")
	if err != nil {
		t.Fatalf("repo python help: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Workspace prose.") {
		t.Errorf("attachment type help missing the prose:\n%s", out)
	}

	out, err = run(t, "repo", "python", "setup", "help")
	if err != nil {
		t.Fatalf("repo python setup help: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "# python:setup") || !strings.Contains(out, "```sh\necho setup\n```") {
		t.Errorf("attachment target help missing generated content:\n%s", out)
	}
}
