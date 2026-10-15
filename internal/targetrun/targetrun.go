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
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/dotenv"
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

// Resolved is a project's composed project types plus the ownership indexes
// the runner needs: SMITH_TYPE_DIR is the OWNING type's installed dir, per
// target (steps, environment) and per param (phase-A rendering), so identity
// is preserved rather than synthesized into one merged type.
type Resolved struct {
	Types    []*projecttype.ProjectType // declaration order
	ByTarget map[string]*projecttype.ProjectType
	ByParam  map[string]*projecttype.ProjectType
	Dirs     map[string]string // "name@version" → absolute installed dir (.smith/types/<name>@v<N>)
}

// ref is the canonical "name@version" form of a resolved type, keying Dirs.
func ref(t *projecttype.ProjectType) string {
	return t.Name + "@" + strconv.Itoa(t.Version)
}

// refs joins the declaration-order refs as "a@1, b@2" for messages.
func (rt *Resolved) refs() string {
	parts := make([]string, len(rt.Types))
	for i, t := range rt.Types {
		parts[i] = ref(t)
	}
	return strings.Join(parts, ", ")
}

// TypeLabel names the project's types for error messages: "type a@1" when
// the project lists one type, "types a@1, b@2" when it lists several.
func (rt *Resolved) TypeLabel() string {
	if len(rt.Types) == 1 {
		return "type " + ref(rt.Types[0])
	}
	return "types " + rt.refs()
}

// TypeFor resolves (and verifies) the project types of a discovered project.
// The CLI provides an implementation that checks each installed copy against
// the lock and composes the refs; the graph builder only needs the resulting
// Resolved.
type TypeFor func(p discovery.Project) (*Resolved, error)

// Runner is the subprocess seam Execute runs steps through.
type Runner interface {
	Run(ctx context.Context, opts runner.Options, name string, args ...string) error
}

// Plan is a built, topologically ordered target graph ready to execute.
type Plan struct {
	order    []Node
	resolved map[string]*Resolved         // by project name
	dirs     map[string]string            // project name → absolute project dir
	projects map[string]discovery.Project // by project name
	params   map[string]map[string]string // project name → merged params

	// Dotenv configuration, set by the caller before PrepareDotenv and
	// Execute. RepoDotenv mirrors the dotenv: key of .smith/repo.yml;
	// SmithEnv is the resolved environment selector ("" = none selected).
	// Neither affects graph shape — only the step environment.
	RepoDotenv bool
	SmithEnv   string

	dotenvRepo     []string            // cached repo-level KEY=VALUE entries
	dotenvProjects map[string][]string // project name → cached project-level entries
}

// Order exposes the plan's nodes in execution order (prerequisites first).
func (p *Plan) Order() []Node { return p.order }

// ReservedTemplateVars are the smith-computed template variables available
// in the rendering pipeline: uppercase keys, collision-free with params by
// construction (the param key pattern is lowercase-only). SMITH_TARGET,
// SMITH_ENV, and SMITH_PAGER are deliberately NOT here — they are
// subprocess-env-only runtime selectors, since manifests and params are
// neither target- nor run-scoped.
var ReservedTemplateVars = []string{"SMITH_REPO_ROOT", "SMITH_PROJECT", "SMITH_PROJECT_DIR", "SMITH_TYPE_DIR"}

// ComputedVars builds the reserved template context for one project. dir is
// the project directory relative to the (absolute) repo root; typeDir is
// the absolute installed directory of the project's type (.smith/types
// <name>@v<N>), the anchor for type-shipped scripts and help.
func ComputedVars(absRoot, dir, project, typeDir string) map[string]string {
	return map[string]string{
		"SMITH_REPO_ROOT":   absRoot,
		"SMITH_PROJECT":     project,
		"SMITH_PROJECT_DIR": filepath.Join(absRoot, filepath.FromSlash(dir)),
		"SMITH_TYPE_DIR":    typeDir,
	}
}

var missingKeyPattern = regexp.MustCompile(`map has no entry for key "([^"]+)"`)

// phaseAError classifies a failed phase-A render (manifest param value
// against computed vars only): referencing another param gets the dedicated
// message, an unknown var a naming one, anything else the raw error with
// project/key/type context.
func phaseAError(p discovery.Project, t *projecttype.ProjectType, key string, err error) error {
	where := fmt.Sprintf("project %q (type %s@%d) param %q", p.Manifest.Name, t.Name, t.Version, key)
	if m := missingKeyPattern.FindStringSubmatch(err.Error()); m != nil {
		missing := m[1]
		if _, declared := t.Params[missing]; declared {
			return fmt.Errorf("%s: param values may reference computed vars (SMITH_*) only, not other params (references %q)", where, missing)
		}
		return fmt.Errorf("%s: unknown var %q; param values may reference computed vars (%s) only",
			where, missing, strings.Join(ReservedTemplateVars, ", "))
	}
	return fmt.Errorf("%s: %w", where, err)
}

// ResolveParams merges type defaults with rendered manifest overrides for one
// project. Defaults merge across rt.Types in declaration order (param keys
// are disjoint by composition). Phase A (strict): manifest param values
// render against the computed vars ONLY — a value referencing another param
// is a hard error naming project, key, and type. A manifest param no type
// declares is likewise a hard error naming the project's types; each value
// renders with SMITH_TYPE_DIR bound to the OWNING type's installed dir.
// Exported for the --context and help views, which resolve a single project
// without building a graph.
func ResolveParams(absRoot string, p discovery.Project, rt *Resolved) (map[string]string, error) {
	merged := map[string]string{}
	for _, t := range rt.Types {
		for key, def := range t.Defaults() {
			merged[key] = def
		}
	}
	keys := make([]string, 0, len(p.Manifest.Params))
	for key := range p.Manifest.Params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		owner, declared := rt.ByParam[key]
		if !declared {
			return nil, fmt.Errorf("project %q (%s) sets param %q, which the type does not declare",
				p.Manifest.Name, rt.TypeLabel(), key)
		}
		rendered, err := projecttype.RenderTemplate(p.Manifest.Params[key], ComputedVars(absRoot, p.Dir, p.Manifest.Name, rt.Dirs[ref(owner)]))
		if err != nil {
			return nil, phaseAError(p, owner, key, err)
		}
		merged[key] = rendered
	}
	return merged, nil
}

// Build computes the transitive prerequisite closure of roots and returns it
// in topological order. Every referenced (project, target) node must be
// declared by one of its project's types — a dangling target, including one
// named by a manifest depends_on entry, is a hard error naming project,
// target, and type. repoRoot scopes the reserved template vars
// (SMITH_REPO_ROOT, SMITH_PROJECT, SMITH_PROJECT_DIR); the installed type
// dirs (SMITH_TYPE_DIR) ride along in Resolved.
func Build(projects []discovery.Project, typeFor TypeFor, roots []Node, repoRoot string) (*Plan, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving repo root: %w", err)
	}

	byName := make(map[string]discovery.Project, len(projects))
	for _, p := range projects {
		byName[p.Manifest.Name] = p
	}

	resolved := map[string]*Resolved{}
	params := map[string]map[string]string{}

	resolve := func(name string) (*Resolved, error) {
		if rt, ok := resolved[name]; ok {
			return rt, nil
		}
		p, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("project %q is not part of this repository", name)
		}
		rt, err := typeFor(p)
		if err != nil {
			return nil, err
		}
		resolved[name] = rt
		merged, err := ResolveParams(absRoot, p, rt)
		if err != nil {
			return nil, err
		}
		params[name] = merged
		return rt, nil
	}

	// prereqs computes the edges feeding node n: in-project depends_on walked
	// on the OWNING type of n.Target only (cross-type wiring is rejected at
	// composition), plus manifest depends_on entries whose "before" list
	// contains n.Target.
	prereqs := func(n Node) ([]Node, error) {
		rt, err := resolve(n.Project)
		if err != nil {
			return nil, err
		}
		owner, ok := rt.ByTarget[n.Target]
		if !ok {
			return nil, fmt.Errorf("project %q (%s) has no target %q", n.Project, rt.TypeLabel(), n.Target)
		}
		var pre []Node
		for _, depTarget := range owner.DependsOn[n.Target] {
			if _, ok := owner.Targets[depTarget]; !ok {
				return nil, fmt.Errorf("type %s of project %q depends on target %q before %q, but %q is not a declared target",
					ref(owner), n.Project, depTarget, n.Target, depTarget)
			}
			pre = append(pre, Node{n.Project, depTarget})
		}
		p := byName[n.Project]
		for _, d := range p.Manifest.DependsOn {
			drt, err := resolve(d.Project)
			if err != nil {
				return nil, err
			}
			if !contains(d.Before, n.Target) {
				continue
			}
			for _, depTarget := range d.Targets {
				if _, ok := drt.ByTarget[depTarget]; !ok {
					return nil, fmt.Errorf("manifest of project %q depends on %q with target %q, but %q (%s) declares no such target",
						n.Project, d.Project, depTarget, d.Project, drt.TypeLabel())
				}
				pre = append(pre, Node{d.Project, depTarget})
			}
		}
		sort.Slice(pre, func(i, j int) bool { return pre[i].less(pre[j]) })
		return pre, nil
	}

	plan := &Plan{
		resolved: resolved,
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

// PrepareDotenv loads the dotenv layers for everything the plan will run and
// caches them for stepEnv, failing loudly before any step runs. Per enabled
// level — the repo (dotenv: in .smith/repo.yml) and then each involved
// project (dotenv: in its smith.yml) — the base file dotenv/.env must exist
// and merges with the selected environment's overlay dotenv/.<env>.env, which
// wins per key within that level. A level that did not opt in contributes
// nothing. When an environment is selected but no overlay exists at any
// enabled level, that is a hard error — the selector promised a file.
func (p *Plan) PrepareDotenv(repoRoot string) error {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}
	p.dotenvRepo = nil
	p.dotenvProjects = map[string][]string{}
	overlays := 0
	enabled := false

	// loadLevel loads one opt-in level via dotenv.LoadStack and flattens its
	// layers into sorted KEY=VALUE entries (base first, overlay winning).
	loadLevel := func(dir, source string, optedIn bool) ([]string, error) {
		if !optedIn {
			return nil, nil
		}
		enabled = true
		layers, err := dotenv.LoadStack(dir, p.SmithEnv, source)
		if err != nil {
			return nil, err
		}
		overlays += dotenv.Overlays(layers)
		merged := map[string]string{}
		for _, layer := range layers {
			merged = dotenv.Merge(merged, layer.Vars)
		}
		return dotenv.Entries(merged), nil
	}

	entries, err := loadLevel(absRoot, ".smith/repo.yml", p.RepoDotenv)
	if err != nil {
		return err
	}
	p.dotenvRepo = entries

	involved := map[string]bool{}
	for _, n := range p.order {
		involved[n.Project] = true
	}
	names := make([]string, 0, len(involved))
	for name := range involved {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !p.projects[name].Manifest.Dotenv {
			continue
		}
		dir := absRoot
		if p.dirs[name] != "." {
			dir = filepath.Join(absRoot, filepath.FromSlash(p.dirs[name]))
		}
		entries, err := loadLevel(dir, fmt.Sprintf("project %q (smith.yml)", name), true)
		if err != nil {
			return err
		}
		p.dotenvProjects[name] = entries
	}

	if p.SmithEnv != "" && enabled && overlays == 0 {
		return dotenv.NoOverlayError(p.SmithEnv)
	}
	return nil
}

// Execute runs the plan serially, fail-fast: the first failing step aborts
// the run with an error naming "project: target" and the exit status (via
// the runner). Each step runs as `sh -c <step>` in the project's directory —
// or the repo root when the OWNING type sets working_dir: repo, unless the
// manifest overrides it (manifest working_dir wins over the type's; composed
// types must agree on working_dir). Steps and environment values are Go
// templates over the merged params (type defaults <- manifest overrides).
// The step environment is, lowest to highest precedence: the process
// environment; the dotenv layers cached by PrepareDotenv (repo dotenv/.env,
// repo dotenv/.<env>.env, project dotenv/.env, project dotenv/.<env>.env);
// each type's environment in declaration order; the manifest's environment;
// then the computed SMITH_REPO_ROOT / SMITH_PROJECT / SMITH_TARGET /
// SMITH_TYPE_DIR (the OWNING type's installed dir), plus SMITH_ENV when an
// environment is selected (appended last, so nothing can shadow them). A
// header line per node is written to out before its steps.
func (p *Plan) Execute(ctx context.Context, r Runner, repoRoot string, out io.Writer) error {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}
	for _, n := range p.order {
		rt := p.resolved[n.Project]
		t := rt.ByTarget[n.Target]
		dir := absRoot
		// The manifest's working_dir overrides the type's (same two values);
		// empty means inherit, and a type omitting it defaults to project.
		workingDir := t.WorkingDir
		if m := p.projects[n.Project].Manifest.WorkingDir; m != "" {
			workingDir = projecttype.WorkingDir(m)
		}
		if workingDir != projecttype.WorkingDirRepo {
			dir = filepath.Join(absRoot, filepath.FromSlash(p.dirs[n.Project]))
		}
		params := p.params[n.Project]
		// Phase B: steps and environment values render against the merged
		// params PLUS the reserved computed vars (computed wins on the
		// (impossible) collision, by construction). SMITH_TYPE_DIR is the
		// owning type's installed dir for this target.
		context := make(map[string]string, len(params)+len(ReservedTemplateVars))
		for key, value := range params {
			context[key] = value
		}
		for key, value := range ComputedVars(absRoot, p.dirs[n.Project], n.Project, rt.Dirs[ref(t)]) {
			context[key] = value
		}

		var steps []string
		for _, step := range t.Targets[n.Target].Steps {
			rendered, err := projecttype.RenderTemplate(step, context)
			if err != nil {
				return fmt.Errorf("%s: rendering step: %w", n, err)
			}
			steps = append(steps, rendered)
		}

		env, err := p.stepEnv(n, rt, t, context, absRoot)
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
// cached dotenv layers (repo, then project — see PrepareDotenv), then each
// type's environment in declaration order (keys are disjoint by composition),
// then the rendered manifest environment, then the computed SMITH_* variables
// (appended last so they win), plus SMITH_ENV when an environment is
// selected. SMITH_PROJECT_DIR is template-only and deliberately NOT exported
// here; SMITH_TYPE_DIR IS — a step shells out to $SMITH_TYPE_DIR/scripts/...
// and should not have to template it; it is the OWNING type's dir for this
// target.
func (p *Plan) stepEnv(n Node, rt *Resolved, t *projecttype.ProjectType, context map[string]string, absRoot string) ([]string, error) {
	env := os.Environ()
	env = append(env, p.dotenvRepo...)
	if projectEntries, ok := p.dotenvProjects[n.Project]; ok {
		env = append(env, projectEntries...)
	}
	appendLayer := func(vars map[string]string) error {
		keys := make([]string, 0, len(vars))
		for key := range vars {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rendered, err := projecttype.RenderTemplate(vars[key], context)
			if err != nil {
				return fmt.Errorf("rendering %q: %w", key, err)
			}
			env = append(env, key+"="+rendered)
		}
		return nil
	}
	for _, typ := range rt.Types {
		if err := appendLayer(typ.Environment); err != nil {
			return nil, fmt.Errorf("type %s environment: %w", ref(typ), err)
		}
	}
	if err := appendLayer(p.projects[n.Project].Manifest.Environment); err != nil {
		return nil, fmt.Errorf("manifest environment: %w", err)
	}
	env = append(env,
		"SMITH_REPO_ROOT="+absRoot,
		"SMITH_PROJECT="+n.Project,
		"SMITH_TARGET="+n.Target,
		"SMITH_TYPE_DIR="+rt.Dirs[ref(t)],
	)
	if p.SmithEnv != "" {
		env = append(env, "SMITH_ENV="+p.SmithEnv)
	}
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
