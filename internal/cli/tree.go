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
)

func newTreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tree",
		Short: "Print the discovered project tree",
		Long: `Print the discovered project tree: every smith.yml found by
traversing the declared workspace.project_roots, annotated with project name
and type. Output is sorted alphabetically.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTree(cmd)
		},
	}
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
	return renderTree(cmd.OutOrStdout(), filepath.Base(repoRoot), cfg, projects)
}

// treeNode is one directory in the rendered tree. project is set when the
// directory holds a smith.yml.
type treeNode struct {
	name     string
	project  *discovery.Project
	children map[string]*treeNode
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

	fmt.Fprintln(w, repoName)
	renderChildren(w, root, "")
	return nil
}

func renderChildren(w io.Writer, parent *treeNode, prefix string) {
	kids := make([]*treeNode, 0, len(parent.children))
	for _, child := range parent.children {
		kids = append(kids, child)
	}
	sort.Slice(kids, func(i, j int) bool { return kids[i].name < kids[j].name })

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
			annotation := fmt.Sprintf("(%s · %s)", child.project.Manifest.Name, child.project.Manifest.Type)
			line += strings.Repeat(" ", maxLen-len(child.name)+3) + annotation
		}
		fmt.Fprintln(w, line)
		renderChildren(w, child, childPrefix)
	}
}
