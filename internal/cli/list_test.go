package cli

import (
	"testing"
)

func TestListPrintsNamespaces(t *testing.T) {
	repo := buildMockRepo(t, []string{"libs", "tools", "services", "images"}, nil)
	t.Chdir(repo)

	out, err := run(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Declared roots in declaration order, then the reserved repo namespace.
	want := "libs\ntools\nservices\nimages\nrepo\n"
	if out != want {
		t.Errorf("list output = %q, want %q", out, want)
	}
}

func TestListOutsideRepoErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := run(t, "list"); err == nil {
		t.Fatal("list outside a repo: want error, got nil")
	}
}
