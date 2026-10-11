package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smith.yml")
	yml := "version: 1\nname: alpha\ntype: mock/std/lib@1\ndotenv: true\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !m.Dotenv {
		t.Error("Dotenv = false, want true")
	}
}

func TestLoadRejectsNonBooleanDotenv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smith.yml")
	yml := "version: 1\nname: alpha\ntype: mock/lib@1\ndotenv: yes please\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load with non-boolean dotenv: no error")
	}
}
