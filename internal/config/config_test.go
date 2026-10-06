package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cliwright/smith/internal/validate"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check func(t *testing.T, cfg *RepoConfig)
	}{
		{
			name: "example repo.yml",
			path: "testdata/repo.yml",
			check: func(t *testing.T, cfg *RepoConfig) {
				t.Helper()
				if cfg.Version != 1 {
					t.Errorf("Version = %d, want 1", cfg.Version)
				}
				if cfg.Name != "name-of-monorepo" {
					t.Errorf("Name = %q, want %q", cfg.Name, "name-of-monorepo")
				}
				if len(cfg.Registries) != 2 {
					t.Fatalf("len(Registries) = %d, want 2", len(cfg.Registries))
				}
				first := cfg.Registries[0]
				if first.Name != "cliwright" {
					t.Errorf("Registries[0].Name = %q, want %q", first.Name, "cliwright")
				}
				if first.Types.Git != "https://github.com/cliwright/smith-project-types" {
					t.Errorf("Registries[0].Types.Git = %q", first.Types.Git)
				}
				if first.Templates.Git != "https://github.com/cliwright/smith-project-templates" {
					t.Errorf("Registries[0].Templates.Git = %q", first.Templates.Git)
				}
				if !slices.Equal(cfg.Tools, []string{"git", "uv", "docker"}) {
					t.Errorf("Tools = %v", cfg.Tools)
				}
				wantRoots := []string{"libs", "tools", "services", "images", "infra"}
				if !slices.Equal(cfg.Workspace.ProjectRoots, wantRoots) {
					t.Errorf("ProjectRoots = %v, want %v", cfg.Workspace.ProjectRoots, wantRoots)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(tt.path)
			if err != nil {
				t.Fatalf("Load(%q): %v", tt.path, err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadRejectsInvalidDocs(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string // substring the error must contain
	}{
		{
			name: "unknown top-level key",
			yaml: "version: 1\nname: demo\nregistries:\n  - name: cliwright\n    types: {git: https://example.com/types}\n    templates: {git: https://example.com/templates}\nworkspace:\n  project_roots: [libs]\nbogus_key: true\n",
			want: "bogus_key",
		},
		{
			name: "empty registries",
			yaml: "version: 1\nname: demo\nregistries: []\nworkspace:\n  project_roots: [libs]\n",
			want: "registries",
		},
		{
			name: "absolute project root",
			yaml: "version: 1\nname: demo\nregistries:\n  - name: cliwright\n    types: {git: https://example.com/types}\n    templates: {git: https://example.com/templates}\nworkspace:\n  project_roots: [/etc]\n",
			want: "project_roots",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "repo.yml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load(%q) succeeded, want error containing %q", path, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load(%q) error = %q, want substring %q", path, err, tt.want)
			}
			if !strings.Contains(err.Error(), "repo-config") {
				t.Errorf("Load(%q) error = %q, want it to name the schema", path, err)
			}
		})
	}
}

// TestSavePreservesComments is the critical round-trip test: load the
// comment-heavy example, modify one field, save, and require the
// modification, the comments, and schema validity to all survive.
func TestSavePreservesComments(t *testing.T) {
	cfg, err := Load("testdata/repo.yml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Tools = append(cfg.Tools, "ruff")

	saved := filepath.Join(t.TempDir(), "repo.yml")
	if err := Save(saved, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}

	// (a) the modification stuck
	reloaded, err := Load(saved)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	if !slices.Contains(reloaded.Tools, "ruff") {
		t.Errorf("reloaded Tools = %v, want it to contain %q", reloaded.Tools, "ruff")
	}

	// (b) the comments survived
	for _, comment := range []string{
		"# .smith/repo.yml — Smith repository configuration",
		"# Required. Identifies the monorepo (display, lock provenance).",
		"# Registry sources, in priority order: first match wins when a type or",
		"# Tools required to work in this repo, checked by `smith doctor` against PATH",
		"# Smith ONLY traverses these directories looking for smith.yml projects —",
	} {
		if !strings.Contains(string(data), comment) {
			t.Errorf("saved file lost comment %q", comment)
		}
	}

	// (c) the saved bytes still validate against the embedded schema
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("parsing saved file: %v", err)
	}
	var doc any
	if err := node.Decode(&doc); err != nil {
		t.Fatalf("decoding saved file: %v", err)
	}
	if err := validate.Validate(validate.RepoConfig, doc); err != nil {
		t.Errorf("saved file no longer validates: %v", err)
	}
}

// TestSaveFreshConfig round-trips a config built in code, which has no
// loaded comments to preserve.
func TestSaveFreshConfig(t *testing.T) {
	fresh := &RepoConfig{
		Version: 1,
		Name:    "demo",
		Registries: []Registry{
			{Name: "cliwright", Types: Source{Path: "../types"}, Templates: Source{Path: "../templates"}},
		},
		Workspace: Workspace{ProjectRoots: []string{"libs"}},
	}
	path := filepath.Join(t.TempDir(), "repo.yml")
	if err := Save(path, fresh); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if reloaded.Name != "demo" || reloaded.Registries[0].Types.Path != "../types" {
		t.Errorf("round trip = %+v", reloaded)
	}
}
