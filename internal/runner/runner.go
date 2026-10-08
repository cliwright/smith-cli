// Package runner runs external commands as subprocesses. It exists so
// commands that shell out — cookiecutter for `smith new`, soon target
// execution for `smith <ns> <name> <target>` — share one small, testable
// wrapper around os/exec instead of growing their own.
//
// Only the modes that exist today are built: run in a directory with chosen
// env and stdio wiring, plus an Interactive preset that passes the process's
// own terminal through (what cookiecutter needs, since templates prompt).
// Capture and parallel modes are deliberately left to the M4 target runner.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Options controls where and how a command runs. A nil Env inherits the
// process environment (os/exec's default); nil stdio readers/writers are
// /dev/null, as with os/exec.
type Options struct {
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Interactive returns Options that wire the process's own terminal through —
// for commands that prompt, like cookiecutter.
func Interactive() Options {
	return Options{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}

// Runner runs commands. The constructor fields exist so tests can replace
// command construction (and PATH resolution) without a real binary.
type Runner struct {
	lookPath func(string) (string, error)
	command  func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// New returns a Runner backed by exec.LookPath and exec.CommandContext.
func New() *Runner {
	return &Runner{
		lookPath: exec.LookPath,
		command:  exec.CommandContext,
	}
}

// Default is the process-wide Runner used by Run.
var Default = New()

// LookPath resolves an executable name against PATH.
func (r *Runner) LookPath(name string) (string, error) {
	return r.lookPath(name)
}

// Run resolves name against PATH, then runs it with the given options. A
// non-zero exit is reported as "name: exited with status N".
func (r *Runner) Run(ctx context.Context, opts Options, name string, args ...string) error {
	if _, err := r.lookPath(name); err != nil {
		return fmt.Errorf("%s: not found on PATH", name)
	}
	cmd := r.command(ctx, name, args...)
	cmd.Dir = opts.Dir
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s: exited with status %d", name, exitErr.ExitCode())
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Run is Default.Run.
func Run(ctx context.Context, opts Options, name string, args ...string) error {
	return Default.Run(ctx, opts, name, args...)
}
