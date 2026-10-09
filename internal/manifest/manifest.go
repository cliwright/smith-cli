// Package manifest loads and saves smith.yml, the per-project manifest.
//
// Like package config it preserves comments across Load/Save via the
// yaml.Node tree. depends_on accepts two forms in one list: a bare project
// name (sugar: dep's "build" before this project's "build") or a mapping
// with project, targets and before keys (both default to [build]).
package manifest

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/cliwright/smith/internal/validate"
)

// defaultTarget is what sugar-form dependencies and omitted targets/before
// lists mean: the "build" target.
const defaultTarget = "build"

// Manifest is the typed view of smith.yml.
type Manifest struct {
	Version   int               `json:"version" yaml:"version"`
	Name      string            `json:"name" yaml:"name"`
	Type      string            `json:"type" yaml:"type"`
	DependsOn []Dependency      `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Params    map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
	// Environment extras for this project's target steps; values are Go
	// templates over the merged params.
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`

	node *yaml.Node
}

// Dependency is one entry of depends_on. The sugar string form and the
// explicit mapping form both decode into this single type; Targets and
// Before always hold at least one entry (defaulting to [build]).
type Dependency struct {
	Project string   `json:"project" yaml:"project"`
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
	Before  []string `json:"before,omitempty" yaml:"before,omitempty"`
}

// UnmarshalYAML accepts either the sugar scalar form ("- auth") or the
// explicit mapping form ("- project: auth\n  targets: [test]").
func (d *Dependency) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		if value.Tag != "!!str" {
			return fmt.Errorf("dependency must be a project name or a mapping, got %s", value.ShortTag())
		}
		d.Project = value.Value
		d.Targets = []string{defaultTarget}
		d.Before = []string{defaultTarget}
		return nil
	}
	var raw struct {
		Project string   `yaml:"project"`
		Targets []string `yaml:"targets,omitempty"`
		Before  []string `yaml:"before,omitempty"`
	}
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("dependency on %q: %w", raw.Project, err)
	}
	d.Project = raw.Project
	d.Targets = raw.Targets
	if len(d.Targets) == 0 {
		d.Targets = []string{defaultTarget}
	}
	d.Before = raw.Before
	if len(d.Before) == 0 {
		d.Before = []string{defaultTarget}
	}
	return nil
}

// MarshalYAML emits the sugar scalar form when the dependency carries the
// defaults, and the explicit mapping form otherwise.
func (d Dependency) MarshalYAML() (any, error) {
	if d.isSugar() {
		return d.Project, nil
	}
	return struct {
		Project string   `yaml:"project"`
		Targets []string `yaml:"targets,omitempty"`
		Before  []string `yaml:"before,omitempty"`
	}{
		Project: d.Project,
		Targets: d.Targets,
		Before:  d.Before,
	}, nil
}

// isSugar reports whether the dependency carries the defaults (a nil or
// [build] list means [build], per the schema), in which case the compact
// scalar form is used.
func (d Dependency) isSugar() bool {
	return isDefaultTargets(d.Targets) && isDefaultTargets(d.Before)
}

func isDefaultTargets(ts []string) bool {
	return len(ts) == 0 || (len(ts) == 1 && ts[0] == defaultTarget)
}

// Load reads path, parses it, validates it against the embedded
// project-manifest schema and returns the typed manifest. The parsed
// document is retained so a later Save preserves comments.
func Load(path string) (*Manifest, error) {
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
	var m Manifest
	if err := node.Decode(&m); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	m.node = &node
	return &m, nil
}

// Save renders m with Marshal and writes it to path.
func Save(path string, m *Manifest) error {
	data, err := Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Marshal renders m as YAML. If m came from Load, comments from the original
// file are carried over; fields added since Load have none.
func Marshal(m *Manifest) ([]byte, error) {
	doc, err := encodeDocument(m)
	if err != nil {
		return nil, err
	}
	if m.node != nil {
		mergeComments(doc, m.node)
	}
	return render(doc)
}

// encodeDocument marshals v into a yaml document node.
func encodeDocument(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding manifest: %w", err)
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
		return nil, fmt.Errorf("encoding manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// validateNode decodes the document into a generic value and validates it
// against the project-manifest schema, attributing failures to path.
func validateNode(path string, node *yaml.Node) error {
	var doc any
	if err := node.Decode(&doc); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	if err := validate.Validate(validate.ProjectManifest, doc); err != nil {
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
