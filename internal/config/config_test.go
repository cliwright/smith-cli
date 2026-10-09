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
		{
			name: "repo_targets type must be repo/*",
			yaml: "version: 1\nname: demo\nregistries:\n  - name: cliwright\n    types: {git: https://example.com/types}\n    templates: {git: https://example.com/templates}\nworkspace:\n  project_roots: [libs]\nrepo_targets:\n  python:\n    type: python/astral/lib@1\n",
			want: "repo_targets",
		},
		{
			name: "repo_targets entry is closed",
			yaml: "version: 1\nname: demo\nregistries:\n  - name: cliwright\n    types: {git: https://example.com/types}\n    templates: {git: https://example.com/templates}\nworkspace:\n  project_roots: [libs]\nrepo_targets:\n  python:\n    type: repo/uv/workspace@1\n    commands: [make setup]\n",
			want: "repo_targets",
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

// TestLoadRepoTargets covers the attachment config: aliases bound to
// repo/<flavor>/<kind>@<version> types, with optional params/environment.
func TestLoadRepoTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.yml")
	yaml := "version: 1\nname: demo\n" +
		"registries:\n" +
		"  - name: cliwright\n" +
		"    types: {git: https://example.com/types}\n" +
		"    templates: {git: https://example.com/templates}\n" +
		"workspace:\n  project_roots: [libs]\n" +
		"repo_targets:\n" +
		"  python:\n" +
		"    type: repo/uv/workspace@1\n" +
		"    params:\n" +
		"      extras: dev\n" +
		"    environment:\n" +
		"      UV_FROZEN: \"1\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	rt, ok := cfg.RepoTargets["python"]
	if !ok {
		t.Fatalf("RepoTargets missing %q: %+v", "python", cfg.RepoTargets)
	}
	if rt.Type != "repo/uv/workspace@1" {
		t.Errorf("Type = %q", rt.Type)
	}
	if rt.Params["extras"] != "dev" || rt.Environment["UV_FROZEN"] != "1" {
		t.Errorf("attachment params/env = %+v / %+v", rt.Params, rt.Environment)
	}
}

// TestValidateRepoTargets exercises the loader backstop directly: even a doc
// that somehow passed the schema with a non-repo/* attachment type is
// rejected at load with the loud message.
func TestValidateRepoTargets(t *testing.T) {
	cfg := &RepoConfig{RepoTargets: map[string]RepoTarget{
		"python": {Type: "python/astral/lib@1"},
	}}
	err := validateRepoTargets(cfg)
	if err == nil || !strings.Contains(err.Error(), "repo_targets entries must use repo/<flavor>/<kind>@<version> types, got \"python/astral/lib@1\"") {
		t.Fatalf("validateRepoTargets = %v, want the loud constraint error", err)
	}
	if err := validateRepoTargets(&RepoConfig{RepoTargets: map[string]RepoTarget{
		"python": {Type: "repo/uv/workspace@1"},
	}}); err != nil {
		t.Fatalf("validateRepoTargets = %v, want nil for a repo/* type", err)
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
