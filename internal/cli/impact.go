package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/targetrun"
)

// impactClosure returns the seed plus its transitive dependents, ordered
// dependency-first: the seed, then each dependent after everything in the
// closure it (transitively) depends on. Reverse edges are the manifest
// depends_on graph inverted at load — no new declarations. The repo's
// graph is acyclic (discovery rejects cycles before any command runs), so
// the topological pass cannot stall; a stall is an internal error, not
// user input.
func impactClosure(projects []discovery.Project, seed discovery.Project) ([]discovery.Project, error) {
	byName := make(map[string]discovery.Project, len(projects))
	dependents := map[string][]string{} // dependency -> projects that directly depend on it
	for _, p := range projects {
		byName[p.Manifest.Name] = p
		seen := map[string]bool{}
		for _, dep := range p.Manifest.DependsOn {
			if !seen[dep.Project] {
				seen[dep.Project] = true
				dependents[dep.Project] = append(dependents[dep.Project], p.Manifest.Name)
			}
		}
	}

	// members: the seed plus every project reachable from it up the
	// reverse edges.
	members := map[string]bool{seed.Manifest.Name: true}
	queue := []string{seed.Manifest.Name}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, dependent := range dependents[name] {
			if !members[dependent] {
				members[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}

	// Kahn's algorithm over the subgraph induced by the members, edge
	// dependency -> dependent: the seed is the unique zero-indegree node
	// (every other member transitively depends on it), so it comes first.
	// The queue is kept sorted for a deterministic order.
	indegree := map[string]int{}
	edges := map[string][]string{}
	for name := range members {
		indegree[name] = 0
	}
	for name := range members {
		seen := map[string]bool{}
		for _, dep := range byName[name].Manifest.DependsOn {
			if members[dep.Project] && !seen[dep.Project] {
				seen[dep.Project] = true
				indegree[name]++
				edges[dep.Project] = append(edges[dep.Project], name)
			}
		}
	}
	var ready []string
	for name, n := range indegree {
		if n == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	var out []discovery.Project
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		out = append(out, byName[name])
		var freed []string
		for _, dependent := range edges[name] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				freed = append(freed, dependent)
			}
		}
		if len(freed) > 0 {
			ready = append(ready, freed...)
			sort.Strings(ready)
		}
	}
	if len(out) != len(members) {
		return nil, fmt.Errorf("internal: impact closure stalled: only %d of %d projects ordered (dependency cycle slipped past discovery?)", len(out), len(members))
	}
	return out, nil
}

// printImpact lists the seed's downstream closure, one project name per
// line, seed first. Inspection never blocks on trust or custody: no types
// are loaded.
func (rc *repoContext) printImpact(cmd *cobra.Command, seed discovery.Project) error {
	closure, err := impactClosure(rc.projects, seed)
	if err != nil {
		return err
	}
	out := stdout(cmd)
	for _, p := range closure {
		fmt.Fprintln(out, p.Manifest.Name)
	}
	return nil
}

// runImpact runs target over the seed's downstream closure as one ordinary
// plan: closure members whose composed types declare the target become
// roots (in closure order, so the seed runs first — Build's closure adds
// any cross-project prerequisites), the rest are skipped and reported. The
// plan funnels through executePlan like any other run, so the trust gate,
// dotenv preflight, and fail-fast all apply unchanged.
func (rc *repoContext) runImpact(cmd *cobra.Command, seed discovery.Project, target string) error {
	warnParallel(cmd)
	closure, err := impactClosure(rc.projects, seed)
	if err != nil {
		return err
	}

	var roots []targetrun.Node
	skipped := 0
	for _, p := range closure {
		rt, err := rc.typeFor(p)
		if err != nil {
			return err
		}
		if _, ok := rt.ByTarget[target]; ok {
			roots = append(roots, targetrun.Node{Project: p.Manifest.Name, Target: target})
		} else {
			skipped++
		}
	}

	if len(roots) == 0 {
		return fmt.Errorf("no project downstream of %q declares target %q", seed.Manifest.Name, target)
	}
	plan, err := targetrun.Build(rc.projects, rc.typeFor, roots, rc.repoRoot, rc.cliArgs)
	if err != nil {
		return err
	}
	if err := rc.executePlan(cmd, plan); err != nil {
		return err
	}
	if skipped > 0 {
		fmt.Fprintf(stdout(cmd), "ran %d, skipped %d (type has no '%s' target) downstream of %q\n",
			len(roots), skipped, target, seed.Manifest.Name)
	}
	return nil
}
