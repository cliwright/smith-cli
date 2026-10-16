package cli

import (
	"slices"
	"strings"
	"testing"
)

// cliArgsFixture builds a repo with two projects sharing a type whose step
// templates {{.SMITH_CLI_ARGS}} — the "--" passthrough probe.
func cliArgsFixture(t *testing.T, repo string) {
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/psenv@1"},
		"libs/beta":  {name: "beta", typ: "mock/std/psenv@1"},
	}, map[string]typeSpec{
		"mock/std/psenv@1": {
			targets: map[string][]string{"env.get": {`echo {{.SMITH_CLI_ARGS}}`}},
		},
	})
}

func envHas(env []string, want string) bool {
	return slices.Contains(env, want)
}

// TestCliArgsRunForm: passthrough tokens reach the step rendered into
// {{.SMITH_CLI_ARGS}} and exported as $SMITH_CLI_ARGS; tokens outside the
// safe charset are single-quoted (round-trip-safe through sh -c).
func TestCliArgsRunForm(t *testing.T) {
	repo := t.TempDir()
	cliArgsFixture(t, repo)
	t.Chdir(repo)

	cases := []struct {
		name    string
		pass    []string
		wantRun string
		wantEnv string
	}{
		{"single token", []string{"--dry-run"}, "echo --dry-run", "SMITH_CLI_ARGS=--dry-run"},
		{"two-word token is quoted", []string{"--name", "John Doe"}, "echo --name 'John Doe'", "SMITH_CLI_ARGS=--name 'John Doe'"},
		{"embedded single quote is escaped", []string{"it's"}, `echo 'it'\''s'`, `SMITH_CLI_ARGS='it'\''s'`},
		{"empty token is quoted", []string{""}, "echo ''", "SMITH_CLI_ARGS=''"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"libs", "alpha", "env.get", "--"}, tt.pass...)
			f := &fakeExec{}
			useFakeExec(t, f)
			if _, err := run(t, args...); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if !slices.Equal(f.runs, []string{tt.wantRun}) {
				t.Errorf("rendered step = %q, want %q", f.runs, tt.wantRun)
			}
			if len(f.envs) != 1 || !envHas(f.envs[0], tt.wantEnv) {
				t.Errorf("step env missing %q: %v", tt.wantEnv, f.envs)
			}
		})
	}
}

// TestCliArgsInterspersedFlags: ArgsLenAtDash through cobra/pflag — flags
// may precede the positional grammar in both spellings, before the "--".
func TestCliArgsInterspersedFlags(t *testing.T) {
	repo := t.TempDir()
	cliArgsFixture(t, repo)
	t.Chdir(repo)

	for _, args := range [][]string{
		{"--env", "dev", "libs", "alpha", "env.get", "--", "--dry-run"},
		{"--env=dev", "libs", "alpha", "env.get", "--", "--dry-run"},
		{"libs", "alpha", "env.get", "--env", "dev", "--", "--dry-run"},
	} {
		f := &fakeExec{}
		useFakeExec(t, f)
		if _, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !slices.Equal(f.runs, []string{"echo --dry-run"}) {
			t.Errorf("%v: rendered step = %q, want the passthrough only", args, f.runs)
		}
		if len(f.envs) != 1 || !envHas(f.envs[0], "SMITH_ENV=dev") || !envHas(f.envs[0], "SMITH_CLI_ARGS=--dry-run") {
			t.Errorf("%v: env = %v, want SMITH_ENV and SMITH_CLI_ARGS", args, f.envs)
		}
	}
}

// TestCliArgsNoDash: without "--" the var is empty and the grammar's
// too-many-arguments error is preserved.
func TestCliArgsNoDash(t *testing.T) {
	repo := t.TempDir()
	cliArgsFixture(t, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "libs", "alpha", "env.get"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !slices.Equal(f.runs, []string{"echo "}) {
		t.Errorf("rendered step = %q, want empty var", f.runs)
	}
	if !envHas(f.envs[0], "SMITH_CLI_ARGS=") {
		t.Errorf("env missing empty SMITH_CLI_ARGS: %v", f.envs[0])
	}

	if _, err := run(t, "libs", "alpha", "env.get", "extra"); err == nil ||
		!strings.Contains(err.Error(), "too many arguments") {
		t.Errorf("no-dash excess args: err = %v, want too-many-arguments", err)
	}
}

// TestCliArgsHelpAfterDash: a token "--help" after "--" is passthrough data
// for the step, not smith's help trigger.
func TestCliArgsHelpAfterDash(t *testing.T) {
	repo := t.TempDir()
	cliArgsFixture(t, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	out, err := run(t, "libs", "alpha", "env.get", "--", "--help")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !slices.Equal(f.runs, []string{"echo --help"}) {
		t.Errorf("rendered step = %q, want the passthrough; output:\n%s", f.runs, out)
	}
}

// TestCliArgsBroadcast: one invocation, one value — namespace-wide and
// repo-wide runs see the same SMITH_CLI_ARGS at every node.
func TestCliArgsBroadcast(t *testing.T) {
	repo := t.TempDir()
	cliArgsFixture(t, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "libs", "env.get", "--", "--dry-run"); err != nil {
		t.Fatalf("namespace-wide: %v", err)
	}
	want := []string{"echo --dry-run", "echo --dry-run"}
	if !slices.Equal(f.runs, want) {
		t.Errorf("namespace-wide steps = %v, want %v", f.runs, want)
	}
	for i, env := range f.envs {
		if !envHas(env, "SMITH_CLI_ARGS=--dry-run") {
			t.Errorf("node %d env missing SMITH_CLI_ARGS: %v", i, env)
		}
	}

	f2 := &fakeExec{}
	useFakeExec(t, f2)
	if _, err := run(t, "env.get", "--", "--dry-run"); err != nil {
		t.Fatalf("repo-wide: %v", err)
	}
	if !slices.Equal(f2.runs, want) {
		t.Errorf("repo-wide steps = %v, want %v", f2.runs, want)
	}
}

// TestCliArgsRepoAttachment: attachment runs inherit the passthrough.
func TestCliArgsRepoAttachment(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/alpha": {name: "alpha", typ: "mock/std/lib@1"},
	}, workspaceTypeSpec, workspaceTarget)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "repo", "python", "setup", "--", "--dry-run"); err != nil {
		t.Fatalf("repo python setup: %v", err)
	}
	for i, env := range f.envs {
		if !envHas(env, "SMITH_CLI_ARGS=--dry-run") {
			t.Errorf("step %d env missing SMITH_CLI_ARGS: %v", i, env)
		}
	}
}

// TestCliArgsServicesRun: service runs inherit the passthrough at every
// component's steps.
func TestCliArgsServicesRun(t *testing.T) {
	repo := t.TempDir()
	authServiceFixture(t, repo)
	t.Chdir(repo)

	f := &fakeExec{}
	useFakeExec(t, f)
	if _, err := run(t, "services", "auth", "deploy", "--", "--dry-run"); err != nil {
		t.Fatalf("services auth deploy: %v", err)
	}
	if len(f.envs) == 0 {
		t.Fatal("no steps ran")
	}
	for i, env := range f.envs {
		if !envHas(env, "SMITH_CLI_ARGS=--dry-run") {
			t.Errorf("step %d env missing SMITH_CLI_ARGS: %v", i, env)
		}
	}
}

// TestCliArgsContextRow: the --context view shows the invocation's
// passthrough among the computed vars.
func TestCliArgsContextRow(t *testing.T) {
	repo := contextFixture(t)
	t.Chdir(repo)

	out, err := run(t, "libs", "alpha", "--context", "--", "--dry-run")
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if !strings.Contains(out, "SMITH_CLI_ARGS=--dry-run") ||
		!strings.Contains(out, "# computed (-- passthrough)") {
		t.Errorf("context view missing the SMITH_CLI_ARGS row:\n%s", out)
	}
}
