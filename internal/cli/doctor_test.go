package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
)

// makeFakeTools installs executable shell scripts into a temp bin dir and
// prepends it to PATH for the duration of the test.
func makeFakeTools(t *testing.T, scripts map[string]string) {
	t.Helper()
	bin := t.TempDir()
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// doctorFixture builds a repo whose tool union spans all three sources:
// repo tools (cookiecutter), project types (python/uv for alpha, smith-test-widget for
// beta), and an attachment (uv for repo:python).
func doctorFixture(t *testing.T) string {
	return doctorFixtureWithHome(t, setupHome(t))
}

func TestDoctorHealthy(t *testing.T) {
	repo := doctorFixture(t)
	makeFakeTools(t, map[string]string{
		"cookiecutter":      `echo "fake-cc 1.0"`,
		"smith-test-widget": `echo "widget 2.0"`,
		"python":            `echo "python 3.12"`,
		"uv":                `echo "uv 0.4"`,
	})
	t.Chdir(repo)

	out, err := run(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v\noutput:\n%s", err, out)
	}
	want := `cookiecutter  fake-cc 1.0  needed by: repo
python  python 3.12  needed by: alpha
smith-test-widget  widget 2.0  needed by: beta
uv  uv 0.4  needed by: alpha, repo:python
doctor: 4 ok, 0 missing
`
	if out != want {
		t.Errorf("doctor output mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestDoctorMissingToolIsUnhealthy(t *testing.T) {
	repo := doctorFixture(t)
	makeFakeTools(t, map[string]string{
		"cookiecutter": `echo "fake-cc 1.0"`,
		"python":       `echo "python 3.12"`,
		"uv":           `echo "uv 0.4"`,
		// smith-test-widget absent
	})
	t.Chdir(repo)

	out, err := run(t, "doctor")
	if !errors.Is(err, errUnhealthy) {
		t.Fatalf("doctor error = %v, want errUnhealthy\noutput:\n%s", err, out)
	}
	want := `cookiecutter  fake-cc 1.0  needed by: repo
python  python 3.12  needed by: alpha
uv  uv 0.4  needed by: alpha, repo:python
missing:
smith-test-widget  needed by: beta
doctor: 3 ok, 1 missing
`
	if out != want {
		t.Errorf("doctor output mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestDoctorVersionProbeFallsBackToUnknown(t *testing.T) {
	repo := doctorFixture(t)
	makeFakeTools(t, map[string]string{
		"cookiecutter":      `echo "fake-cc 1.0"`,
		"smith-test-widget": `echo "widget 2.0"`,
		"python":            `exit 1`,  // probe fails → version unknown
		"uv":                `echo ""`, // nothing on stdout → version unknown
	})
	t.Chdir(repo)

	out, err := run(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, want := range []string{
		"python  version unknown  needed by: alpha",
		"uv  version unknown  needed by: alpha, repo:python",
		"doctor: 4 ok, 0 missing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorUnloadableTypeIsAFinding(t *testing.T) {
	home := setupHome(t)
	repo := doctorFixtureWithHome(t, home)
	makeFakeTools(t, map[string]string{
		"cookiecutter":      `echo "fake-cc 1.0"`,
		"smith-test-widget": `echo "widget 2.0"`,
		"python":            `echo "python 3.12"`,
		"uv":                `echo "uv 0.4"`,
	})
	t.Chdir(repo)

	// Tamper with alpha's installed type: custody check fails, but the
	// doctor keeps checking the rest.
	installPath := filepath.Join(home, cacheDirName, "project-types", filepath.FromSlash("mock/std/lib@v1.json"))
	if err := os.WriteFile(installPath, []byte(`{"name":"mock/lib"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "doctor")
	if !errors.Is(err, errUnhealthy) {
		t.Fatalf("doctor error = %v, want errUnhealthy\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "warnings:") ||
		!strings.Contains(out, `cannot check tools for alpha (type mock/std/lib@1): run smith sync`) {
		t.Errorf("output missing the unloadable-type finding:\n%s", out)
	}
	// The rest was still checked.
	if !strings.Contains(out, "smith-test-widget  widget 2.0  needed by: beta") ||
		!strings.Contains(out, "doctor: 3 ok, 0 missing, 1 type(s) could not be checked") {
		t.Errorf("output missing continued checks or summary:\n%s", out)
	}
}

// doctorFixtureWithHome is doctorFixture with an injected home so tampering
// tests can reach the installed types.
func doctorFixtureWithHome(t *testing.T, home string) string {
	repo := t.TempDir()
	writeDispatchRepo(t, home, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		"libs/beta":  {name: "beta", typ: "mock/std/other@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {
			targets: map[string][]string{"build": {"echo build"}},
			tools:   []string{"python", "uv"},
		},
		"mock/std/other@1": {
			targets: map[string][]string{"build": {"echo build"}},
			tools:   []string{"smith-test-widget"},
		},
		"repo/uv/workspace@1": {
			targets: map[string][]string{"setup": {"echo setup"}},
			tools:   []string{"uv"},
		},
	}, map[string]config.RepoTarget{
		"python": {Type: "repo/uv/workspace@1"},
	})
	repoYMLPath := filepath.Join(repo, ".smith", "repo.yml")
	data, err := os.ReadFile(repoYMLPath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("tools:\n  - cookiecutter\n")...)
	if err := os.WriteFile(repoYMLPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}
