package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/projecttype"

	"github.com/spf13/cobra"
)

func envCmd(t *testing.T, flagVal string, flagSet bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("env", "", "")
	if flagSet {
		if err := cmd.Flags().Set("env", flagVal); err != nil {
			t.Fatalf("Flags().Set: %v", err)
		}
	}
	return cmd
}

func TestResolveEnvPrecedence(t *testing.T) {
	rc := &repoContext{cfg: &config.RepoConfig{SmithEnv: config.SmithEnv{Default: "from-default"}}}

	t.Setenv("SMITH_ENV", "")
	if got, err := rc.resolveEnv(envCmd(t, "", false)); err != nil || got != "from-default" {
		t.Errorf("repo default: got %q, err %v", got, err)
	}

	t.Setenv("SMITH_ENV", "from-os")
	if got, err := rc.resolveEnv(envCmd(t, "", false)); err != nil || got != "from-os" {
		t.Errorf("$SMITH_ENV: got %q, err %v", got, err)
	}
	if got, err := rc.resolveEnv(envCmd(t, "from-flag", true)); err != nil || got != "from-flag" {
		t.Errorf("--env beats $SMITH_ENV: got %q, err %v", got, err)
	}

	rcNoDefault := &repoContext{cfg: &config.RepoConfig{}}
	t.Setenv("SMITH_ENV", "")
	if got, err := rcNoDefault.resolveEnv(envCmd(t, "", false)); err != nil || got != "" {
		t.Errorf("nothing set: got %q, err %v", got, err)
	}
}

func TestResolveEnvRejectsInvalidNames(t *testing.T) {
	rc := &repoContext{cfg: &config.RepoConfig{}}

	t.Setenv("SMITH_ENV", "")
	if _, err := rc.resolveEnv(envCmd(t, "../prod", true)); err == nil {
		t.Error("invalid --env: no error")
	}

	t.Setenv("SMITH_ENV", "NOPE")
	if _, err := rc.resolveEnv(envCmd(t, "", false)); err == nil {
		t.Error("invalid $SMITH_ENV: no error")
	}
}

// TestRootEnvFlagEndToEnd drives the real root command — cobra parsing of the
// persistent --env flag, namespace dispatch, dotenv preflight, and a real
// shell step — to prove the selector and the dotenv layers reach the
// subprocess.
func TestRootEnvFlagEndToEnd(t *testing.T) {
	home := setupHome(t)
	repo := t.TempDir()
	markers := filepath.Join(repo, "markers")
	writeDispatchRepo(t, home, repo,
		map[string]manifestSpec{
			"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
		},
		map[string]typeSpec{
			"mock/std/lib@1": {
				targets: map[string][]string{
					"build": {fmt.Sprintf(`printf 'CHAIN=%%s SMITH_ENV=%%s\n' "$CHAIN" "${SMITH_ENV:-unset}" >> %s`, markers)},
				},
				wd: projecttype.WorkingDirRepo,
			},
		})

	// Opt the repo into dotenv with a default environment; write base +
	// overlay files.
	repoYML := filepath.Join(repo, ".smith", "repo.yml")
	data, err := os.ReadFile(repoYML)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repoYML, append(data, []byte("dotenv: true\nsmith_env:\n  default: dev\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEnvFile := func(name, content string) {
		t.Helper()
		path := filepath.Join(repo, "dotenv", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeEnvFile(".env", "CHAIN=base\n")
	writeEnvFile(".dev.env", "CHAIN=overlay\n")
	writeEnvFile(".staging.env", "CHAIN=staging\n")
	t.Chdir(repo)

	readMarkers := func() string {
		t.Helper()
		data, err := os.ReadFile(markers)
		if err != nil {
			t.Fatalf("markers: %v", err)
		}
		return string(data)
	}

	// repo.yml default selects dev: the overlay loads and SMITH_ENV exports.
	t.Setenv("SMITH_ENV", "")
	if _, err := run(t, "libs", "alpha", "build"); err != nil {
		t.Fatalf("default env run: %v", err)
	}
	if got, want := readMarkers(), "CHAIN=overlay SMITH_ENV=dev\n"; got != want {
		t.Errorf("after default run: markers = %q, want %q", got, want)
	}

	// $SMITH_ENV beats the default and its overlay loads.
	t.Setenv("SMITH_ENV", "staging")
	if _, err := run(t, "libs", "alpha", "build"); err != nil {
		t.Fatalf("os env run: %v", err)
	}

	// --env beats $SMITH_ENV.
	if _, err := run(t, "--env", "dev", "libs", "alpha", "build"); err != nil {
		t.Fatalf("flag run: %v", err)
	}

	if got, want := readMarkers(),
		"CHAIN=overlay SMITH_ENV=dev\n"+
			"CHAIN=staging SMITH_ENV=staging\n"+
			"CHAIN=overlay SMITH_ENV=dev\n"; got != want {
		t.Errorf("markers = %q, want the three runs in order", got)
	}

	// A selector with no overlay anywhere is a hard error before steps run.
	if _, err := run(t, "--env", "test", "libs", "alpha", "build"); err == nil ||
		!strings.Contains(err.Error(), "dotenv/.test.env") {
		t.Errorf("--env test error = %v, want it to name dotenv/.test.env", err)
	}
	if got := readMarkers(); strings.Count(got, "\n") != 3 {
		t.Errorf("markers grew despite the failed run: %q", got)
	}
}
