// Package config loads and saves .smith/repo.yml, the Smith repository
// configuration.
//
// Load keeps the parsed yaml.Node tree alongside the typed view so that Save
// can re-emit the file with the original comments intact: the typed struct is
// re-encoded to a fresh tree and comments are merged back into it from the
// loaded tree. Configs constructed from scratch (no Load) save as plain YAML.
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cliwright/smith/internal/typeref"
	"github.com/cliwright/smith/internal/validate"
)

// RepoConfig is the typed view of .smith/repo.yml.
type RepoConfig struct {
	Version    int        `json:"version" yaml:"version"`
	Name       string     `json:"name" yaml:"name"`
	Registries []Registry `json:"registries" yaml:"registries"`
	Tools      []string   `json:"tools,omitempty" yaml:"tools,omitempty"`
	Workspace  Workspace  `json:"workspace" yaml:"workspace"`
	// RepoTargets are repo-level targets ("attachments"): named aliases bound
	// to repo/<flavor>/<kind>@<version> types, run via `smith repo <name>
	// <target>`. The alias is the grammar keyword.
	RepoTargets map[string]RepoTarget `json:"repo_targets,omitempty" yaml:"repo_targets,omitempty"`

	node *yaml.Node
}

// RepoTarget is one attachment: a type ref plus optional params and
// environment, both shaped like their manifest counterparts.
type RepoTarget struct {
	Type        string            `json:"type" yaml:"type"`
	Params      map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
}

// Registry is one registry source pair, in priority order: the first
// registry containing a type or template wins.
type Registry struct {
	Name      string `json:"name" yaml:"name"`
	Types     Source `json:"types" yaml:"types"`
	Templates Source `json:"templates" yaml:"templates"`
}

// Source locates a registry: a git URL, or a local path (dev only).
type Source struct {
	Git  string `json:"git,omitempty" yaml:"git,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// Workspace declares where smith looks for projects.
type Workspace struct {
	ProjectRoots []string `json:"project_roots" yaml:"project_roots"`
}

// Load reads path, parses it, validates it against the embedded repo-config
// schema and returns the typed config. The parsed document is retained so a
// later Save preserves comments.
func Load(path string) (*RepoConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := validateNode(path, &node); err != nil {
		return nil, err
	}
	var cfg RepoConfig
	if err := node.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if err := validateRepoTargets(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.node = &node
	return &cfg, nil
}

// validateRepoTargets enforces the attachment constraint at load:
// repo_targets entries must bind repo/<flavor>/<kind>@<version> types. The
// schema pattern already rejects anything else; this is the loud,
// human-readable backstop.
func validateRepoTargets(cfg *RepoConfig) error {
	for alias, target := range cfg.RepoTargets {
		ref, err := typeref.Parse(target.Type)
		if err != nil || !strings.HasPrefix(ref.Name, "repo/") {
			return fmt.Errorf("repo_targets entries must use repo/<flavor>/<kind>@<version> types, got %q (alias %q)", target.Type, alias)
		}
	}
	return nil
}

// Save renders cfg with Marshal and writes it to path.
func Save(path string, cfg *RepoConfig) error {
	data, err := Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Marshal renders cfg as YAML. If cfg came from Load, comments from the
// original file are carried over; fields added since Load have none.
func Marshal(cfg *RepoConfig) ([]byte, error) {
	doc, err := encodeDocument(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.node != nil {
		mergeComments(doc, cfg.node)
	}
	return render(doc)
}

// encodeDocument marshals v into a yaml document node.
func encodeDocument(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding repo config: %w", err)
	}
	if n.Kind == yaml.DocumentNode {
		return &n, nil
	}
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&n}}, nil
}

// render serializes a document node as YAML with two-space indentation.
func render(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("encoding repo config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding repo config: %w", err)
	}
	return buf.Bytes(), nil
}

// validateNode decodes the document into a generic value and validates it
// against the repo-config schema, attributing failures to path.
func validateNode(path string, node *yaml.Node) error {
	var doc any
	if err := node.Decode(&doc); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	if err := validate.Validate(validate.RepoConfig, doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// mergeComments copies comments from src onto dst for every node that exists
// in both trees. Nodes only in dst (new fields) get no comments; nodes only
// in src (removed fields) disappear with their comments.
func mergeComments(dst, src *yaml.Node) {
	if dst == nil || src == nil {
		return
	}
	dst.HeadComment = src.HeadComment
	dst.LineComment = src.LineComment
	dst.FootComment = src.FootComment
	if dst.Kind != src.Kind {
		return
	}
	switch dst.Kind {
	case yaml.DocumentNode:
		mergeComments(dst.Content[0], src.Content[0])
	case yaml.MappingNode:
		for i := 0; i+1 < len(dst.Content); i += 2 {
			srcKey, srcVal := mappingEntry(src, dst.Content[i].Value)
			if srcKey == nil {
				continue
			}
			mergeComments(dst.Content[i], srcKey)
			mergeComments(dst.Content[i+1], srcVal)
		}
	case yaml.SequenceNode:
		for i := 0; i < len(dst.Content) && i < len(src.Content); i++ {
			mergeComments(dst.Content[i], src.Content[i])
		}
	}
}

// mappingEntry returns the key and value nodes of the entry named key in the
// mapping n, or nil if there is no such entry.
func mappingEntry(n *yaml.Node, key string) (k, v *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}
