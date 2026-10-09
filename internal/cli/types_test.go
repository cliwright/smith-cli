package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/typeref"
)

// installTestType writes a type into the injected home's cache and returns
// its ref and bytes (for lock construction).
func installTestType(t *testing.T, home string, pt *projecttype.ProjectType) (typeref.TypeRef, []byte) {
	t.Helper()
	data, err := projecttype.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	ref := typeref.TypeRef{Name: pt.Name, Version: pt.Version}
	path := filepath.Join(home, cacheDirName, "project-types", installRelPath(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return ref, data
}

func libTypeV1() *projecttype.ProjectType {
	return &projecttype.ProjectType{
		Name:         "python/astral/lib",
		Version:      1,
		Description:  "Python library on the Astral toolchain (uv, ruff, ty)",
		Capabilities: []projecttype.Capability{projecttype.CapabilityBuildable, projecttype.CapabilityTestable},
		Tools:        []string{"python", "uv"},
		WorkingDir:   projecttype.WorkingDirProject,
		Params: map[string]projecttype.Param{
			"config_path": {Default: "pyproject.toml", Description: "ruff/pytest config location"},
		},
		Environment: map[string]string{"PYTHONUNBUFFERED": "1"},
		Targets: map[string][]string{
			"lint":  {"uv run ruff check --config '{{.config_path}}' ."},
			"test":  {"uv run pytest -c '{{.config_path}}'"},
			"build": {"uv build"},
		},
		DependsOn: map[string][]string{"build": {"lint", "test"}},
	}
}

func libTypeV2() *projecttype.ProjectType {
	pt := libTypeV1()
	pt.Version = 2
	pt.Description = "Python library, v2"
	return pt
}

func workspaceType() *projecttype.ProjectType {
	return &projecttype.ProjectType{
		Name:        "repo/uv/workspace",
		Version:     1,
		Description: "uv workspace root environment",
		Tools:       []string{"uv"},
		WorkingDir:  projecttype.WorkingDirProject,
		Targets:     map[string][]string{"setup": {"uv venv", "uv sync --extra dev"}, "clean": {"rm -rf .venv"}},
	}
}

// installStandardTypes installs lib@v1, lib@v2, workspace@v1 and returns
// refs + bytes per ref for lock construction.
func installStandardTypes(t *testing.T, home string) map[string][]byte {
	t.Helper()
	bytes := map[string][]byte{}
	for _, pt := range []*projecttype.ProjectType{libTypeV1(), libTypeV2(), workspaceType()} {
		ref, data := installTestType(t, home, pt)
		bytes[ref.String()] = data
	}
	return bytes
}

func TestTypesEmptyCache(t *testing.T) {
	setupHome(t)
	t.Chdir(t.TempDir())

	out, err := run(t, "types")
	if err != nil {
		t.Fatalf("types: %v", err)
	}
	if !strings.Contains(out, "no project types installed") || !strings.Contains(out, "smith sync") {
		t.Errorf("output = %q, want the empty-cache suggestion", out)
	}
}

func TestTypesListingOutsideRepo(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	t.Chdir(t.TempDir()) // outside any repo: no lock annotations

	out, err := run(t, "types")
	if err != nil {
		t.Fatalf("types: %v", err)
	}
	want := `python/astral/lib@1  Python library on the Astral toolchain (uv, ruff, ty)
python/astral/lib@2  Python library, v2
repo/uv/workspace@1  uv workspace root environment
`
	if out != want {
		t.Errorf("listing mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestTypesListingInRepoAnnotatesLockStatus(t *testing.T) {
	home := setupHome(t)
	installed := installStandardTypes(t, home)
	repo := buildMockRepo(t, []string{"libs"}, nil)
	t.Chdir(repo)

	// lib@1 pinned correctly; workspace@1 pinned with a drifted hash; lib@2
	// not pinned at all.
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"python/astral/lib@1": {Registry: "cliwright", Hash: hashBytes(installed["python/astral/lib@1"])},
		"repo/uv/workspace@1": {Registry: "cliwright", Hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
	}}
	if err := lock.Save(filepath.Join(repo, ".smith", "lock.json"), lk); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "types")
	if err != nil {
		t.Fatalf("types: %v", err)
	}
	want := `python/astral/lib@1  Python library on the Astral toolchain (uv, ruff, ty)  [pinned]
python/astral/lib@2  Python library, v2  [not pinned]
repo/uv/workspace@1  uv workspace root environment  [hash mismatch — run smith sync]
`
	if out != want {
		t.Errorf("listing mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestTypesNameListsVersions(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	t.Chdir(t.TempDir())

	out, err := run(t, "types", "python/astral/lib")
	if err != nil {
		t.Fatalf("types python/astral/lib: %v", err)
	}
	want := `1  Python library on the Astral toolchain (uv, ruff, ty)
2  Python library, v2
`
	if out != want {
		t.Errorf("versions mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestTypesUnknownNameListsInstalled(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	t.Chdir(t.TempDir())

	_, err := run(t, "types", "python/nope/lib")
	if err == nil || !strings.Contains(err.Error(), `type "python/nope/lib" is not installed`) ||
		!strings.Contains(err.Error(), "python/astral/lib, repo/uv/workspace") {
		t.Fatalf("error = %v, want unknown-name error listing installed names", err)
	}
}

func TestTypesUnknownVersionListsVersions(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	t.Chdir(t.TempDir())

	_, err := run(t, "types", "python/astral/lib@9")
	if err == nil || !strings.Contains(err.Error(), "type python/astral/lib@9 is not installed") ||
		!strings.Contains(err.Error(), "installed versions: 1, 2") {
		t.Fatalf("error = %v, want unknown-version error listing versions", err)
	}
}

func TestTypesDetailOutsideRepo(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	t.Chdir(t.TempDir())

	out, err := run(t, "types", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("types python/astral/lib@1: %v", err)
	}
	want := `python/astral/lib@1
  description: Python library on the Astral toolchain (uv, ruff, ty)
  capabilities: buildable, testable
  tools: python, uv
  working_dir: project
  params:
    config_path (default: pyproject.toml) — ruff/pytest config location
  environment:
    PYTHONUNBUFFERED=1
  targets:
    build:
      uv build
    lint:
      uv run ruff check --config '{{.config_path}}' .
    test:
      uv run pytest -c '{{.config_path}}'
  depends_on:
    build: lint, test
`
	if out != want {
		t.Errorf("detail mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestTypesDetailInRepoShowsStatus(t *testing.T) {
	home := setupHome(t)
	installed := installStandardTypes(t, home)
	repo := buildMockRepo(t, []string{"libs"}, nil)
	t.Chdir(repo)
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"repo/uv/workspace@1": {Registry: "cliwright", Hash: hashBytes(installed["repo/uv/workspace@1"])},
	}}
	if err := lock.Save(filepath.Join(repo, ".smith", "lock.json"), lk); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "types", "repo/uv/workspace@1")
	if err != nil {
		t.Fatalf("types repo/uv/workspace@1: %v", err)
	}
	if !strings.HasSuffix(out, "  status: pinned\n") {
		t.Errorf("detail missing status line:\n%s", out)
	}

	out, err = run(t, "types", "python/astral/lib")
	if err != nil {
		t.Fatalf("types python/astral/lib: %v", err)
	}
	if !strings.Contains(out, "1  Python library on the Astral toolchain (uv, ruff, ty)  [not pinned]") ||
		!strings.Contains(out, "2  Python library, v2  [not pinned]") {
		t.Errorf("versions missing not-pinned annotations:\n%s", out)
	}
}

func TestTypesCorruptFileWarnsAndContinues(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	bogus := filepath.Join(home, cacheDirName, "project-types", "mock", "std")
	if err := os.MkdirAll(bogus, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bogus, "broken@v1.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	out, err := run(t, "types")
	if err != nil {
		t.Fatalf("types: %v", err)
	}
	if !strings.Contains(out, "warning: cannot load ") || !strings.Contains(out, "broken@v1.json") {
		t.Errorf("output missing the corrupt-file warning:\n%s", out)
	}
	if !strings.Contains(out, "python/astral/lib@1") || !strings.Contains(out, "repo/uv/workspace@1") {
		t.Errorf("listing did not continue past the corrupt file:\n%s", out)
	}
}

func TestTypesBadNameArg(t *testing.T) {
	home := setupHome(t)
	installStandardTypes(t, home)
	t.Chdir(t.TempDir())

	_, err := run(t, "types", "foo")
	if err == nil || !strings.Contains(err.Error(), "want <language>/<flavor>/<kind>") {
		t.Fatalf("error = %v, want the grammar error", err)
	}
}
