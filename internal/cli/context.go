package cli

// --context: print the resolved, annotated effective environment for an
// addressed scope instead of running anything. It shares the run's
// resolution machinery — selector resolution, lock-verified types, param
// merging, dotenv loading, the overlay error policy — so what it prints is
// what a step would see. The os environment layer is deliberately excluded:
// `env` shows that, and --context is about what smith contributes.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/dotenv"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/targetrun"
)

// envEntry is one annotated line of --context output: a KEY=VALUE pair and
// the layer whose value wins.
type envEntry struct {
	kv     string
	source string
}

// resolveEnvOrigin determines the environment selector and where it came
// from ("--env", "$SMITH_ENV", or "repo.yml smith_env.default"). resolveEnv
// wraps it for callers that only need the value.
func (rc *repoContext) resolveEnvOrigin(cmd *cobra.Command) (value, origin string, err error) {
	if flag, _ := cmd.Flags().GetString("env"); flag != "" {
		if !dotenv.ValidName(flag) {
			return "", "", fmt.Errorf("--env %q is not %s", flag, dotenv.NamePatternDesc)
		}
		return flag, "--env", nil
	}
	if v := os.Getenv("SMITH_ENV"); v != "" {
		if !dotenv.ValidName(v) {
			return "", "", fmt.Errorf("$SMITH_ENV=%q is not %s (fix or unset it, or pass --env)", v, dotenv.NamePatternDesc)
		}
		return v, "$SMITH_ENV", nil
	}
	if d := rc.cfg.SmithEnv.Default; d != "" {
		return d, "repo.yml smith_env.default", nil
	}
	return "", "", nil
}

// dispatchContext routes `smith --context ...`: print the resolved
// environment for the addressed scope instead of running anything. It
// shares resolvePositional with the run path, so namespace-free project
// addressing and the precedence rules (PROJECT WINS for 2+ args; 1-arg
// namespace → repo-wide target → bare project) behave exactly as execution;
// only the rendered view differs — a bare project name prints its
// environment where the run path would list its targets.
func (rc *repoContext) dispatchContext(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		// Bare `smith --context` — the repo-level view.
		return rc.printRepoContext(cmd)
	}
	if args[0] == "repo" {
		return rc.dispatchRepoContext(cmd, args[1:])
	}
	route, err := rc.resolvePositional(args)
	if err != nil {
		return err
	}
	switch route.kind {
	case routeNamespace:
		return rc.dispatchNamespaceContext(cmd, route.ns, route.rest)
	case routeBareProject:
		return rc.printProjectContext(cmd, route.project, "", false)
	case routeBareTarget:
		return rc.printTargetContext(cmd, route.rest[0], rc.projects, "this repository")
	default:
		if len(args) == 1 {
			// An undeclared bare name renders the repo-wide target
			// view's error — the same one a run reports.
			return rc.printTargetContext(cmd, args[0], rc.projects, "this repository")
		}
		return fmt.Errorf("--context: unexpected arguments %q", strings.Join(args, " "))
	}
}

// printRepoContext is the bare `smith --context` view: the resolved selector
// and the repo-level dotenv layers, listed literally (keys may repeat across
// base and overlay). No overlay-exists policy here: a project-level file may
// still satisfy a run — this view shows what the repo level actually holds.
func (rc *repoContext) printRepoContext(cmd *cobra.Command) error {
	out := stdout(cmd)
	env, origin, err := rc.resolveEnvOrigin(cmd)
	if err != nil {
		return err
	}
	var entries []envEntry
	if env != "" {
		entries = append(entries, envEntry{"SMITH_ENV=" + env, "selector (" + origin + ")"})
	}
	if rc.cfg.Dotenv {
		absRoot, err := filepath.Abs(rc.repoRoot)
		if err != nil {
			return fmt.Errorf("resolving repo root: %w", err)
		}
		layers, err := dotenv.LoadStack(absRoot, env, ".smith/repo.yml")
		if err != nil {
			return err
		}
		for _, layer := range layers {
			source := relPath(absRoot, layer.Path)
			for _, key := range sortedEnvKeys(layer.Vars) {
				entries = append(entries, envEntry{key + "=" + layer.Vars[key], source})
			}
		}
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "# no environment selected and repo dotenv is not enabled (set dotenv: true in .smith/repo.yml)")
		return nil
	}
	printEntries(out, entries)
	return nil
}

// dispatchNamespaceContext handles "smith <ns> ... --context": a project's
// resolved environment, or — target-wins, mirroring execution — every
// project in the namespace declaring the named target.
func (rc *repoContext) dispatchNamespaceContext(cmd *cobra.Command, ns string, rest []string) error {
	nsProjects := rc.projectsIn(ns)
	if len(rest) == 0 || rest[0] == "list" {
		return fmt.Errorf("smith %s --context needs a project or a target (e.g. \"smith %s <name> --context\")", ns, ns)
	}
	if len(rest) > 2 {
		return fmt.Errorf("too many arguments: %s", strings.Join(rest, " "))
	}
	if p, isProject := projectByName(nsProjects, rest[0]); isProject {
		target := ""
		if len(rest) == 2 {
			rt, err := rc.typeFor(p)
			if err != nil {
				return err
			}
			if _, ok := rt.ByTarget[rest[1]]; !ok {
				return fmt.Errorf("project %q (%s) has no target %q (targets: %s)",
					p.Manifest.Name, rt.TypeLabel(), rest[1], strings.Join(sortedKeys(rt.ByTarget), ", "))
			}
			target = rest[1]
		}
		return rc.printProjectContext(cmd, p, target, false)
	}
	if len(rest) == 1 {
		return rc.printTargetContext(cmd, rest[0], nsProjects, fmt.Sprintf("namespace %q", ns))
	}
	return unknownProjectError(ns, rest[0], nsProjects)
}

// dispatchRepoContext handles "smith repo <name> [--context]": the
// attachment's resolved environment (repo-level dotenv applies; attachments
// have no project-level dotenv of their own).
func (rc *repoContext) dispatchRepoContext(cmd *cobra.Command, rest []string) error {
	attachments, err := rc.attachments()
	if err != nil {
		return err
	}
	if len(rest) == 0 || rest[0] == "list" {
		return fmt.Errorf("smith repo --context needs an attachment (e.g. \"smith repo <name> --context\")")
	}
	if len(rest) > 2 {
		return fmt.Errorf("too many arguments: %s", strings.Join(rest, " "))
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
	target := ""
	if len(rest) == 2 {
		rt, err := rc.typeFor(att)
		if err != nil {
			return err
		}
		if _, ok := rt.ByTarget[rest[1]]; !ok {
			return fmt.Errorf("repo target %q (%s) has no target %q (targets: %s)",
				att.Manifest.Name, rt.TypeLabel(), rest[1], strings.Join(sortedKeys(rt.ByTarget), ", "))
		}
		target = rest[1]
	}
	return rc.printProjectContext(cmd, att, target, false)
}

// printTargetContext prints the context of every project in scope that
// declares the target.
func (rc *repoContext) printTargetContext(cmd *cobra.Command, target string, projects []discovery.Project, scope string) error {
	var declared []discovery.Project
	for _, p := range projects {
		rt, err := rc.typeFor(p)
		if err != nil {
			return err
		}
		if _, ok := rt.ByTarget[target]; ok {
			declared = append(declared, p)
		}
	}
	if len(declared) == 0 {
		return fmt.Errorf("no project in %s has a %q target", scope, target)
	}
	for i, p := range declared {
		if i > 0 {
			fmt.Fprintln(stdout(cmd))
		}
		if err := rc.printProjectContext(cmd, p, target, true); err != nil {
			return err
		}
	}
	return nil
}

// printProjectContext prints one project's annotated effective environment;
// with header, prefixed by "# <name> (<refs>)" — every declared type ref,
// comma-joined — for multi-project views.
func (rc *repoContext) printProjectContext(cmd *cobra.Command, p discovery.Project, target string, header bool) error {
	entries, err := rc.projectEnvEntries(cmd, p, target)
	if err != nil {
		return err
	}
	out := stdout(cmd)
	if header {
		fmt.Fprintf(out, "# %s (%s)\n", p.Manifest.Name, p.Manifest.Types.String())
	}
	printEntries(out, entries)
	return nil
}

// projectEnvEntries assembles the annotated effective environment for one
// project: selector, dotenv layers (repo then project), each type's
// environment in declaration order (rendered with SMITH_TYPE_DIR bound to
// that type's own installed dir), and the manifest environment. Later layers
// overwrite earlier ones, and each key carries the source of its winning
// value. Attachment-aware: synthetic repo-dir projects skip the project
// dotenv level. The dotenv error policy matches a real run, including the
// no-overlay-anywhere error.
func (rc *repoContext) projectEnvEntries(cmd *cobra.Command, p discovery.Project, target string) ([]envEntry, error) {
	env, origin, err := rc.resolveEnvOrigin(cmd)
	if err != nil {
		return nil, err
	}
	rt, err := rc.typeFor(p)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(rc.repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving repo root: %w", err)
	}

	mergedParams, err := targetrun.ResolveParams(absRoot, p, rt, rc.cliArgs)
	if err != nil {
		return nil, err
	}
	// The rendering context for type/manifest environment values mirrors a
	// run: merged params plus the computed vars, with SMITH_TYPE_DIR bound
	// per type — the OWNING type's dir for a per-target view.
	contextFor := func(t *projecttype.ProjectType) map[string]string {
		context := make(map[string]string, len(mergedParams)+len(targetrun.ReservedTemplateVars))
		for key, value := range mergedParams {
			context[key] = value
		}
		for key, value := range targetrun.ComputedVars(absRoot, p.Dir, p.Manifest.Name, rt.Dirs[refOf(t)], rc.cliArgs) {
			context[key] = value
		}
		return context
	}
	targetOwner := (*projecttype.ProjectType)(nil)
	if target != "" {
		targetOwner = rt.ByTarget[target] // existence checked by the caller
	}
	renderEnv := func(vars map[string]string, context map[string]string) (map[string]string, error) {
		out := make(map[string]string, len(vars))
		for _, key := range sortedEnvKeys(vars) {
			rendered, err := projecttype.RenderTemplate(vars[key], context)
			if err != nil {
				return nil, fmt.Errorf("rendering %q: %w", key, err)
			}
			out[key] = rendered
		}
		return out, nil
	}

	effective := map[string]envEntry{}
	set := func(key, value, source string) {
		effective[key] = envEntry{kv: key + "=" + value, source: source}
	}

	if env != "" {
		set("SMITH_ENV", env, "selector ("+origin+")")
	}

	projEnabled := p.Manifest.Dotenv && p.Dir != "."
	overlays := 0
	if rc.cfg.Dotenv {
		layers, err := dotenv.LoadStack(absRoot, env, ".smith/repo.yml")
		if err != nil {
			return nil, err
		}
		overlays += dotenv.Overlays(layers)
		for _, layer := range layers {
			source := relPath(absRoot, layer.Path)
			for key, value := range layer.Vars {
				set(key, value, source)
			}
		}
	}
	if projEnabled {
		dir := filepath.Join(absRoot, filepath.FromSlash(p.Dir))
		layers, err := dotenv.LoadStack(dir, env, fmt.Sprintf("project %q (smith.yml)", p.Manifest.Name))
		if err != nil {
			return nil, err
		}
		overlays += dotenv.Overlays(layers)
		for _, layer := range layers {
			source := relPath(absRoot, layer.Path)
			for key, value := range layer.Vars {
				set(key, value, source)
			}
		}
	}
	if env != "" && (rc.cfg.Dotenv || projEnabled) && overlays == 0 {
		return nil, dotenv.NoOverlayError(env)
	}

	for _, t := range rt.Types {
		typeEnv, err := renderEnv(t.Environment, contextFor(t))
		if err != nil {
			return nil, fmt.Errorf("type %s environment: %w", refOf(t), err)
		}
		typeSource := fmt.Sprintf("type %s environment", refOf(t))
		for _, key := range sortedEnvKeys(typeEnv) {
			set(key, typeEnv[key], typeSource)
		}
	}
	manifestContext := contextFor(rt.Types[0])
	if targetOwner != nil {
		manifestContext = contextFor(targetOwner)
	}
	manifestEnv, err := renderEnv(p.Manifest.Environment, manifestContext)
	if err != nil {
		return nil, fmt.Errorf("manifest environment: %w", err)
	}
	for _, key := range sortedEnvKeys(manifestEnv) {
		set(key, manifestEnv[key], "manifest environment")
	}

	set("SMITH_REPO_ROOT", absRoot, "computed")
	set("SMITH_PROJECT", p.Manifest.Name, "computed")
	if target != "" {
		set("SMITH_TARGET", target, "computed")
	} else {
		set("SMITH_TARGET", "<per target>", "computed (set per run)")
	}
	set("SMITH_CLI_ARGS", rc.cliArgs, "computed (-- passthrough)")

	keys := make([]string, 0, len(effective))
	for key := range effective {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]envEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, effective[key])
	}
	return entries, nil
}

// printEntries writes entries as aligned, source-annotated KEY=VALUE lines.
func printEntries(out io.Writer, entries []envEntry) {
	width := 0
	for _, e := range entries {
		if len(e.kv) > width {
			width = len(e.kv)
		}
	}
	for _, e := range entries {
		fmt.Fprintf(out, "%-*s  # %s\n", width, e.kv, e.source)
	}
}

// relPath formats path relative to root when possible (dotenv display
// labels), falling back to the absolute path.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

func sortedEnvKeys(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
