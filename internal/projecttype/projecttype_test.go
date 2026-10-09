package projecttype

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check func(t *testing.T, pt *ProjectType)
	}{
		{
			name: "example project-type.json",
			path: "testdata/project-type.json",
			check: func(t *testing.T, pt *ProjectType) {
				t.Helper()
				if pt.Name != "python/astral/lib" {
					t.Errorf("Name = %q, want %q", pt.Name, "python/astral/lib")
				}
				if pt.Version != 1 {
					t.Errorf("Version = %d, want 1", pt.Version)
				}
				if pt.Description != "Python library on the Astral toolchain (uv, ruff, ty)" {
					t.Errorf("Description = %q", pt.Description)
				}
				wantCaps := []Capability{
					CapabilityBuildable, CapabilityTestable, CapabilityLintable,
					CapabilityFormattable, CapabilityPackageable,
				}
				if !slices.Equal(pt.Capabilities, wantCaps) {
					t.Errorf("Capabilities = %v, want %v", pt.Capabilities, wantCaps)
				}
				if !slices.Equal(pt.Tools, []string{"python", "uv"}) {
					t.Errorf("Tools = %v", pt.Tools)
				}
				if pt.WorkingDir != WorkingDirProject {
					t.Errorf("WorkingDir = %q, want %q", pt.WorkingDir, WorkingDirProject)
				}
				if !slices.Equal(pt.Targets["test"], []string{"uv run pytest -c '{{.config_path}}'"}) {
					t.Errorf("Targets[test] = %v", pt.Targets["test"])
				}
				if !slices.Equal(pt.DependsOn["build"], []string{"lint", "test"}) {
					t.Errorf("DependsOn[build] = %v", pt.DependsOn["build"])
				}
				param, ok := pt.Params["config_path"]
				if !ok || param.Default != "pyproject.toml" || param.Description != "ruff/pytest config location" {
					t.Errorf("Params[config_path] = %+v", pt.Params)
				}
				if got := pt.Environment["PYTHONUNBUFFERED"]; got != "1" {
					t.Errorf("Environment = %v", pt.Environment)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, err := Load(tt.path)
			if err != nil {
				t.Fatalf("Load(%q): %v", tt.path, err)
			}
			tt.check(t, pt)
		})
	}
}

func TestLoadRejectsInvalidDocs(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "unknown top-level key",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["go build ./..."]},"colour":"blue"}`,
			want: "colour",
		},
		{
			name: "invalid working_dir",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"working_dir":"wherever","targets":{"build":["go build ./..."]}}`,
			want: "working_dir",
		},
		{
			name: "invalid capability",
			json: `{"name":"go/std/lib","version":1,"description":"x","capabilities":["flyable"],"tools":["go"],"targets":{"build":["go build ./..."]}}`,
			want: "capabilities",
		},
		{
			name: "target with empty steps",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":[]}}`,
			want: "build",
		},
		{
			name: "bad type name",
			json: `{"name":"go//lib","version":1,"description":"x","tools":["go"],"targets":{"build":["go build ./..."]}}`,
			want: "name",
		},
		{
			name: "param missing required default",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["echo x"]},"params":{"config_path":{"description":"no default"}}}`,
			want: "default",
		},
		{
			name: "param with unknown inner key",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["echo x"]},"params":{"config_path":{"default":"x","unit":"parsec"}}}`,
			want: "params",
		},
		{
			name: "param value must be an object",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["echo x"]},"params":{"config_path":"pyproject.toml"}}`,
			want: "params",
		},
		{
			name: "param key must match the pattern",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["echo x"]},"params":{"ConfigPath":{"default":"x"}}}`,
			want: "params",
		},
		{
			name: "environment value must be a string",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["echo x"]},"environment":{"DEBUG":3}}`,
			want: "environment",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "project-type.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load(%q) succeeded, want error containing %q", path, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load(%q) error = %q, want substring %q", path, err, tt.want)
			}
		})
	}
}

// TestWorkingDirDefault verifies that a type omitting working_dir loads with
// the schema default, project.
func TestWorkingDirDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-type.json")
	doc := `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["go build ./..."]}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	pt, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if pt.WorkingDir != WorkingDirProject {
		t.Errorf("WorkingDir = %q, want default %q", pt.WorkingDir, WorkingDirProject)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	pt, err := Load("testdata/project-type.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	path := filepath.Join(t.TempDir(), "project-type.json")
	if err := Save(path, pt); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	if reloaded.Name != pt.Name || reloaded.Version != pt.Version ||
		len(reloaded.Targets) != len(pt.Targets) ||
		reloaded.Description != pt.Description {
		t.Errorf("round trip = %+v, want %+v", reloaded, pt)
	}
}

// TestLoadDryRendersTemplates covers the early validation: every step and
// environment value must render against the declared defaults, so a broken
// template fails at load (sync) time, not mid-run.
func TestLoadDryRendersTemplates(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name:    "undefined template key in a step",
			json:    `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"params":{"config_path":{"default":"pyproject.toml"}},"targets":{"build":["echo {{.config_pah}}"]}}`,
			wantErr: "config_pah",
		},
		{
			name:    "undefined template key in environment",
			json:    `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"environment":{"X":"{{.config_pah}}"},"targets":{"build":["echo ok"]}}`,
			wantErr: "config_pah",
		},
		{
			name: "template with a declared key loads",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"params":{"config_path":{"default":"pyproject.toml"}},"environment":{"X":"{{.config_path}}"},"targets":{"build":["echo {{.config_path}}"]}}`,
		},
		{
			name: "no templates at all loads",
			json: `{"name":"go/std/lib","version":1,"description":"x","tools":["go"],"targets":{"build":["go build ./..."]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "project-type.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "go/std/lib") {
				t.Errorf("Load error = %v, want it to name the type", err)
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	rendered, err := RenderTemplate(`ruff check --config '{{.config_path}}' src`, map[string]string{"config_path": "my conf.toml"})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	// The author's quoting is preserved: the value stays inside the quotes.
	want := `ruff check --config 'my conf.toml' src`
	if rendered != want {
		t.Errorf("RenderTemplate = %q, want %q", rendered, want)
	}

	if _, err := RenderTemplate(`{{.missing}}`, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "missing") {
		t.Errorf("RenderTemplate missing-key error = %v", err)
	}
}
