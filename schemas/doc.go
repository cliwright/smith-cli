// Package schemas exposes the Smith JSON Schema contracts embedded in the
// binary. The schema files in this directory are the canonical copies; they
// are validated against at document load time and are eventually published
// from the registry repository.
package schemas

import "embed"

// FS holds the four Smith document schemas.
//
//go:embed *.schema.json
var FS embed.FS
