// Package typeref parses and formats project-type references of the form
// "language/flavor/kind@version" (e.g. "python/astral/lib@1"). The lexical
// rules mirror the patterns in project-manifest.schema.json (the type field)
// and lock.schema.json (the types keys).
package typeref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// namePattern is the type-name rule: three slash-separated slugs,
	// each starting with a letter.
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*){2}$`)

	// versionPattern is the version rule: a positive integer with no
	// leading zero, written as 1, 2, 3, …
	versionPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// TypeRef is a parsed project-type reference: the type name
// (language/flavor/kind) and its version.
type TypeRef struct {
	Name    string
	Version int
}

// Parse splits s into name and version. It returns an error unless s
// matches ^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*){2}@[1-9][0-9]*$.
func Parse(s string) (TypeRef, error) {
	name, version, found := strings.Cut(s, "@")
	if !found || name == "" || version == "" || strings.Contains(version, "@") {
		return TypeRef{}, fmt.Errorf("invalid type ref %q: want <language>/<flavor>/<kind>@<version>", s)
	}
	if !namePattern.MatchString(name) {
		return TypeRef{}, fmt.Errorf("invalid type ref %q: name must be language/flavor/kind of slugs matching %s", s, namePattern)
	}
	if !versionPattern.MatchString(version) {
		return TypeRef{}, fmt.Errorf("invalid type ref %q: version must be a positive integer without leading zeros", s)
	}
	v, err := strconv.Atoi(version)
	if err != nil {
		return TypeRef{}, fmt.Errorf("invalid type ref %q: %w", s, err)
	}
	return TypeRef{Name: name, Version: v}, nil
}

// String returns the canonical "name@version" form.
func (r TypeRef) String() string {
	return r.Name + "@" + strconv.Itoa(r.Version)
}
