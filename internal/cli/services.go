package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/service"
	"github.com/cliwright/smith/internal/targetrun"
)

// servicesRootName is the reserved project root that holds service files:
// arbitrary-named *.yml directly inside it declare services. The grammar
// keyword is the plural ("services") and shadows the directory namespace of
// the same name, exactly like the reserved "repo" namespace.
const servicesRootName = "services"

// dispatchServices handles "smith services ..." — the reserved namespace of
// named service compositions:
//
//	smith services                       list services (same as "services list")
//	smith services list                  list services
//	smith services <name>                show components and implemented targets
//	smith services <name> graph          component dependency view (cycle-checked)
//	smith services <name> graph <target> print the target's execution order, run nothing
//	smith services <name> <target>       run target across the implementing components
//
// A service run resolves which components implement the target (mapping
// first, name match as fallback) and feeds those (project, target) pairs as
// roots into the ordinary targetrun closure — ordering comes from
// project-level depends_on, cycles and dangling targets are hard errors, and
// components without the target are skipped and reported.
func (rc *repoContext) dispatchServices(cmd *cobra.Command, rest []string) error {
	out := stdout(cmd)

	if len(rest) == 0 || (rest[0] == "list" && len(rest) == 1) {
		if len(rc.services) == 0 {
			fmt.Fprintf(out, "no services defined; add a *.yml to the repo's %s/ directory\n", servicesRootName)
			return nil
		}
		for _, s := range rc.services {
			fmt.Fprintln(out, s.Name)
		}
		return nil
	}
	if rest[0] == "list" {
		return fmt.Errorf("list takes no further arguments")
	}
	if len(rest) > 3 {
		return fmt.Errorf("too many arguments: %s", strings.Join(rest, " "))
	}

	svc, ok := rc.serviceByName(rest[0])
	if !ok {
		if len(rc.services) == 0 {
			return fmt.Errorf("unknown service %q (no services defined; add a *.yml to the repo's %s/ directory)",
				rest[0], servicesRootName)
		}
		return fmt.Errorf("unknown service %q (services: %s)",
			rest[0], strings.Join(rc.serviceNames(), ", "))
	}

	switch {
	case len(rest) == 1:
		return rc.showService(cmd, svc)
	case rest[1] == "graph":
		if len(rest) == 2 {
			return rc.graphService(cmd, svc)
		}
		return rc.graphServiceTarget(cmd, svc, rest[2])
	default:
		return rc.runService(cmd, svc, rest[1])
	}
}

func (rc *repoContext) serviceByName(name string) (*service.Service, bool) {
	for _, s := range rc.services {
		if s.Name == name {
			return s, true
		}
	}
	return nil, false
}

func (rc *repoContext) serviceNames() []string {
	names := make([]string, len(rc.services))
	for i, s := range rc.services {
		names[i] = s.Name
	}
	return names
}

// checkServiceTarget rejects targets outside the closed service-target
// vocabulary before any resolution happens — a typo dies here, not as a
// confusing "no component implements" message.
func checkServiceTarget(target string) error {
	for _, t := range service.StandardTargets {
		if t == target {
			return nil
		}
	}
	return fmt.Errorf("unknown service target %q (service targets: %s)",
		target, strings.Join(service.StandardTargets, ", "))
}

// serviceRoots resolves which (project, target) nodes implement service
// target st. A component whose targets map any component target onto st
// contributes the mapped component target(s); otherwise a component falls
// back to a name match against its type's targets. Components that do
// neither are returned separately for skip-and-report. Roots follow
// component declaration order, mapped targets sorted within a component.
func (rc *repoContext) serviceRoots(svc *service.Service, st string) (roots []targetrun.Node, skipped []string, err error) {
	for _, c := range svc.Components {
		p, ok := projectByName(rc.projects, c.Project)
		if !ok { // load-time validation guarantees this; stay loud anyway
			return nil, nil, fmt.Errorf("service %q lists unknown component %q", svc.Name, c.Project)
		}
		t, err := rc.typeFor(p)
		if err != nil {
			return nil, nil, err
		}
		var mapped []string
		for ct, svt := range c.Targets {
			if svt == st {
				mapped = append(mapped, ct)
			}
		}
		if len(mapped) > 0 {
			sort.Strings(mapped)
			for _, ct := range mapped {
				roots = append(roots, targetrun.Node{Project: c.Project, Target: ct})
			}
			continue
		}
		if _, ok := t.Targets[st]; ok {
			roots = append(roots, targetrun.Node{Project: c.Project, Target: st})
			continue
		}
		skipped = append(skipped, c.Project)
	}
	return roots, skipped, nil
}

// runService runs a service target: the implementing components' targets
// become roots of the ordinary closure graph, so project-level depends_on
// orders the run and a closure may legally leave the service for
// prerequisites.
func (rc *repoContext) runService(cmd *cobra.Command, svc *service.Service, target string) error {
	if err := checkServiceTarget(target); err != nil {
		return err
	}
	roots, skipped, err := rc.serviceRoots(svc, target)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return fmt.Errorf("no component in service %q implements target %q (targets: %s)",
			svc.Name, target, strings.Join(rc.implementedServiceTargets(svc), ", "))
	}
	warnParallel(cmd)
	plan, err := targetrun.Build(rc.projects, rc.typeFor, roots, rc.repoRoot, typesDir(rc.repoRoot))
	if err != nil {
		return err
	}
	if err := rc.executePlan(cmd, plan); err != nil {
		return err
	}
	if reported := skipReport(plan, skipped); len(reported) > 0 {
		fmt.Fprintf(stdout(cmd), "skipped %d component(s) without a %q target: %s\n",
			len(reported), target, strings.Join(reported, ", "))
	}
	return nil
}

// skipReport drops components that the plan pulled in as prerequisites —
// they ran something, so reporting them as skipped would be a lie. What
// remains are components the target genuinely never touched.
func skipReport(plan *targetrun.Plan, skipped []string) []string {
	involved := map[string]bool{}
	for _, n := range plan.Order() {
		involved[n.Project] = true
	}
	var out []string
	for _, name := range skipped {
		if !involved[name] {
			out = append(out, name)
		}
	}
	return out
}

// implementedServiceTargets is the union of service targets the components
// implement, in vocabulary order — the "available targets" listing for
// errors and the show view.
func (rc *repoContext) implementedServiceTargets(svc *service.Service) []string {
	impl := map[string]bool{}
	for _, c := range svc.Components {
		p, ok := projectByName(rc.projects, c.Project)
		if !ok {
			continue
		}
		t, err := rc.typeFor(p)
		if err != nil {
			continue
		}
		for _, st := range service.StandardTargets {
			if impl[st] {
				continue
			}
			if _, ok := t.Targets[st]; ok {
				impl[st] = true
				continue
			}
			for _, svt := range c.Targets {
				if svt == st {
					impl[st] = true
					break
				}
			}
		}
	}
	var out []string
	for _, st := range service.StandardTargets {
		if impl[st] {
			out = append(out, st)
		}
	}
	return out
}

// showService prints a service's components with their types and the
// service targets each component implements.
func (rc *repoContext) showService(cmd *cobra.Command, svc *service.Service) error {
	out := stdout(cmd)
	fmt.Fprintf(out, "service %s (%d components)\n\n", svc.Name, len(svc.Components))
	for _, c := range svc.Components {
		p, ok := projectByName(rc.projects, c.Project)
		if !ok {
			return fmt.Errorf("service %q lists unknown component %q", svc.Name, c.Project)
		}
		t, err := rc.typeFor(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s (%s@%d)\n", c.Project, t.Name, t.Version)
		impl := componentTargetsDisplay(t, c)
		if len(impl) == 0 {
			fmt.Fprintln(out, "  targets: (none — participates via dependencies only)")
			continue
		}
		fmt.Fprintf(out, "  targets: %s\n", strings.Join(impl, ", "))
	}
	return nil
}

// componentTargetsDisplay lists the service targets a component implements,
// in vocabulary order: a bare name for a name match (or an identity
// mapping), "svc (target: ct)" for a renamed mapping.
func componentTargetsDisplay(t *projecttype.ProjectType, c service.Component) []string {
	var out []string
	for _, st := range service.StandardTargets {
		if _, ok := t.Targets[st]; ok {
			out = append(out, st)
			continue
		}
		var cts []string
		for ct, svt := range c.Targets {
			if svt == st {
				cts = append(cts, ct)
			}
		}
		sort.Strings(cts)
		for _, ct := range cts {
			if ct == st {
				out = append(out, st)
			} else {
				out = append(out, fmt.Sprintf("%s (target: %s)", st, ct))
			}
		}
	}
	return out
}

// graphService prints the component dependency view: each component with
// the projects that must come before it (its manifest depends_on), marking
// dependencies outside the service as external. The reachable project-level
// dependency graph is cycle-checked — a cycle would break any run that
// touches it, so it is reported here instead of as a mid-run surprise.
func (rc *repoContext) graphService(cmd *cobra.Command, svc *service.Service) error {
	out := stdout(cmd)
	inService := map[string]bool{}
	for _, c := range svc.Components {
		inService[c.Project] = true
	}
	if err := rc.checkServiceCycles(svc); err != nil {
		return err
	}
	fmt.Fprintf(out, "service %s — component dependency graph\n\n", svc.Name)
	for _, c := range svc.Components {
		p, ok := projectByName(rc.projects, c.Project)
		if !ok {
			return fmt.Errorf("service %q lists unknown component %q", svc.Name, c.Project)
		}
		fmt.Fprintln(out, c.Project)
		var deps []string
		for _, d := range p.Manifest.DependsOn {
			marker := ""
			if !inService[d.Project] {
				marker = " (external)"
			}
			deps = append(deps, d.Project+marker)
		}
		if len(deps) == 0 {
			fmt.Fprintln(out, "  (no dependencies)")
			continue
		}
		fmt.Fprintf(out, "  after: %s\n", strings.Join(deps, ", "))
	}
	return nil
}

// checkServiceCycles runs a DFS over the manifest depends_on graph reachable
// from the service's components and reports the first cycle found,
// erroring with the project path (no targets — this is the project-level
// view; target-level cycles are caught per plan by targetrun.Build).
func (rc *repoContext) checkServiceCycles(svc *service.Service) error {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var visit func(name string) error
	visit = func(name string) error {
		switch color[name] {
		case black:
			return nil
		case grey:
			start := 0
			for i, s := range stack {
				if s == name {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), name)
			return fmt.Errorf("service %q has a dependency cycle: %s", svc.Name, strings.Join(cycle, " -> "))
		}
		color[name] = grey
		stack = append(stack, name)
		p, ok := projectByName(rc.projects, name)
		if !ok {
			return fmt.Errorf("service %q lists unknown component %q", svc.Name, name)
		}
		for _, d := range p.Manifest.DependsOn {
			if err := visit(d.Project); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return nil
	}
	for _, c := range svc.Components {
		if err := visit(c.Project); err != nil {
			return err
		}
	}
	return nil
}

// graphServiceTarget prints the exact execution order a service target run
// would use — the same root resolution and closure Build as the run, but
// the plan is printed, never executed. Target-level dependency cycles
// surface here as the same hard error the run would hit.
func (rc *repoContext) graphServiceTarget(cmd *cobra.Command, svc *service.Service, target string) error {
	if err := checkServiceTarget(target); err != nil {
		return err
	}
	roots, skipped, err := rc.serviceRoots(svc, target)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return fmt.Errorf("no component in service %q implements target %q (targets: %s)",
			svc.Name, target, strings.Join(rc.implementedServiceTargets(svc), ", "))
	}
	plan, err := targetrun.Build(rc.projects, rc.typeFor, roots, rc.repoRoot, typesDir(rc.repoRoot))
	if err != nil {
		return err
	}
	out := stdout(cmd)
	order := plan.Order()
	fmt.Fprintf(out, "service %s — %q execution order (%d nodes)\n\n", svc.Name, target, len(order))
	for i, n := range order {
		fmt.Fprintf(out, "%d. %s\n", i+1, n)
	}
	if reported := skipReport(plan, skipped); len(reported) > 0 {
		fmt.Fprintf(out, "\nskipped (no %q target): %s\n", target, strings.Join(reported, ", "))
	}
	return nil
}
