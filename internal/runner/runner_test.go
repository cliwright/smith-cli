package runner

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	ctx := context.Background()

	t.Run("stdout captured and dir honored", func(t *testing.T) {
		dir := t.TempDir()
		var buf bytes.Buffer
		err := Run(ctx, Options{Dir: dir, Stdout: &buf}, "sh", "-c", "pwd")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(buf.String()); got != dir {
			t.Errorf("pwd = %q, want %q", got, dir)
		}
	})

	t.Run("env override", func(t *testing.T) {
		var buf bytes.Buffer
		err := Run(ctx, Options{Env: []string{"FOO=bar"}, Stdout: &buf}, "sh", "-c", `printf %s "$FOO"`)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if buf.String() != "bar" {
			t.Errorf("FOO = %q, want %q", buf.String(), "bar")
		}
	})

	t.Run("non-zero exit reports status", func(t *testing.T) {
		err := Run(ctx, Options{}, "sh", "-c", "exit 3")
		if err == nil || !strings.Contains(err.Error(), "exited with status 3") {
			t.Errorf("Run error = %v, want status 3", err)
		}
	})

	t.Run("missing binary reports not found", func(t *testing.T) {
		err := Run(ctx, Options{}, "definitely-not-a-real-binary-xyz")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("Run error = %v, want not-found", err)
		}
	})
}

func TestInteractive(t *testing.T) {
	opts := Interactive()
	if opts.Stdin != os.Stdin || opts.Stdout != os.Stdout || opts.Stderr != os.Stderr {
		t.Errorf("Interactive() = %+v, want the process's own terminal", opts)
	}
}

// TestLookPath exercises the injectable resolution seam.
func TestLookPath(t *testing.T) {
	r := New()
	if _, err := r.LookPath("sh"); err != nil {
		t.Errorf("LookPath(sh): %v", err)
	}
	if _, err := r.LookPath("definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("LookPath(fake) = nil, want error")
	}
}
