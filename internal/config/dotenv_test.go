package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dotenvRepoYML = `version: 1
name: mock
registries:
  - name: cliwright
    types: {git: https://example.test/types}
    templates: {git: https://example.test/templates}
workspace:
  project_roots: [libs]
dotenv: true
smith_env:
  default: dev
`

func TestLoadDotenvAndSmithEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.yml")
	if err := os.WriteFile(path, []byte(dotenvRepoYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Dotenv {
		t.Error("Dotenv = false, want true")
	}
	if cfg.SmithEnv.Default != "dev" {
		t.Errorf("SmithEnv.Default = %q, want dev", cfg.SmithEnv.Default)
	}
}

func TestLoadRejectsBadSmithEnvDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.yml")
	bad := strings.Replace(dotenvRepoYML, "default: dev", "default: DEV/x", 1)
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "smith_env") {
		t.Errorf("Load with bad smith_env.default = %v, want schema error naming smith_env", err)
	}
}
