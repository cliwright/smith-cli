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

func TestSubcommandStubs(t *testing.T) {
	// init, tree and sync are implemented (see init_test.go, tree_test.go and
	// sync_test.go) and must not run against the package directory, so they
	// are not in this list.
	for _, cmd := range []string{"new", "types", "doctor", "list"} {
		t.Run(cmd, func(t *testing.T) {
			out, err := run(t, cmd)
			if err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
			if !strings.Contains(out, "not implemented yet") {
				t.Errorf("%s output = %q, want a stub message", cmd, out)
			}
		})
	}
}

func TestNamespaceDispatch(t *testing.T) {
	out, err := run(t, "libs", "my-lib", "test")
	if err != nil {
		t.Fatalf("libs my-lib test: %v", err)
	}
	if !strings.Contains(out, "namespace dispatch: libs my-lib test (not implemented)") {
		t.Errorf("output = %q", out)
	}
}

func TestEachNamespaceDispatches(t *testing.T) {
	for _, ns := range []string{"libs", "services", "tools", "images"} {
		out, err := run(t, ns, "list")
		if err != nil {
			t.Fatalf("%s list: %v", ns, err)
		}
		if !strings.Contains(out, "namespace dispatch: "+ns+" list") {
			t.Errorf("output = %q", out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	_, err := run(t, "frobnicate")
	if err == nil {
		t.Fatal("frobnicate: want error, got nil")
	}
	if !strings.Contains(err.Error(), `unknown command "frobnicate"`) {
		t.Errorf("error = %v", err)
	}
}

func TestParallelFlag(t *testing.T) {
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
