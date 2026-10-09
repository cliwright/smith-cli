// Package targetrun builds and executes the make-style target graph: nodes
// are (project, target) pairs, edges are in-project type depends_on entries
// and cross-project manifest depends_on entries. Selection picks roots only;
// the transitive prerequisite closure always runs, each node at most once,
// in deterministic topological order. Execution is serial and fail-fast.
package targetrun

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/runner"
)

// Node is one unit of execution: a named target of a named project.
type Node struct {
	Project string
	Target  string
}

func (n Node) String() string { return n.Project + ":" + n.Target }

// less orders nodes deterministically (project, then target).
func (n Node) less(o Node) bool {
	if n.Project != o.Project {
		return n.Project < o.Project
	}
	return n.Target < o.Target
}

// TypeFor resolves (and verifies) the project type of a discovered project.
// The CLI provides an implementation that checks the installed copy against
// the lock; the graph builder only needs the resulting definition.
type TypeFor func(p discovery.Project) (*projecttype.ProjectType, error)

// Runner is the subprocess seam Execute runs steps through.
type Runner interface {
	Run(ctx context.Context, opts runner.Options, name string, args ...string) error
}

// Plan is a built, topologically ordered target graph ready to execute.
type Plan struct {
	order    []Node
	types    map[string]*projecttype.ProjectType // by project name
	dirs     map[string]string                   // project name → absolute project dir
	projects map[string]discovery.Project        // by project name
	params   map[string]map[string]string        // project name → merged params
}

// Order exposes the plan's nodes in execution order (prerequisites first).
func (p *Plan) Order() []Node { return p.order }

// Build computes the transitive prerequisite closure of roots and returns it
// in topological order. Every referenced (project, target) node must exist in
// its project's type — a dangling target, including one named by a manifest
// depends_on entry, is a hard error naming project, target, and type.
func Build(projects []discovery.Project, typeFor TypeFor, roots []Node) (*Plan, error) {
	byName := make(map[string]discovery.Project, len(projects))
	for _, p := range projects {
		byName[p.Manifest.Name] = p
	}

	types := map[string]*projecttype.ProjectType{}
	params := map[string]map[string]string{}

	// resolveParams merges type defaults with manifest overrides. A manifest
	// param the type does not declare is a hard error naming project, key,
	// and type.
	resolveParams := func(p discovery.Project, t *projecttype.ProjectType) (map[string]string, error) {
		merged := t.Defaults()
		keys := make([]string, 0, len(p.Manifest.Params))
		for key := range p.Manifest.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, declared := t.Params[key]; !declared {
				return nil, fmt.Errorf("project %q (type %s@%d) sets param %q, which the type does not declare", p.Manifest.Name, t.Name, t.Version, key)
			}
			merged[key] = p.Manifest.Params[key]
		}
		return merged, nil
	}

	resolve := func(name string) (*projecttype.ProjectType, error) {
		if t, ok := types[name]; ok {
			return t, nil
		}
		p, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("project %q is not part of this repository", name)
		}
		t, err := typeFor(p)
		if err != nil {
			return nil, err
		}
		types[name] = t
		merged, err := resolveParams(p, t)
		if err != nil {
			return nil, err
		}
		params[name] = merged
		return t, nil
	}

	// prereqs computes the edges feeding node n: in-project type depends_on,
	// plus manifest depends_on entries whose "before" list contains n.Target.
	prereqs := func(n Node) ([]Node, error) {
		t, err := resolve(n.Project)
		if err != nil {
			return nil, err
		}
		if _, ok := t.Targets[n.Target]; !ok {
			return nil, fmt.Errorf("project %q (type %s@%d) has no target %q", n.Project, t.Name, t.Version, n.Target)
		}
		var pre []Node
		for _, depTarget := range t.DependsOn[n.Target] {
			if _, ok := t.Targets[depTarget]; !ok {
				return nil, fmt.Errorf("type %s of project %q depends on target %q before %q, but %q is not a declared target", t.Name, n.Project, depTarget, n.Target, depTarget)
			}
			pre = append(pre, Node{n.Project, depTarget})
		}
		p := byName[n.Project]
		for _, d := range p.Manifest.DependsOn {
			dt, err := resolve(d.Project)
			if err != nil {
				return nil, err
			}
			if !contains(d.Before, n.Target) {
				continue
			}
			for _, depTarget := range d.Targets {
				if _, ok := dt.Targets[depTarget]; !ok {
					return nil, fmt.Errorf("manifest of project %q depends on %q with target %q, but %q's type %s declares no such target", n.Project, d.Project, depTarget, d.Project, dt.Name)
				}
				pre = append(pre, Node{d.Project, depTarget})
			}
		}
		sort.Slice(pre, func(i, j int) bool { return pre[i].less(pre[j]) })
		return pre, nil
	}

	plan := &Plan{
		types:    types,
		dirs:     map[string]string{},
		projects: byName,
		params:   params,
	}
	const (
		white = iota // unvisited
		grey         // on the current DFS stack
		black        // done
	)
	color := map[Node]int{}
	var stack []Node

	var visit func(n Node) error
	visit = func(n Node) error {
		switch color[n] {
		case black:
			return nil
		case grey:
			start := 0
			for i, s := range stack {
				if s == n {
					start = i
					break
				}
			}
			cycle := append(append([]Node{}, stack[start:]...), n)
			parts := make([]string, len(cycle))
			for i, c := range cycle {
				parts[i] = c.String()
			}
			return fmt.Errorf("dependency cycle: %s", strings.Join(parts, " -> "))
		}
		color[n] = grey
		stack = append(stack, n)
		pre, err := prereqs(n)
		if err != nil {
			return err
		}
		for _, p := range pre {
			if err := visit(p); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		plan.order = append(plan.order, n)
		return nil
	}

	for _, root := range roots {
		if _, ok := byName[root.Project]; !ok {
			return nil, fmt.Errorf("project %q is not part of this repository", root.Project)
		}
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	for _, p := range projects {
		plan.dirs[p.Manifest.Name] = p.Dir
	}
	return plan, nil
}

// Execute runs the plan serially, fail-fast: the first failing step aborts
// the run with an error naming "project: target" and the exit status (via
// the runner). Each step runs as `sh -c <step>` in the project's directory —
// or the repo root when the type sets working_dir: repo. Steps and
// environment values are Go templates over the merged params (type defaults
// <- manifest overrides). The step environment is, lowest to highest
// precedence: the process environment, the type's environment, the
// manifest's environment, then the computed SMITH_REPO_ROOT / SMITH_PROJECT /
// SMITH_TARGET (appended last, so nothing can shadow them). A header line
// per node is written to out before its steps.
func (p *Plan) Execute(ctx context.Context, r Runner, repoRoot string, out io.Writer) error {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}
	for _, n := range p.order {
		t := p.types[n.Project]
		dir := absRoot
		if t.WorkingDir != projecttype.WorkingDirRepo {
			dir = filepath.Join(absRoot, filepath.FromSlash(p.dirs[n.Project]))
		}
		params := p.params[n.Project]

		var steps []string
		for _, step := range t.Targets[n.Target] {
			rendered, err := projecttype.RenderTemplate(step, params)
			if err != nil {
				return fmt.Errorf("%s: rendering step: %w", n, err)
			}
			steps = append(steps, rendered)
		}

		env, err := p.stepEnv(n, t, params, absRoot)
		if err != nil {
			return fmt.Errorf("%s: building environment: %w", n, err)
		}

		fmt.Fprintf(out, "==> %s\n", n)
		for _, step := range steps {
			opts := runner.Interactive()
			opts.Dir = dir
			opts.Env = env
			if err := r.Run(ctx, opts, "sh", "-c", step); err != nil {
				return fmt.Errorf("%s: %w", n, err)
			}
		}
	}
	return nil
}

// stepEnv builds the environment for one node: os.Environ() first, then the
// rendered type environment, then the rendered manifest environment, then
// the computed SMITH_* variables (appended last so they win).
func (p *Plan) stepEnv(n Node, t *projecttype.ProjectType, params map[string]string, absRoot string) ([]string, error) {
	env := os.Environ()
	appendLayer := func(vars map[string]string) error {
		keys := make([]string, 0, len(vars))
		for key := range vars {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rendered, err := projecttype.RenderTemplate(vars[key], params)
			if err != nil {
				return fmt.Errorf("rendering %q: %w", key, err)
			}
			env = append(env, key+"="+rendered)
		}
		return nil
	}
	if err := appendLayer(t.Environment); err != nil {
		return nil, fmt.Errorf("type environment: %w", err)
	}
	if err := appendLayer(p.projects[n.Project].Manifest.Environment); err != nil {
		return nil, fmt.Errorf("manifest environment: %w", err)
	}
	env = append(env,
		"SMITH_REPO_ROOT="+absRoot,
		"SMITH_PROJECT="+n.Project,
		"SMITH_TARGET="+n.Target,
	)
	return env, nil
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
