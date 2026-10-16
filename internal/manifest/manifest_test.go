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
				if got := m.Types.String(); got != "python/astral/service@2" {
					t.Errorf("Types = %q, want %q", got, "python/astral/service@2")
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
				if got := m.Params["config_path"]; got != "ci/pyproject.toml" {
					t.Errorf("params[config_path] = %q", got)
				}
				if got := m.Environment["PYTHONWARNDEFAULTENCODING"]; got != "1" {
					t.Errorf("environment = %v", m.Environment)
				}
			},
		},
		{
			name: "dotted target names in explicit depends_on",
			path: "testdata/smith-dotted.yml",
			check: func(t *testing.T, m *Manifest) {
				t.Helper()
				if len(m.DependsOn) != 1 {
					t.Fatalf("len(DependsOn) = %d, want 1", len(m.DependsOn))
				}
				dep := m.DependsOn[0]
				if dep.Project != "auth" ||
					!slices.Equal(dep.Targets, []string{"env.get"}) ||
					!slices.Equal(dep.Before, []string{"env.put"}) {
					t.Errorf("dep = %+v, want {auth [env.get] [env.put]}", dep)
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
		{
			name: "param value must be a string",
			yaml: valid + "params:\n  config_path: [pyproject.toml]\n",
			want: "params",
		},
		{
			name: "environment value must be a string",
			yaml: valid + "environment:\n  DEBUG: 3\n",
			want: "environment",
		},
		{
			name: "working_dir outside the two legal values",
			yaml: valid + "working_dir: wherever\n",
			want: "working_dir",
		},
		{
			name: "type array with a bad entry",
			yaml: "version: 1\nname: demo\ntype: [python/astral/lib@1, mock/lib]\n",
			want: "mock/lib",
		},
		{
			name: "type array entry must be a string",
			yaml: "version: 1\nname: demo\ntype: [3]\n",
			want: "type",
		},
		{
			name: "type array must not be empty",
			yaml: "version: 1\nname: demo\ntype: []\n",
			want: "type",
		},
		{
			name: "type array must not repeat a ref",
			yaml: "version: 1\nname: demo\ntype: [python/astral/lib@1, python/astral/lib@1]\n",
			want: "type",
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

// TestWorkingDirRoundTrip covers the manifest-level working_dir override:
// it loads, and Save preserves it.
func TestWorkingDirRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smith.yml")
	doc := "version: 1\nname: demo\ntype: python/astral/lib@1\nworking_dir: repo\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.WorkingDir != "repo" {
		t.Errorf("WorkingDir = %q, want repo", m.WorkingDir)
	}
	saved := filepath.Join(t.TempDir(), "smith.yml")
	if err := Save(saved, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(saved)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	if reloaded.WorkingDir != "repo" {
		t.Errorf("reloaded WorkingDir = %q, want repo", reloaded.WorkingDir)
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

// TestLoadTypeArray covers the multi-type form: an array of refs loads in
// declaration order, and the sugar string form still loads.
func TestLoadTypeArray(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string // Types.String()
	}{
		{
			name: "sugar string form",
			yaml: "version: 1\nname: demo\ntype: python/astral/lib@1\n",
			want: "python/astral/lib@1",
		},
		{
			name: "array form",
			yaml: "version: 1\nname: demo\ntype: [python/astral/lib@1, tofu/std/module@2]\n",
			want: "python/astral/lib@1, tofu/std/module@2",
		},
		{
			name: "one-element array",
			yaml: "version: 1\nname: demo\ntype:\n  - python/astral/lib@1\n",
			want: "python/astral/lib@1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "smith.yml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			m, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := m.Types.String(); got != tt.want {
				t.Errorf("Types = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSaveTypeArrayRoundTrip: a loaded multi-type manifest saves back as a
// sequence, keeps its comments, and reloads identically. A one-element array
// is sugar: it saves as the bare scalar form (intended normalization).
func TestSaveTypeArrayRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantType string // substring the saved type field must render as
	}{
		{
			name: "multi-element array stays a sequence",
			yaml: "# head comment\nversion: 1\nname: demo\n# type comment\ntype: [python/astral/lib@1, tofu/std/module@2]\n",
			// The saved form is a block sequence; the exact layout is yaml.v3's.
			wantType: "- python/astral/lib@1",
		},
		{
			name:     "one-element array normalizes to the scalar sugar form",
			yaml:     "version: 1\nname: demo\n# type comment\ntype:\n  - python/astral/lib@1\n",
			wantType: "type: python/astral/lib@1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "smith.yml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			m, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			saved := filepath.Join(t.TempDir(), "smith.yml")
			if err := Save(saved, m); err != nil {
				t.Fatalf("Save: %v", err)
			}
			data, err := os.ReadFile(saved)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tt.wantType) {
				t.Errorf("saved file missing %q:\n%s", tt.wantType, data)
			}
			if !strings.Contains(string(data), "# type comment") {
				t.Errorf("saved file lost the type comment:\n%s", data)
			}
			reloaded, err := Load(saved)
			if err != nil {
				t.Fatalf("reloading saved file: %v", err)
			}
			if got, want := reloaded.Types.String(), m.Types.String(); got != want {
				t.Errorf("reloaded Types = %q, want %q", got, want)
			}
		})
	}
}
