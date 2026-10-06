package manifest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check func(t *testing.T, m *Manifest)
	}{
		{
			name: "example smith.yml",
			path: "testdata/smith.yml",
			check: func(t *testing.T, m *Manifest) {
				t.Helper()
				if m.Version != 1 {
					t.Errorf("Version = %d, want 1", m.Version)
				}
				if m.Name != "payments" {
					t.Errorf("Name = %q, want %q", m.Name, "payments")
				}
				if m.Type != "python/astral/service@2" {
					t.Errorf("Type = %q, want %q", m.Type, "python/astral/service@2")
				}
				if len(m.DependsOn) != 3 {
					t.Fatalf("len(DependsOn) = %d, want 3", len(m.DependsOn))
				}
				sugar := m.DependsOn[0]
				if sugar.Project != "auth" ||
					!slices.Equal(sugar.Targets, []string{"build"}) ||
					!slices.Equal(sugar.Before, []string{"build"}) {
					t.Errorf("sugar dep = %+v, want {auth [build] [build]}", sugar)
				}
				explicit := m.DependsOn[1]
				if explicit.Project != "database" ||
					!slices.Equal(explicit.Targets, []string{"test", "lint", "build"}) ||
					!slices.Equal(explicit.Before, []string{"build"}) {
					t.Errorf("explicit dep = %+v", explicit)
				}
				override := m.DependsOn[2]
				if override.Project != "auth" ||
					!slices.Equal(override.Targets, []string{"run"}) ||
					!slices.Equal(override.Before, []string{"test"}) {
					t.Errorf("override dep = %+v", override)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Load(tt.path)
			if err != nil {
				t.Fatalf("Load(%q): %v", tt.path, err)
			}
			tt.check(t, m)
		})
	}
}

func TestLoadRejectsInvalidDocs(t *testing.T) {
	valid := "version: 1\nname: demo\ntype: python/astral/lib@1\n"
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown top-level key",
			yaml: valid + "colour: blue\n",
			want: "colour",
		},
		{
			name: "explicit dep with unknown key",
			yaml: valid + "depends_on:\n  - project: auth\n    targets: [build]\n    bogus: x\n",
			want: "bogus",
		},
		{
			name: "bad project name in sugar form",
			yaml: valid + "depends_on:\n  - Auth\n",
			want: "depends_on",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "smith.yml")
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
		})
	}
}

// TestSavePreservesCommentsAndSugarForm loads the comment-heavy example,
// saves it untouched, and requires comments to survive and the sugar-form
// dependency to still be a bare scalar (not an explicit mapping).
func TestSavePreservesCommentsAndSugarForm(t *testing.T) {
	m, err := Load("testdata/smith.yml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	saved := filepath.Join(t.TempDir(), "smith.yml")
	if err := Save(saved, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}

	// The typed content round-trips.
	reloaded, err := Load(saved)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	if reloaded.Name != "payments" || len(reloaded.DependsOn) != 3 {
		t.Errorf("round trip = %+v", reloaded)
	}

	// The comments survived.
	for _, comment := range []string{
		"# smith.yml — Smith project manifest. One per project directory; declares",
		"# Required. Project type from the registry, pinned to a version.",
		"# sugar: auth:build before this project's build",
	} {
		if !strings.Contains(string(data), comment) {
			t.Errorf("saved file lost comment %q", comment)
		}
	}

	// The sugar dependency is still a bare scalar, not an explicit mapping.
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("parsing saved file: %v", err)
	}
	dependsOn := mustFindEntry(t, node.Content[0], "depends_on")
	if dependsOn.Kind != yaml.SequenceNode || len(dependsOn.Content) != 3 {
		t.Fatalf("depends_on = kind %v, %d items", dependsOn.Kind, len(dependsOn.Content))
	}
	first := dependsOn.Content[0]
	if first.Kind != yaml.ScalarNode || first.Value != "auth" {
		t.Errorf("first dependency = kind %v value %q, want bare scalar %q", first.Kind, first.Value, "auth")
	}
	third := dependsOn.Content[2]
	if third.Kind != yaml.MappingNode {
		t.Errorf("third dependency = kind %v, want mapping", third.Kind)
	}
}

// TestSaveModifiedManifest appends a dependency and requires it in the
// output, with the loaded comments still intact around it.
func TestSaveModifiedManifest(t *testing.T) {
	m, err := Load("testdata/smith.yml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m.DependsOn = append(m.DependsOn, Dependency{Project: "observability"})

	saved := filepath.Join(t.TempDir(), "smith.yml")
	if err := Save(saved, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(saved)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	if len(reloaded.DependsOn) != 4 || reloaded.DependsOn[3].Project != "observability" {
		t.Errorf("reloaded DependsOn = %+v", reloaded.DependsOn)
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- observability") {
		t.Errorf("saved file missing new dependency:\n%s", data)
	}
}

func mustFindEntry(t *testing.T, n *yaml.Node, key string) *yaml.Node {
	t.Helper()
	k, v := lookupEntry(n, key)
	if k == nil {
		t.Fatalf("key %q not found", key)
	}
	return v
}

func lookupEntry(n *yaml.Node, key string) (k, v *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}
