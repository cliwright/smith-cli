// Package projecttype loads and saves project-type.json, a versioned
// project-type definition from a registry. These files are plain JSON:
// encoding/json in, schema validation, encoding/json out.
package projecttype

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/template"

	"github.com/cliwright/smith/internal/validate"
)

// ProjectType is the typed view of project-type.json.
type ProjectType struct {
	Name         string              `json:"name" yaml:"name"`
	Version      int                 `json:"version" yaml:"version"`
	Description  string              `json:"description" yaml:"description"`
	Capabilities []Capability        `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Tools        []string            `json:"tools" yaml:"tools"`
	WorkingDir   WorkingDir          `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	Targets      map[string]Target   `json:"targets" yaml:"targets"`
	DependsOn    map[string][]string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Params this type declares; steps and environment values are Go
	// templates over the merged params ({{.name}}).
	Params      map[string]Param  `json:"params,omitempty" yaml:"params,omitempty"`
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
}

// Target is one named target of a project type: the ordered shell steps that
// run for it, plus an optional short description for list views. In
// project-type.json a target is either a bare steps array (the common case)
// or an object {"description": ..., "steps": [...]}; both decode into this
// type, and MarshalJSON writes the bare form back when there is no
// description, keeping hand-written types slim.
type Target struct {
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Steps       []string `json:"steps" yaml:"steps"`
}

// UnmarshalJSON accepts both target forms: a bare array of steps, or an
// object with description and steps.
func (t *Target) UnmarshalJSON(data []byte) error {
	var bare []string
	if err := json.Unmarshal(data, &bare); err == nil {
		t.Steps = bare
		t.Description = ""
		return nil
	}
	var described struct {
		Description string   `json:"description"`
		Steps       []string `json:"steps"`
	}
	if err := json.Unmarshal(data, &described); err != nil {
		return fmt.Errorf("target must be a steps array or an object with description/steps: %w", err)
	}
	t.Description = described.Description
	t.Steps = described.Steps
	return nil
}

// MarshalJSON writes the bare steps array when the target carries no
// description, and the described object otherwise.
func (t Target) MarshalJSON() ([]byte, error) {
	if t.Description == "" {
		return json.Marshal(t.Steps)
	}
	return json.Marshal(struct {
		Description string   `json:"description,omitempty"`
		Steps       []string `json:"steps"`
	}{t.Description, t.Steps})
}

// Param is one declared type param: a required default and an optional
// description for type browsers.
type Param struct {
	Default     string `json:"default" yaml:"default"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Capability is what a project of this type can do, independent of how.
type Capability string

// The capability vocabulary of project-type.json.
const (
	CapabilityBuildable   Capability = "buildable"
	CapabilityTestable    Capability = "testable"
	CapabilityLintable    Capability = "lintable"
	CapabilityFormattable Capability = "formattable"
	CapabilityRunnable    Capability = "runnable"
	CapabilityPackageable Capability = "packageable"
	CapabilityValidatable Capability = "validatable"
	CapabilityPlanable    Capability = "planable"
	CapabilityApplicable  Capability = "applicable"
	CapabilityDeployable  Capability = "deployable"
)

// WorkingDir is where the type's targets execute.
type WorkingDir string

// The two legal working_dir values; project is the default.
const (
	WorkingDirProject WorkingDir = "project"
	WorkingDirRepo    WorkingDir = "repo"
)

// Load reads path, validates it against the embedded project-type schema
// and returns the typed definition.
func Load(path string) (*ProjectType, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := validate.Validate(validate.ProjectType, doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var pt ProjectType
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if pt.WorkingDir == "" {
		pt.WorkingDir = WorkingDirProject
	}
	if err := pt.validateTemplates(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &pt, nil
}

// RenderTemplate renders one Go text/template against a flat params map with
// missingkey=error: an undefined key (a typo'd {{.name}}) is an error, never
// a silent empty string.
func RenderTemplate(text string, params map[string]string) (string, error) {
	tmpl, err := template.New("smith").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Defaults returns the declared default params.
func (pt *ProjectType) Defaults() map[string]string {
	defaults := make(map[string]string, len(pt.Params))
	for key, param := range pt.Params {
		defaults[key] = param.Default
	}
	return defaults
}

// Compose validates that the given types can back one project together and
// returns the owner indexes: which type declares each target and each param.
// Types compose only when they are disjoint (their verbs): every conflict is
// a hard error —
//   - the same type name at two versions,
//   - a target, param, or environment key declared by more than one type,
//   - working_dir disagreement between types (the manifest working_dir knob
//     is the sanctioned per-project override; composed types must agree on
//     their own), and
//   - a type depends_on entry pointing at a target its own type does not
//     declare — cross-type wiring is not a thing; alternatives stay flavors.
//
// The caller names the project in errors; Compose's messages name the types
// and the offending key.
func Compose(types []*ProjectType) (byTarget, byParam map[string]*ProjectType, err error) {
	byTarget = make(map[string]*ProjectType)
	byParam = make(map[string]*ProjectType)
	seenName := map[string]*ProjectType{}
	for _, t := range types {
		label := fmt.Sprintf("%s@%d", t.Name, t.Version)
		if first, ok := seenName[t.Name]; ok {
			return nil, nil, fmt.Errorf("types %s@%d and %s are two versions of the same type; one project cannot list both",
				first.Name, first.Version, label)
		}
		seenName[t.Name] = t

		// An omitted working_dir means project (Load normalizes this; the
		// fallback keeps Compose honest for hand-built types).
		workingDir := t.WorkingDir
		if workingDir == "" {
			workingDir = WorkingDirProject
		}
		if first := types[0]; workingDir != normalizedWorkingDir(first) {
			return nil, nil, fmt.Errorf("types %s and %s disagree on working_dir (%q vs %q); composed types must agree — use the manifest working_dir knob to override where this project's steps execute",
				label, fmt.Sprintf("%s@%d", first.Name, first.Version), workingDir, normalizedWorkingDir(first))
		}

		for name := range t.Targets {
			if owner, ok := byTarget[name]; ok {
				return nil, nil, fmt.Errorf("types %s@%d and %s both declare target %q",
					owner.Name, owner.Version, label, name)
			}
			byTarget[name] = t
		}
		for key := range t.Params {
			if owner, ok := byParam[key]; ok {
				return nil, nil, fmt.Errorf("types %s@%d and %s both declare param %q",
					owner.Name, owner.Version, label, key)
			}
			byParam[key] = t
		}
		for key := range t.Environment {
			if owner := envOwner(types, key, t); owner != nil {
				return nil, nil, fmt.Errorf("types %s@%d and %s both set environment key %q",
					owner.Name, owner.Version, label, key)
			}
		}
		for target, deps := range t.DependsOn {
			for _, dep := range deps {
				if _, ok := t.Targets[dep]; !ok {
					return nil, nil, fmt.Errorf("type %s: target %q depends on %q, which the same type does not declare (depends_on must stay within one type)",
						label, target, dep)
				}
			}
		}
	}
	return byTarget, byParam, nil
}

// normalizedWorkingDir applies the type-defaulting rule to a working_dir
// value for comparison.
func normalizedWorkingDir(t *ProjectType) WorkingDir {
	if t.WorkingDir == "" {
		return WorkingDirProject
	}
	return t.WorkingDir
}

// envOwner returns the first type before t in types that sets key, or nil.
func envOwner(types []*ProjectType, key string, t *ProjectType) *ProjectType {
	for _, other := range types {
		if other == t {
			return nil
		}
		if _, ok := other.Environment[key]; ok {
			return other
		}
	}
	return nil
}

// Reserved template vars, injected into the dry-render context so type
// steps and environment values referencing them pass load-time validation.
// The values are obviously-not-real-paths sentinels; real values are bound
// per project at execution time.
var dryRenderComputedVars = map[string]string{
	"SMITH_REPO_ROOT":   "<smith-repo-root>",
	"SMITH_PROJECT":     "<smith-project>",
	"SMITH_PROJECT_DIR": "<smith-project-dir>",
	"SMITH_TYPE_DIR":    "<smith-type-dir>",
	"SMITH_CLI_ARGS":    "<smith-cli-args>",
}

// validateTemplates dry-renders every step and environment value against the
// declared defaults plus the reserved computed vars, so a broken template
// fails at load (sync) time instead of mid-run.
func (pt *ProjectType) validateTemplates() error {
	context := pt.Defaults()
	for key, value := range dryRenderComputedVars {
		context[key] = value
	}
	for name, target := range pt.Targets {
		for i, step := range target.Steps {
			if _, err := RenderTemplate(step, context); err != nil {
				return fmt.Errorf("type %s@%d: target %q step %d: %w", pt.Name, pt.Version, name, i+1, err)
			}
		}
	}
	keys := make([]string, 0, len(pt.Environment))
	for key := range pt.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := RenderTemplate(pt.Environment[key], context); err != nil {
			return fmt.Errorf("type %s@%d: environment %q: %w", pt.Name, pt.Version, key, err)
		}
	}
	return nil
}

// Marshal renders pt as indented JSON with a trailing newline.
func Marshal(pt *ProjectType) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pt); err != nil {
		return nil, fmt.Errorf("encoding project type: %w", err)
	}
	return buf.Bytes(), nil
}

// Save renders pt with Marshal and writes it to path.
func Save(path string, pt *ProjectType) error {
	data, err := Marshal(pt)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
