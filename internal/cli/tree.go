package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/discovery"
	"github.com/cliwright/smith/internal/manifest"
)

func newTreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tree",
		Short: "Print the discovered project tree",
		Long: `Print the discovered project tree: every smith.yml found by
traversing the declared workspace.project_roots, projects annotated with
(type), or (type -> deps) when they have dependencies. Output is sorted
alphabetically.

With --show-deps, a Dependencies section after the tree spells out every
depends_on entry with its explicit targets, including defaults (a sugar
dependency renders as "build → build").`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTree(cmd)
		},
	}
	cmd.Flags().Bool("show-deps", false,
		"after the tree, list every depends_on entry with its explicit targets (including defaults)")
	return cmd
}

func runTree(cmd *cobra.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith tree: %w", err)
	}
	repoRoot, err := discovery.FindRepoRoot(cwd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(filepath.Join(repoRoot, ".smith", "repo.yml"))
	if err != nil {
		return err
	}
	projects, err := discovery.FindProjects(repoRoot, cfg)
	if err != nil {
		return err
	}
	if err := renderTree(stdout(cmd), filepath.Base(repoRoot), cfg, projects); err != nil {
		return err
	}
	showDeps, err := cmd.Flags().GetBool("show-deps")
	if err != nil {
		return err
	}
	if showDeps {
		renderDeps(stdout(cmd), projects)
	}
	return nil
}

// treeNode is one directory in the rendered tree. project is set when the
// directory holds a smith.yml. synthetic marks the repo node that carries
// repo-level targets (attachments): it sorts after every real root.
type treeNode struct {
	name      string
	project   *discovery.Project
	synthetic bool
	children  map[string]*treeNode
}

func newTreeNode(name string) *treeNode {
	return &treeNode{name: name, children: map[string]*treeNode{}}
}

// renderTree prints the repo tree: the repo directory name, then the
// hierarchy of declared roots and projects, projects annotated as
// "name · type". Children are sorted; sibling annotations align.
func renderTree(w io.Writer, repoName string, cfg *config.RepoConfig, projects []discovery.Project) error {
	root := newTreeNode(repoName)
	getChild := func(parent *treeNode, name string) *treeNode {
		if child, ok := parent.children[name]; ok {
			return child
		}
		child := newTreeNode(name)
		parent.children[name] = child
		return child
	}
	insert := func(path string) *treeNode {
		node := root
		for _, segment := range strings.Split(path, "/") {
			node = getChild(node, segment)
		}
		return node
	}

	for _, projectRoot := range cfg.Workspace.ProjectRoots {
		insert(filepath.ToSlash(projectRoot))
	}
	for i := range projects {
		node := insert(projects[i].Dir)
		node.project = &projects[i]
	}

	// Synthetic repo node for repo-level targets (attachments), after the
	// directory roots; omitted when repo_targets is empty.
	if len(cfg.RepoTargets) > 0 {
		repoNode := insert("repo")
		repoNode.synthetic = true
		for alias, rt := range cfg.RepoTargets {
			child := getChild(repoNode, alias)
			child.project = &discovery.Project{
				Dir: ".",
				Manifest: &manifest.Manifest{
					Name: alias, Type: rt.Type,
					Params: rt.Params, Environment: rt.Environment,
				},
			}
		}
	}

	fmt.Fprintln(w, repoName)
	renderChildren(w, root, "")
	return nil
}

func renderChildren(w io.Writer, parent *treeNode, prefix string) {
	kids := make([]*treeNode, 0, len(parent.children))
	for _, child := range parent.children {
		kids = append(kids, child)
	}
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].synthetic != kids[j].synthetic {
			return !kids[i].synthetic // synthetic (repo) node sorts last
		}
		return kids[i].name < kids[j].name
	})

	maxLen := 0
	for _, child := range kids {
		if len(child.name) > maxLen {
			maxLen = len(child.name)
		}
	}

	for i, child := range kids {
		last := i == len(kids)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if last {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		line := prefix + connector + child.name
		if child.project != nil {
			annotation := annotation(child.project.Manifest)
			line += strings.Repeat(" ", maxLen-len(child.name)+3) + annotation
		}
		fmt.Fprintln(w, line)
		renderChildren(w, child, childPrefix)
	}
}

// annotation renders a project's parenthesized type annotation, with a
// sorted, deduplicated "-> a, b" dependency suffix inside the parens when
// the project has dependencies. The annotation uses the ASCII arrow; the
// --show-deps footer keeps its own arrow.
func annotation(m *manifest.Manifest) string {
	inner := m.Type
	if deps := depNames(m); len(deps) > 0 {
		inner += " -> " + strings.Join(deps, ", ")
	}
	return "(" + inner + ")"
}

// depNames returns the distinct dependency project names of a manifest,
// sorted for deterministic output.
func depNames(m *manifest.Manifest) []string {
	seen := map[string]bool{}
	for _, d := range m.DependsOn {
		seen[d.Project] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// depEdge is one depends_on entry rendered by renderDeps: project depends on
// dep, with dep's targets running before the given local targets.
type depEdge struct {
	project, dep, targets, before string
}

// renderDeps spells out every depends_on entry of every project, one line per
// entry — including defaults, so a sugar dependency renders as "build →
// build". Entries are sorted by project, dependency, and targets, and the
// section is omitted entirely when the repo has no dependencies. Unlike the
// tree annotation, duplicate edges are NOT merged: --show-deps exists to show
// them distinctly.
func renderDeps(w io.Writer, projects []discovery.Project) {
	var edges []depEdge
	for _, p := range projects {
		for _, d := range p.Manifest.DependsOn {
			edges = append(edges, depEdge{
				project: p.Manifest.Name,
				dep:     d.Project,
				targets: strings.Join(d.Targets, ", "),
				before:  strings.Join(d.Before, ", "),
			})
		}
	}
	if len(edges) == 0 {
		return
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].project != edges[j].project {
			return edges[i].project < edges[j].project
		}
		if edges[i].dep != edges[j].dep {
			return edges[i].dep < edges[j].dep
		}
		if edges[i].targets != edges[j].targets {
			return edges[i].targets < edges[j].targets
		}
		return edges[i].before < edges[j].before
	})

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Dependencies:")
	for _, e := range edges {
		fmt.Fprintf(w, "  %s → %s   %s → %s\n", e.project, e.dep, e.targets, e.before)
	}
}
