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
	Targets      map[string][]string `json:"targets" yaml:"targets"`
	DependsOn    map[string][]string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Params this type declares; steps and environment values are Go
	// templates over the merged params ({{.name}}).
	Params      map[string]Param  `json:"params,omitempty" yaml:"params,omitempty"`
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
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

// Reserved template vars, injected into the dry-render context so type
// steps and environment values referencing them pass load-time validation.
// The values are obviously-not-real-paths sentinels; real values are bound
// per project at execution time.
var dryRenderComputedVars = map[string]string{
	"SMITH_REPO_ROOT":   "<smith-repo-root>",
	"SMITH_PROJECT":     "<smith-project>",
	"SMITH_PROJECT_DIR": "<smith-project-dir>",
}

// validateTemplates dry-renders every step and environment value against the
// declared defaults plus the reserved computed vars, so a broken template
// fails at load (sync) time instead of mid-run.
func (pt *ProjectType) validateTemplates() error {
	context := pt.Defaults()
	for key, value := range dryRenderComputedVars {
		context[key] = value
	}
	for name, steps := range pt.Targets {
		for i, step := range steps {
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
