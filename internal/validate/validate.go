// Package validate compiles the embedded Smith JSON Schemas once and
// validates decoded documents against them at load time.
//
// The Go standard library regexp engine (RE2) cannot express every pattern
// used by the schemas — repo-config uses a negative lookahead in the
// workspace.project_roots pattern — so all schemas are compiled with a
// github.com/dlclark/regexp2 engine instead, following the example shipped
// with github.com/santhosh-tekuri/jsonschema/v6.
package validate

import (
	"fmt"
	"sync"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/cliwright/smith/schemas"
)

// Document names understood by Validate, one per embedded schema.
const (
	RepoConfig      = "repo-config"
	ProjectManifest = "project-manifest"
	ProjectType     = "project-type"
	Lock            = "lock"
)

var schemaFiles = map[string]string{
	RepoConfig:      "repo-config.schema.json",
	ProjectManifest: "project-manifest.schema.json",
	ProjectType:     "project-type.schema.json",
	Lock:            "lock.schema.json",
}

var (
	compileOnce sync.Once
	compiled    map[string]*jsonschema.Schema
	compileErr  error
)

func compileAll() {
	compiler := jsonschema.NewCompiler()
	compiler.UseRegexpEngine(dlclarkCompile)

	compiled = make(map[string]*jsonschema.Schema, len(schemaFiles))
	for name, file := range schemaFiles {
		f, err := schemas.FS.Open(file)
		if err != nil {
			compileErr = fmt.Errorf("opening embedded schema %s: %w", file, err)
			return
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		_ = f.Close()
		if err != nil {
			compileErr = fmt.Errorf("parsing embedded schema %s: %w", file, err)
			return
		}
		if err := compiler.AddResource(file, doc); err != nil {
			compileErr = fmt.Errorf("registering schema %s: %w", file, err)
			return
		}
		sch, err := compiler.Compile(file)
		if err != nil {
			compileErr = fmt.Errorf("compiling schema %s: %w", file, err)
			return
		}
		compiled[name] = sch
	}
}

// Validate checks doc — a value of the shape produced by decoding a document
// (map[string]any, []any, string, int, …) — against the named schema and
// describes every violation found.
func Validate(documentName string, doc any) error {
	compileOnce.Do(compileAll)
	if compileErr != nil {
		return compileErr
	}
	sch, ok := compiled[documentName]
	if !ok {
		return fmt.Errorf("unknown document schema %q (want one of: repo-config, project-manifest, project-type, lock)", documentName)
	}
	if err := sch.Validate(doc); err != nil {
		return fmt.Errorf("%s: schema validation failed: %w", documentName, err)
	}
	return nil
}

// dlclarkRegexp adapts *regexp2.Regexp to jsonschema.Regexp; see
// Example_customRegexpEngine in the jsonschema/v6 module.
type dlclarkRegexp regexp2.Regexp

func (re *dlclarkRegexp) MatchString(s string) bool {
	matched, err := (*regexp2.Regexp)(re).MatchString(s)
	return err == nil && matched
}

func (re *dlclarkRegexp) String() string {
	return (*regexp2.Regexp)(re).String()
}

func dlclarkCompile(pattern string) (jsonschema.Regexp, error) {
	re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*dlclarkRegexp)(re), nil
}
