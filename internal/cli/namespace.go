package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/runner"
	"github.com/cliwright/smith/internal/targetrun"
	"github.com/cliwright/smith/internal/typeref"
)

// nsExecRunner runs target steps; it is a variable so tests can substitute a
// recording fake (the default passes stdio through to the real terminal).
var nsExecRunner targetrun.Runner = runner.Default

// repoContext is everything the namespace dispatcher needs from the
// repository: config, discovered projects, and lazily resolved, lock-verified
// project types.
type repoContext struct {
	repoRoot string
	cfg      *config.RepoConfig
	projects []discovery.Project // sorted by Dir
	lockFile *lock.Lock
	types    map[string]*projecttype.ProjectType // by project name, resolved lazily
}

func loadRepoContext() (*repoContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("smith: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(filepath.Join(repoRoot, cacheDirName, repoYMLName))
	if err != nil {
		return nil, err
	}
	projects, err := discovery.FindProjects(repoRoot, cfg)
	if err != nil {
		return nil, err
	}
	return &repoContext{
		repoRoot: repoRoot,
		cfg:      cfg,
		projects: projects,
		types:    map[string]*projecttype.ProjectType{},
	}, nil
}

// lock loads and caches .smith/lock.json. A missing lock is a hard error:
// nothing can run without chain of custody.
func (rc *repoContext) lock() (*lock.Lock, error) {
	if rc.lockFile != nil {
		return rc.lockFile, nil
	}
	lockPath := filepath.Join(rc.repoRoot, cacheDirName, lockJSONName)
	lk, err := lock.Load(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no %s found; run smith sync", filepath.Join(cacheDirName, lockJSONName))
		}
		return nil, err
	}
	rc.lockFile = lk
	return lk, nil
}

// typeFor returns the project type of a discovered project, verifying the
// installed copy against the lock before use. Missing, unpinned, or drifted
// types are hard errors pointing at smith sync.
func (rc *repoContext) typeFor(p discovery.Project) (*projecttype.ProjectType, error) {
	if t, ok := rc.types[p.Manifest.Name]; ok {
		return t, nil
	}
	ref, err := typeref.Parse(p.Manifest.Type)
	if err != nil {
		return nil, err
	}
	lk, err := rc.lock()
	if err != nil {
		return nil, err
	}
	pin, ok := lk.Types[ref.String()]
	if !ok {
		return nil, fmt.Errorf("type %s is not pinned in %s; run smith sync", ref, filepath.Join(cacheDirName, lockJSONName))
	}
	path, err := rc.installedPath(ref)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("installed type %s is missing (%s); run smith sync", ref, path)
	}
	if hashBytes(data) != pin.Hash {
		return nil, fmt.Errorf("installed type %s failed hash verification; run smith sync", ref)
	}
	pt, err := projecttype.Load(path)
	if err != nil {
		return nil, fmt.Errorf("installed type %s: %w", ref, err)
	}
	rc.types[p.Manifest.Name] = pt
	return pt, nil
}

func (rc *repoContext) installedPath(ref typeref.TypeRef) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, cacheDirName, "project-types", installRelPath(ref)), nil
}

// namespaces are the first path segments of the declared project_roots.
func (rc *repoContext) namespaces() []string {
	seen := map[string]bool{}
	var ns []string
	for _, root := range rc.cfg.Workspace.ProjectRoots {
		first := strings.Split(filepath.ToSlash(root), "/")[0]
		if !seen[first] {
			seen[first] = true
			ns = append(ns, first)
		}
	}
	sort.Strings(ns)
	return ns
}

// projectsIn returns the projects under one namespace, sorted by name. A
// project sitting directly at the root dir belongs to that namespace too.
func (rc *repoContext) projectsIn(ns string) []discovery.Project {
	var out []discovery.Project
	for _, p := range rc.projects {
		if p.Dir == ns || strings.HasPrefix(p.Dir, ns+"/") {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out
}

func projectByName(projects []discovery.Project, name string) (discovery.Project, bool) {
	for _, p := range projects {
		if p.Manifest.Name == name {
			return p, true
		}
	}
	return discovery.Project{}, false
}

func projectNames(projects []discovery.Project) []string {
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Manifest.Name
	}
	return names
}

// attachments models repo-level targets (repo_targets) as synthetic
// projects: an in-memory manifest named by the alias with the repo root as
// its directory, routed through the ordinary targetrun machinery. Sorted by
// alias.
func (rc *repoContext) attachments() []discovery.Project {
	aliases := make([]string, 0, len(rc.cfg.RepoTargets))
	for alias := range rc.cfg.RepoTargets {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	out := make([]discovery.Project, 0, len(aliases))
	for _, alias := range aliases {
		rt := rc.cfg.RepoTargets[alias]
		out = append(out, discovery.Project{
			Dir: ".",
			Manifest: &manifest.Manifest{
				Name:        alias,
				Type:        rt.Type,
				Params:      rt.Params,
				Environment: rt.Environment,
			},
		})
	}
	return out
}

// dispatchRepo handles "smith repo ..." — the synthetic namespace backed by
// repo_targets (attachments are not discovered directories):
//
//	smith repo                 list attachments (same as "repo list")
//	smith repo list            list attachments
//	smith repo <name>          list that attachment's targets
//	smith repo <name> list     list that attachment's targets (reserved word, like project namespaces)
//	smith repo <name> <target> run target on the attachment
//
// Unlike directory namespaces there is no repo-wide attachment run (bare
// `smith <target>` never includes attachments), so position 2 always names
// an attachment here.
func (rc *repoContext) dispatchRepo(cmd *cobra.Command, rest []string) error {
	out := stdout(cmd)
	attachments := rc.attachments()

	if len(rest) == 0 || (rest[0] == "list" && len(rest) == 1) {
		if len(attachments) == 0 {
			fmt.Fprintf(out, "no repo targets defined; add a repo_targets entry to %s\n",
				filepath.Join(cacheDirName, repoYMLName))
			return nil
		}
		for _, a := range attachments {
			fmt.Fprintln(out, a.Manifest.Name)
		}
		return nil
	}
	if rest[0] == "list" {
		return fmt.Errorf("list takes no further arguments")
	}

	att, ok := projectByName(attachments, rest[0])
	if !ok {
		if len(attachments) == 0 {
			return fmt.Errorf("unknown repo target %q (no repo targets defined; add a repo_targets entry to %s)",
				rest[0], filepath.Join(cacheDirName, repoYMLName))
		}
		return fmt.Errorf("unknown repo target %q (repo targets: %s)",
			rest[0], strings.Join(projectNames(attachments), ", "))
	}

	if len(rest) == 1 {
		return rc.listTargets(cmd, att)
	}
	if len(rest) > 2 {
		return fmt.Errorf("too many arguments: %s", strings.Join(rest, " "))
	}
	if rest[1] == "list" {
		return rc.listTargets(cmd, att)
	}

	t, err := rc.typeFor(att)
	if err != nil {
		return err
	}
	if _, ok := t.Targets[rest[1]]; !ok {
		return fmt.Errorf("repo target %q (type %s@%d) has no target %q (targets: %s)",
			att.Manifest.Name, t.Name, t.Version, rest[1], strings.Join(sortedKeys(t.Targets), ", "))
	}
	warnParallel(cmd)
	plan, err := targetrun.Build([]discovery.Project{att}, rc.typeFor,
		[]targetrun.Node{{Project: att.Manifest.Name, Target: rest[1]}}, rc.repoRoot)
	if err != nil {
		return err
	}
	return plan.Execute(context.Background(), nsExecRunner, rc.repoRoot, stdout(cmd))
}

// dispatchNamespace handles "smith <ns> ..." — the confirmed grammar:
//
//	smith <ns>                 list projects (same as "<ns> list")
//	smith <ns> list            list projects
//	smith <ns> <name>          TARGET WINS: run <name> on every project in the ns
//	smith <ns> <name> list     list that project's targets
//	smith <ns> <name> <target> run target on that project
//	smith <ns> <target>        run target on every project in the ns that declares it
func (rc *repoContext) dispatchNamespace(cmd *cobra.Command, ns string, rest []string) error {
	out := stdout(cmd)
	nsProjects := rc.projectsIn(ns)

	if len(rest) == 0 || (rest[0] == "list" && len(rest) == 1) {
		if len(nsProjects) == 0 {
			fmt.Fprintf(out, "no projects in %q\n", ns)
			return nil
		}
		for _, p := range nsProjects {
			fmt.Fprintln(out, p.Manifest.Name)
		}
		return nil
	}
	if rest[0] == "list" {
		return fmt.Errorf("list takes no further arguments")
	}

	name := rest[0]
	_, isProject := projectByName(nsProjects, name)

	if len(rest) == 1 {
		// TARGET WINS over a project name: a lib named "test" does not
		// shadow `smith libs test` (reach it via `smith libs test list`).
		return rc.runTarget(cmd, rest[0], nsProjects, fmt.Sprintf("namespace %q", ns), ns)
	}

	if len(rest) > 2 {
		return fmt.Errorf("too many arguments: %s", strings.Join(rest, " "))
	}

	if rest[1] == "list" {
		if !isProject {
			return unknownProjectError(ns, name, nsProjects)
		}
		p, _ := projectByName(nsProjects, name)
		return rc.listTargets(cmd, p)
	}

	if !isProject {
		return unknownProjectError(ns, name, nsProjects)
	}
	p, _ := projectByName(nsProjects, name)
	return rc.runSingle(cmd, p, rest[1])
}

func unknownProjectError(ns, name string, nsProjects []discovery.Project) error {
	if len(nsProjects) == 0 {
		return fmt.Errorf("unknown project %q in namespace %q (no projects in this namespace)", name, ns)
	}
	return fmt.Errorf("unknown project %q in namespace %q (projects: %s)",
		name, ns, strings.Join(projectNames(nsProjects), ", "))
}

// listTargets prints the target names a project's type declares.
func (rc *repoContext) listTargets(cmd *cobra.Command, p discovery.Project) error {
	t, err := rc.typeFor(p)
	if err != nil {
		return err
	}
	targets := make([]string, 0, len(t.Targets))
	for name := range t.Targets {
		targets = append(targets, name)
	}
	sort.Strings(targets)
	out := stdout(cmd)
	for _, name := range targets {
		fmt.Fprintln(out, name)
	}
	return nil
}

// runSingle runs one (project, target) pair. An unknown target is a hard
// error listing what the type actually declares.
func (rc *repoContext) runSingle(cmd *cobra.Command, p discovery.Project, target string) error {
	t, err := rc.typeFor(p)
	if err != nil {
		return err
	}
	if _, ok := t.Targets[target]; !ok {
		return fmt.Errorf("project %q (type %s@%d) has no target %q (targets: %s)",
			p.Manifest.Name, t.Name, t.Version, target, strings.Join(sortedKeys(t.Targets), ", "))
	}
	warnParallel(cmd)
	plan, err := targetrun.Build(rc.projects, rc.typeFor, []targetrun.Node{{Project: p.Manifest.Name, Target: target}}, rc.repoRoot)
	if err != nil {
		return err
	}
	return plan.Execute(context.Background(), nsExecRunner, rc.repoRoot, stdout(cmd))
}

// runTarget runs target across a scope of projects. Projects whose type does
// not declare the target are skipped and reported; if nothing can run, that
// is an error.
func (rc *repoContext) runTarget(cmd *cobra.Command, target string, projects []discovery.Project, scope, ns string) error {
	warnParallel(cmd)

	var roots []targetrun.Node
	skipped := 0
	for _, p := range projects {
		t, err := rc.typeFor(p)
		if err != nil {
			return err
		}
		if _, ok := t.Targets[target]; ok {
			roots = append(roots, targetrun.Node{Project: p.Manifest.Name, Target: target})
		} else {
			skipped++
		}
	}

	if len(roots) == 0 {
		hint := ""
		if scope != "this repository" {
			if _, isProject := projectByName(projects, target); isProject {
				hint = fmt.Sprintf(" (there is a project named %q; try \"smith %s %s list\" to see its targets)", target, ns, target)
			}
		}
		return fmt.Errorf("no project in %s has a %q target%s", scope, target, hint)
	}

	plan, err := targetrun.Build(rc.projects, rc.typeFor, roots, rc.repoRoot)
	if err != nil {
		return err
	}
	if err := plan.Execute(context.Background(), nsExecRunner, rc.repoRoot, stdout(cmd)); err != nil {
		return err
	}
	if skipped > 0 {
		fmt.Fprintf(stdout(cmd), "ran %d, skipped %d (type has no '%s' target)\n", len(roots), skipped, target)
	}
	return nil
}

// warnParallel notes that --parallel is accepted but execution is serial.
func warnParallel(cmd *cobra.Command) {
	if n, err := cmd.Flags().GetInt("parallel"); err == nil && n > 1 {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: parallel execution not implemented yet; running serially")
	}
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
