// Package dotenv loads the KEY=VALUE "dotenv" files that smith repositories
// and projects keep in their conventional dotenv/ directories. Loading is
// opt-in per level (the dotenv: key of .smith/repo.yml and smith.yml) and
// never touches any other .env location — the directory name is the
// convention. Files are parameters, not secrets machinery: parsing is strict
// and loud.
package dotenv

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DirName is the etched name of the directory holding environment files, at
// the repo root and/or in a project directory.
const DirName = "dotenv"

// namePattern is the environment-selector pattern. The selector becomes a
// dotfile name (.<name>.env), so anything path-shaped is rejected outright.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// NamePatternDesc describes namePattern for error messages.
const NamePatternDesc = "a lowercase name matching [a-z][a-z0-9_-]*"

// ValidName reports whether name is a usable environment selector.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// BasePath returns the always-loaded file of the dotenv directory under dir.
func BasePath(dir string) string { return filepath.Join(dir, DirName, ".env") }

// OverlayPath returns the environment-specific file of the dotenv directory
// under dir for the named environment.
func OverlayPath(dir, name string) string {
	return filepath.Join(dir, DirName, "."+name+".env")
}

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse parses dotenv content into a map: KEY=VALUE lines, '#' comment lines,
// and blank lines. Values are literal except that one pair of matching
// surrounding single or double quotes is stripped. Malformed lines, illegal
// variable names, and duplicate keys are hard errors naming the file and
// line — a typo must never load as an empty value.
func Parse(data []byte, path string) (map[string]string, error) {
	vars := map[string]string{}
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: not a KEY=VALUE line: %q", path, i+1, trimmed)
		}
		key = strings.TrimSpace(key)
		if !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("%s:%d: invalid variable name %q (want [A-Za-z_][A-Za-z0-9_]*)", path, i+1, key)
		}
		if _, dup := vars[key]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate key %q", path, i+1, key)
		}
		vars[key] = unquote(strings.TrimSpace(value))
	}
	return vars, nil
}

// unquote strips one pair of matching surrounding single or double quotes.
func unquote(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '"' || first == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// LoadFile parses the dotenv file at path, which must exist.
func LoadFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, path)
}

// Merge returns base with overlay's entries winning per key.
func Merge(base, overlay map[string]string) map[string]string {
	merged := maps.Clone(base)
	maps.Copy(merged, overlay)
	return merged
}

// Entries flattens vars into sorted KEY=VALUE strings, ready to append onto a
// subprocess environment in ascending precedence order (later wins).
func Entries(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+vars[key])
	}
	return out
}

// Layer is one loaded dotenv file: its absolute path (for display and
// errors) and the variables it sets.
type Layer struct {
	Path string
	Vars map[string]string
}

// LoadStack loads the dotenv stack of the directory dir/dotenv: the base
// .env — required; a missing one is a hard error naming the opting-in file
// (".smith/repo.yml", or a project's smith.yml, via source) — and, when env
// is non-empty, the optional overlay .<env>.env. Layers are returned in load
// order (base first); callers merge in that order so later layers win per
// key. Parse errors carry the file path and line.
func LoadStack(dir, env, source string) ([]Layer, error) {
	basePath := BasePath(dir)
	base, err := LoadFile(basePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s sets dotenv: true but %s does not exist", source, basePath)
		}
		return nil, err
	}
	layers := []Layer{{Path: basePath, Vars: base}}
	if env != "" {
		overlayPath := OverlayPath(dir, env)
		overlay, err := LoadFile(overlayPath)
		switch {
		case err == nil:
			layers = append(layers, Layer{Path: overlayPath, Vars: overlay})
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, err
		}
	}
	return layers, nil
}

// Overlays counts the overlay layers in a stack (0 or 1 today; derived from
// the layer count so the policy survives future layering changes).
func Overlays(layers []Layer) int {
	if len(layers) == 0 {
		return 0
	}
	return len(layers) - 1
}

// NoOverlayError is the hard error for a selected environment with no
// matching overlay at any dotenv-enabled level. Shared by the runner and
// --context so both fail identically.
func NoOverlayError(env string) error {
	return fmt.Errorf("environment %q is selected but no dotenv/.%s.env exists at any dotenv-enabled level (repo or project)", env, env)
}
