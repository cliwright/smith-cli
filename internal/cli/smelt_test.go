package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/ingot"
)

// writeRegistryTypeDir creates a registry-layout type directory
// <root>/<lang>/<flavor>/<kind>/v<N> with the given files and returns the
// version directory.
func writeRegistryTypeDir(t *testing.T, root, name string, version int, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(fmt.Sprintf("%s/v%d", name, version)))
	for fileName, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(fileName))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const smeltTypeContent = `{"name":"python/astral/lib","version":1,"description":"Python library on the Astral toolchain","tools":["python","uv"],"targets":{"build":["uv build"]}}`

func TestSmeltWritesIngotAndSidecar(t *testing.T) {
	root := t.TempDir()
	dir := writeRegistryTypeDir(t, root, "python/astral/lib", 1, map[string]string{
		"project-type.json": smeltTypeContent,
		"scripts/build.sh":  "#!/bin/sh\necho build\n",
		"help/project.md":   "# project\n",
	})
	out, err := run(t, "smelt", "--path", dir)
	if err != nil {
		t.Fatalf("smelt: %v\noutput:\n%s", err, out)
	}

	artifact := filepath.Join(dir, "python-astral-lib@v1.ingot")
	ingotBytes, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("ingot missing: %v", err)
	}
	names, err := ingot.List(ingotBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "help/project.md,project-type.json,scripts/build.sh" {
		t.Errorf("ingot entries = %v", names)
	}

	sidecar, err := os.ReadFile(artifact + ".sha256")
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	want := fmt.Sprintf("%s  python-astral-lib@v1.ingot\n", strings.TrimPrefix(ingot.Hash(ingotBytes), "sha256:"))
	if string(sidecar) != want {
		t.Errorf("sidecar = %q, want %q", sidecar, want)
	}

	// Packing again is byte-identical (reproducibility) — the first pack's
	// own artifact is excluded from the second.
	if _, err := run(t, "smelt", "--path", dir); err != nil {
		t.Fatalf("second smelt: %v", err)
	}
	again, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(ingotBytes) {
		t.Error("re-packing the same directory produced a different ingot")
	}
}

func TestSmeltEnforcesRegistryPosition(t *testing.T) {
	root := t.TempDir()

	// Version directory does not match the file's version.
	badVersion := writeRegistryTypeDir(t, root, "python/astral/lib", 2, map[string]string{
		"project-type.json": smeltTypeContent, // declares version 1
	})
	_, err := run(t, "smelt", "--path", badVersion)
	if err == nil || !strings.Contains(err.Error(), `version directory is "v2"`) {
		t.Fatalf("error = %v, want version-mismatch error", err)
	}

	// Path name does not match the file's name.
	badName := writeRegistryTypeDir(t, root, "python/other/lib", 1, map[string]string{
		"project-type.json": smeltTypeContent, // declares python/astral/lib
	})
	_, err = run(t, "smelt", "--path", badName)
	if err == nil || !strings.Contains(err.Error(), `path implies type "python/other/lib"`) {
		t.Fatalf("error = %v, want name-mismatch error", err)
	}
}

func TestSmeltRefusesNonTypeDir(t *testing.T) {
	_, err := run(t, "smelt", "--path", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "project-type.json") {
		t.Fatalf("error = %v, want missing-type error", err)
	}
}

func TestSmeltRequiresPathFlag(t *testing.T) {
	_, err := run(t, "smelt")
	if err == nil || !strings.Contains(err.Error(), "--path is required") {
		t.Fatalf("error = %v, want the --path guard", err)
	}
}
