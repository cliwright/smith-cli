// Package lock loads and saves .smith/lock.json, the file smith sync
// generates and checks in. Plain JSON: encoding/json in, schema validation,
// encoding/json out.
package lock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cliwright/smith/internal/validate"
)

// Lock is the typed view of .smith/lock.json.
type Lock struct {
	Version int                `json:"version" yaml:"version"`
	Types   map[string]TypePin `json:"types" yaml:"types"`
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

// WriteAtomic renders lk and writes it to path atomically: the bytes land in
// a temporary file in the same directory and are renamed over path, so a
// crash mid-write can never leave a truncated lock behind.
func WriteAtomic(path string, lk *Lock) error {
	data, err := Marshal(lk)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lock-*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
