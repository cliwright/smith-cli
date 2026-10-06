// Package lock loads and saves .smith/lock.json, the file smith sync
// generates and checks in. Plain JSON: encoding/json in, schema validation,
// encoding/json out.
package lock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cliwright/smith/internal/validate"
)

// Lock is the typed view of .smith/lock.json.
type Lock struct {
	Version int                  `json:"version" yaml:"version"`
	Sources map[string]SourceSet `json:"sources" yaml:"sources"`
	Types   map[string]TypePin   `json:"types" yaml:"types"`
}

// SourceSet pins the types and templates sources of one registry as of the
// last sync.
type SourceSet struct {
	Types     Source `json:"types" yaml:"types"`
	Templates Source `json:"templates" yaml:"templates"`
}

// Source is a pinned registry source: either git at an exact commit, or a
// local path (dev mode, no rev recorded).
type Source struct {
	Git  string `json:"git,omitempty" yaml:"git,omitempty"`
	Rev  string `json:"rev,omitempty" yaml:"rev,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// TypePin pins one project type, keyed by name@version in the Types map.
type TypePin struct {
	Registry string `json:"registry" yaml:"registry"`
	Hash     string `json:"hash" yaml:"hash"`
}

// Load reads path, validates it against the embedded lock schema and
// returns the typed lock.
func Load(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := validate.Validate(validate.Lock, doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var lk Lock
	if err := json.Unmarshal(data, &lk); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &lk, nil
}

// Marshal renders lk as indented JSON with a trailing newline.
func Marshal(lk *Lock) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(lk); err != nil {
		return nil, fmt.Errorf("encoding lock: %w", err)
	}
	return buf.Bytes(), nil
}

// Save renders lk with Marshal and writes it to path.
func Save(path string, lk *Lock) error {
	data, err := Marshal(lk)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
