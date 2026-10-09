package cli

import (
	"bytes"
	"strings"
	"testing"
)

// run executes the root command with args and captures everything it prints.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestRootHelpListsCommandTree(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"init", "new", "sync", "types", "doctor", "tree", "list", "--parallel"} {
		if !strings.Contains(out, want) {
			t.Errorf("root help missing %q:\n%s", want, out)
		}
	}
}

// TestSubcommandStubs was removed when `smith types` — the last stub — was
// implemented; only `smith list` remains stubbed (see commands.go), and its
// milestone has not landed.

// Namespace dispatch is fully covered in namespace_test.go; these two
// stub-era tests (namespace dispatch stub messages) were removed when the
// dispatcher became real.

func TestUnknownCommand(t *testing.T) {
	repo := buildMockRepo(t, []string{"libs"}, map[string][2]string{
		"libs/only": {"only", "python/astral/lib@1"},
	})
	t.Chdir(repo)
	out, err := run(t, "frobnicate", "extra")
	if err == nil {
		t.Fatal("frobnicate extra: want error, got nil")
	}
	if !strings.Contains(err.Error(), `unknown command "frobnicate"`) {
		t.Errorf("error = %v", err)
	}
	// Errors must not be followed by the usage dump: a failure is not a
	// usage mistake, and runtime failures (target exits) flow through the
	// same root dispatch.
	if strings.Contains(out, "Usage:") {
		t.Errorf("output after error contains usage dump:\n%s", out)
	}
}

func TestParallelFlag(t *testing.T) {
	repo := buildMockRepo(t, []string{"libs"}, nil)
	t.Chdir(repo)
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--parallel", "4", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--parallel 4 list: %v", err)
	}
	n, err := cmd.Flags().GetInt("parallel")
	if err != nil {
		t.Fatalf("GetInt: %v", err)
	}
	if n != 4 {
		t.Errorf("--parallel = %d, want 4", n)
	}
}
