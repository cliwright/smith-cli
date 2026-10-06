package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/manifest"
)

func project(dir, name, typ string) discovery.Project {
	return discovery.Project{
		Dir:      dir,
		Manifest: &manifest.Manifest{Name: name, Type: typ},
	}
}

func TestRenderTree(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs", "services", "tools"}},
	}
	projects := []discovery.Project{
		project("libs/my-lib", "my-lib", "python/astral/lib@1"),
		project("services/payments/app", "payments", "python/astral/service@2"),
		project("services/payments/infra", "payments-infra", "tofu/std/module@1"),
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "smith-test-repo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `smith-test-repo
├── libs
│   └── my-lib   (my-lib · python/astral/lib@1)
├── services
│   └── payments
│       ├── app     (payments · python/astral/service@2)
│       └── infra   (payments-infra · tofu/std/module@1)
└── tools
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestRenderTreeProjectAtRoot(t *testing.T) {
	cfg := &config.RepoConfig{
		Workspace: config.Workspace{ProjectRoots: []string{"libs"}},
	}
	projects := []discovery.Project{
		project("libs", "libs", "python/astral/lib@1"),
	}

	var buf bytes.Buffer
	if err := renderTree(&buf, "demo", cfg, projects); err != nil {
		t.Fatalf("renderTree: %v", err)
	}

	want := `demo
└── libs   (libs · python/astral/lib@1)
`
	if buf.String() != want {
		t.Errorf("renderTree output mismatch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// buildMockRepo creates an initialized repo with the given declared roots and
// projects (dir -> {name, type}) and returns its path.
func buildMockRepo(t *testing.T, roots []string, projects map[string][2]string) string {
	t.Helper()
	repo := t.TempDir()
	var rootLines strings.Builder
	for _, r := range roots {
		fmt.Fprintf(&rootLines, "    - %s\n", r)
	}
	content := "version: 1\nname: mock\n" +
		"registries:\n" +
		"  - name: r\n" +
		"    types: {git: https://example.com/types}\n" +
		"    templates: {git: https://example.com/templates}\n" +
		"workspace:\n  project_roots:\n" + rootLines.String()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, nt := range projects {
		full := filepath.Join(repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf("version: 1\nname: %s\ntype: %s\n", nt[0], nt[1])
		if err := os.WriteFile(filepath.Join(full, "smith.yml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestTreeCommandEndToEnd(t *testing.T) {
	repo := buildMockRepo(t,
		[]string{"libs", "services", "tools"},
		map[string][2]string{
			"libs/my-lib":             {"my-lib", "python/astral/lib@1"},
			"services/payments/app":   {"payments", "python/astral/service@2"},
			"services/payments/infra": {"payments-infra", "tofu/std/module@1"},
		})
	t.Chdir(repo)

	out, err := run(t, "tree")
	if err != nil {
		t.Fatalf("tree: %v\noutput:\n%s", err, out)
	}

	want := fmt.Sprintf(`%s
├── libs
│   └── my-lib   (my-lib · python/astral/lib@1)
├── services
│   └── payments
│       ├── app     (payments · python/astral/service@2)
│       └── infra   (payments-infra · tofu/std/module@1)
└── tools
`, filepath.Base(repo))
	if out != want {
		t.Errorf("tree output mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestTreeNotARepo(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := run(t, "tree")
	if err == nil {
		t.Fatal("tree outside a repo: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not a Smith repository") {
		t.Errorf("error = %v, want 'not a Smith repository'", err)
	}
}

// TestOutputChannels locks in the discipline that normal output goes to
// stdout and errors/diagnostics go to stderr, with separate buffers.
func TestOutputChannels(t *testing.T) {
	t.Run("tree output on stdout", func(t *testing.T) {
		repo := buildMockRepo(t, []string{"libs"}, map[string][2]string{
			"libs/only": {"only", "python/astral/lib@1"},
		})
		t.Chdir(repo)
		cmd := newRootCmd()
		var so, se bytes.Buffer
		cmd.SetOut(&so)
		cmd.SetErr(&se)
		cmd.SetArgs([]string{"tree"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("tree: %v", err)
		}
		if !strings.Contains(so.String(), "libs") {
			t.Errorf("stdout missing tree output:\n%s", so.String())
		}
		if se.Len() != 0 {
			t.Errorf("stderr = %q, want empty", se.String())
		}
	})

	t.Run("error on stderr", func(t *testing.T) {
		t.Chdir(t.TempDir())
		cmd := newRootCmd()
		var so, se bytes.Buffer
		cmd.SetOut(&so)
		cmd.SetErr(&se)
		cmd.SetArgs([]string{"tree"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("tree outside a repo: want error, got nil")
		}
		if !strings.Contains(se.String(), "not a Smith repository") {
			t.Errorf("stderr missing error:\n%s", se.String())
		}
		if so.Len() != 0 {
			t.Errorf("stdout = %q, want empty on failure", so.String())
		}
	})
}
