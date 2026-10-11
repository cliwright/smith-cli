package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeServiceFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validService = `version: 1
name: auth
components:
  - project: auth-core
  - project: auth-tofu
    targets: {apply: deploy, destroy: destroy}
`

func TestLoadValid(t *testing.T) {
	path := writeServiceFile(t, t.TempDir(), "auth.yml", validService)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "auth" || s.Version != 1 {
		t.Errorf("name/version = %q/%d", s.Name, s.Version)
	}
	if len(s.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(s.Components))
	}
	if s.Components[0].Project != "auth-core" || len(s.Components[0].Targets) != 0 {
		t.Errorf("component 0 = %+v", s.Components[0])
	}
	if s.Components[1].Project != "auth-tofu" {
		t.Errorf("component 1 = %+v", s.Components[1])
	}
	if s.Components[1].Targets["apply"] != "deploy" || s.Components[1].Targets["destroy"] != "destroy" {
		t.Errorf("mapping = %v", s.Components[1].Targets)
	}
}

func TestLoadRejectsInvalidDocuments(t *testing.T) {
	cases := map[string]string{
		"unknown top-level key":           "version: 1\nname: auth\ncomponents:\n  - project: a\nbogus: true\n",
		"name pattern":                    "version: 1\nname: Auth_Service\ncomponents:\n  - project: a\n",
		"empty components":                "version: 1\nname: auth\ncomponents: []\n",
		"mapping value not in vocabulary": "version: 1\nname: auth\ncomponents:\n  - project: a\n    targets: {apply: frisbee}\n",
		"component extra key":             "version: 1\nname: auth\ncomponents:\n  - project: a\n    when: always\n",
		"version two":                     "version: 2\nname: auth\ncomponents:\n  - project: a\n",
	}
	for label, doc := range cases {
		path := writeServiceFile(t, t.TempDir(), "svc.yml", doc)
		if _, err := Load(path); err == nil {
			t.Errorf("%s: want schema error, got nil", label)
		} else if !strings.Contains(err.Error(), "schema validation failed") {
			t.Errorf("%s: error = %v, want schema validation failure", label, err)
		}
	}
}

func TestStandardTargetsCoversSchemaEnum(t *testing.T) {
	// The vocabulary lives in two places (schema enum + this list); drift
	// shows up as dispatch accepting targets the schema would refuse.
	want := map[string]bool{"build": true, "test": true, "lint": true, "push": true, "plan": true, "deploy": true, "destroy": true}
	if len(StandardTargets) != len(want) {
		t.Fatalf("StandardTargets = %v", StandardTargets)
	}
	for _, st := range StandardTargets {
		if !want[st] {
			t.Errorf("StandardTargets has %q, not in schema enum", st)
		}
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	writeServiceFile(t, dir, "b-second.yml", "version: 1\nname: beta\ncomponents:\n  - project: a\n")
	writeServiceFile(t, dir, "a-first.yml", validService)
	writeServiceFile(t, dir, "notes.md", "not a service\n")
	writeServiceFile(t, dir, "smith.yml", "version: 1\nname: not-a-service\n")
	writeServiceFile(t, filepath.Join(dir, "nested"), "inner.yml", "version: 1\nname: nested\ncomponents:\n  - project: a\n")

	svcs, err := LoadDir(dir, map[string]bool{"auth-core": true, "auth-tofu": true, "a": true})
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(svcs) != 2 {
		t.Fatalf("services = %d, want 2 (yml only, top level, not smith.yml)", len(svcs))
	}
	if svcs[0].Name != "auth" || svcs[1].Name != "beta" {
		t.Errorf("order = %s, %s; want sorted auth, beta", svcs[0].Name, svcs[1].Name)
	}
}

func TestLoadDirMissingDirMeansNoServices(t *testing.T) {
	svcs, err := LoadDir(filepath.Join(t.TempDir(), "nope"), map[string]bool{})
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if svcs != nil {
		t.Errorf("services = %v, want nil", svcs)
	}
}

func TestLoadDirDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	doc := "version: 1\nname: auth\ncomponents:\n  - project: a\n"
	writeServiceFile(t, dir, "one.yml", doc)
	writeServiceFile(t, dir, "two.yml", doc)
	_, err := LoadDir(dir, map[string]bool{"a": true})
	if err == nil || !strings.Contains(err.Error(), `duplicate service name "auth"`) {
		t.Fatalf("error = %v, want duplicate service name", err)
	}
}

func TestLoadDirComponentProblems(t *testing.T) {
	cases := map[string]string{
		"unknown component":   "version: 1\nname: auth\ncomponents:\n  - project: ghost\n",
		"duplicate component": "version: 1\nname: auth\ncomponents:\n  - project: a\n  - project: a\n",
	}
	for label, doc := range cases {
		dir := t.TempDir()
		writeServiceFile(t, dir, "svc.yml", doc)
		_, err := LoadDir(dir, map[string]bool{"a": true})
		if err == nil {
			t.Errorf("%s: want error, got nil", label)
		}
	}
}
