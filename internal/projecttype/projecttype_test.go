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
				if !slices.Equal(pt.Targets["test"], []string{"uv run pytest"}) {
					t.Errorf("Targets[test] = %v", pt.Targets["test"])
				}
				if !slices.Equal(pt.DependsOn["build"], []string{"lint", "test"}) {
					t.Errorf("DependsOn[build] = %v", pt.DependsOn["build"])
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
