package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
)

// initRepo writes a valid .smith/repo.yml with the given project roots into
// root and returns the loaded config.
func initRepo(t *testing.T, root string, roots ...string) *config.RepoConfig {
	t.Helper()
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
	smithDir := filepath.Join(root, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(smithDir, "repo.yml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// writeManifest writes a minimal valid smith.yml declaring the project in dir.
func writeManifest(t *testing.T, dir, name, typ string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("version: 1\nname: %s\ntype: %s\n", name, typ)
	if err := os.WriteFile(filepath.Join(dir, "smith.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeManifestWithDeps writes a minimal valid smith.yml with the given
// sugar-form dependencies.
func writeManifestWithDeps(t *testing.T, dir, name, typ string, deps ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var dependsOn strings.Builder
	if len(deps) > 0 {
		dependsOn.WriteString("depends_on:\n")
		for _, dep := range deps {
			fmt.Fprintf(&dependsOn, "  - %s\n", dep)
		}
	}
	content := fmt.Sprintf("version: 1\nname: %s\ntype: %s\n%s", name, typ, dependsOn.String())
	if err := os.WriteFile(filepath.Join(dir, "smith.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projectDirs(projects []Project) []string {
	dirs := make([]string, len(projects))
	for i, p := range projects {
		dirs[i] = p.Dir
	}
	return dirs
}

func TestFindRepoRoot(t *testing.T) {
	t.Run("start dir is the repo root", func(t *testing.T) {
		repo := t.TempDir()
		initRepo(t, repo, "libs")
		got, err := FindRepoRoot(repo)
		if err != nil {
			t.Fatalf("FindRepoRoot(%q): %v", repo, err)
		}
		if got != repo {
			t.Errorf("FindRepoRoot(%q) = %q, want %q", repo, got, repo)
		}
	})

	t.Run("nested subdirectory finds ancestor", func(t *testing.T) {
		repo := t.TempDir()
		initRepo(t, repo, "libs")
		nested := filepath.Join(repo, "libs", "a-lib", "src", "deep")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := FindRepoRoot(nested)
		if err != nil {
			t.Fatalf("FindRepoRoot(%q): %v", nested, err)
		}
		if got != repo {
			t.Errorf("FindRepoRoot(%q) = %q, want %q", nested, got, repo)
		}
	})

	t.Run("no repository anywhere", func(t *testing.T) {
		orphan := t.TempDir()
		_, err := FindRepoRoot(orphan)
		if err == nil {
			t.Fatalf("FindRepoRoot(%q) = nil, want error", orphan)
		}
		if err.Error() != notARepoMessage {
			t.Errorf("error = %q, want %q", err, notARepoMessage)
		}
	})
}

func TestFindProjects(t *testing.T) {
	tests := []struct {
		name    string
		roots   []string
		setup   func(t *testing.T, repo string)
		want    []string
		wantErr string
	}{
		{
			name:  "multiple roots with empty and missing roots",
			roots: []string{"libs", "services", "tools", "images", "nonexistent"},
			setup: func(t *testing.T, repo string) {
				writeManifest(t, filepath.Join(repo, "libs", "z-lib"), "z-lib", "python/astral/lib@1")
				writeManifest(t, filepath.Join(repo, "libs", "a-lib"), "a-lib", "python/astral/lib@1")
				writeManifest(t, filepath.Join(repo, "services", "svc"), "svc", "python/astral/service@2")
				writeManifest(t, filepath.Join(repo, "tools", "cli"), "cli", "go/std/tool@1")
				// "images" exists but has no projects; "nonexistent" is absent.
				if err := os.MkdirAll(filepath.Join(repo, "images"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"libs/a-lib", "libs/z-lib", "services/svc", "tools/cli"},
		},
		{
			name:  "nested service layout",
			roots: []string{"services"},
			setup: func(t *testing.T, repo string) {
				writeManifest(t, filepath.Join(repo, "services", "payments", "app"), "payments", "python/astral/service@2")
				writeManifest(t, filepath.Join(repo, "services", "payments", "infra"), "payments-infra", "tofu/std/module@1")
			},
			want: []string{"services/payments/app", "services/payments/infra"},
		},
		{
			name:  "project directly at a root",
			roots: []string{"libs"},
			setup: func(t *testing.T, repo string) {
				writeManifest(t, filepath.Join(repo, "libs"), "libs-meta", "python/astral/lib@1")
			},
			want: []string{"libs"},
		},
		{
			name:  "skip list is not descended into",
			roots: []string{"libs"},
			setup: func(t *testing.T, repo string) {
				writeManifest(t, filepath.Join(repo, "libs", "foo"), "foo", "python/astral/lib@1")
				for _, skipped := range []string{
					filepath.Join("libs", "foo", "node_modules", "x"),
					filepath.Join("libs", "foo", ".venv", "x"),
					filepath.Join("libs", "foo", "venv", "x"),
					filepath.Join("libs", "foo", "__pycache__", "x"),
					filepath.Join("libs", "foo", ".tox", "x"),
					filepath.Join("libs", "foo", ".mypy_cache", "x"),
					filepath.Join("libs", "foo", ".pytest_cache", "x"),
					filepath.Join("libs", "foo", ".ruff_cache", "x"),
					filepath.Join("libs", "foo", "dist", "x"),
					filepath.Join("libs", "foo", "target", "x"),
					filepath.Join("libs", ".git", "x"),
				} {
					writeManifest(t, filepath.Join(repo, skipped), "planted", "python/astral/lib@1")
				}
			},
			want: []string{"libs/foo"},
		},
		{
			name:  "duplicate project names fail naming both paths",
			roots: []string{"libs", "services"},
			setup: func(t *testing.T, repo string) {
				writeManifest(t, filepath.Join(repo, "libs", "dup"), "dup", "python/astral/lib@1")
				writeManifest(t, filepath.Join(repo, "services", "dup"), "dup", "python/astral/lib@1")
			},
			wantErr: `duplicate project name "dup"`,
		},
		{
			name:  "broken manifest fails with its path",
			roots: []string{"libs"},
			setup: func(t *testing.T, repo string) {
				dir := filepath.Join(repo, "libs", "broken")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				content := "version: 1\nname: broken\ntype: python/astral/lib@1\nbogus_key: true\n"
				if err := os.WriteFile(filepath.Join(dir, "smith.yml"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "libs/broken/smith.yml",
		},
		{
			name:  "root with no projects yields nothing",
			roots: []string{"libs"},
			setup: func(t *testing.T, repo string) {
				if err := os.MkdirAll(filepath.Join(repo, "libs", "empty-dir"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{},
		},
		{
			name:  "dangling dependency names project and missing dependency",
			roots: []string{"libs"},
			setup: func(t *testing.T, repo string) {
				writeManifestWithDeps(t, filepath.Join(repo, "libs", "spam"), "spam", "python/astral/service@2", "auth")
			},
			wantErr: `libs/spam/smith.yml: dependency "auth" does not match any project`,
		},
		{
			name:  "self dependency is rejected",
			roots: []string{"libs"},
			setup: func(t *testing.T, repo string) {
				writeManifestWithDeps(t, filepath.Join(repo, "libs", "spam"), "spam", "python/astral/service@2", "spam")
			},
			wantErr: `libs/spam/smith.yml: project "spam" must not depend on itself`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			cfg := initRepo(t, repo, tt.roots...)
			tt.setup(t, repo)

			projects, err := FindProjects(repo, cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("FindProjects = %v, want error containing %q", projectDirs(projects), tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err, tt.wantErr)
				}
				if tt.name == "duplicate project names fail naming both paths" &&
					(!strings.Contains(err.Error(), "libs/dup") || !strings.Contains(err.Error(), "services/dup")) {
					t.Errorf("error = %q, want it to name both paths", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindProjects: %v", err)
			}
			if got := projectDirs(projects); !slices.Equal(got, tt.want) {
				t.Errorf("project dirs = %v, want %v", got, tt.want)
			}
			for _, p := range projects {
				if p.Manifest == nil {
					t.Errorf("project %s has nil manifest", p.Dir)
				}
			}
		})
	}
}
