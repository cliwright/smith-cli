// Package trust implements the smith trust model: installed project types
// are untrusted by default, the user vets and explicitly trusts them with
// "smith trust", and executing steps from an untrusted or stale type is a
// hard error — the gate lives in the cli package, which every run path
// funnels through. One marker file per installed ref lives in the repo's
// .smith/trust directory, deliberately outside the custody-verified
// installed tree (a marker beside the ingot would read as tree drift).
// A marker is a single line "<sha256> <user> <timestamp>" and is valid only
// while the recorded hash matches the installed artifact — the lock pin,
// since custody is verified before the gate runs — so content changes
// (publisher churn, re-sync from another source) invalidate trust. Trust
// means "I vetted THIS code", not a name. SMITH_AUTO_TRUST=1 bypasses the
// gate entirely (the CI escape hatch; subprocess-env-only like
// SMITH_ENV/SMITH_PAGER, never a template var) and never writes markers.
package trust

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/cliwright/smith/internal/typeref"
)

// trustDirName is the trust store's directory under the repo's .smith.
const trustDirName = "trust"

// Status is the trust state of one installed type.
type Status int

const (
	Untrusted Status = iota // no (or empty) marker
	Trusted                 // marker hash matches the installed artifact
	Stale                   // marker exists, but the content changed since it was trusted
)

// String renders the short status word used in annotations.
func (s Status) String() string {
	switch s {
	case Trusted:
		return "trusted"
	case Stale:
		return "stale"
	default:
		return "untrusted"
	}
}

// Marker is one parsed trust marker: the ingot hash the user vetted, who
// vetted it, and when.
type Marker struct {
	Hash string
	User string
	Time string
}

// Dir is the repo-local trust store: one marker file per installed ref.
func Dir(repoRoot string) string {
	return filepath.Join(repoRoot, ".smith", trustDirName)
}

// Check reads ref's trust marker under repoRoot and compares the recorded
// hash against wantHash (the installed artifact's hash, i.e. the lock pin).
// A missing or empty marker reports Untrusted with a nil Marker; a hash
// match reports Trusted; any other hash reports Stale. A malformed marker
// is an error — a marker smith wrote should never drift.
func Check(repoRoot string, ref typeref.TypeRef, wantHash string) (Status, *Marker, error) {
	data, err := os.ReadFile(markerPath(repoRoot, ref))
	if errors.Is(err, os.ErrNotExist) {
		return Untrusted, nil, nil
	}
	if err != nil {
		return Untrusted, nil, fmt.Errorf("reading trust marker for %s: %w", ref, err)
	}
	if len(data) == 0 {
		return Untrusted, nil, nil
	}
	fields := strings.Fields(string(data))
	if len(fields) != 3 {
		return Untrusted, nil, fmt.Errorf("malformed trust marker for %s: want \"<sha256> <user> <timestamp>\", got %q",
			ref, strings.TrimSpace(string(data)))
	}
	m := &Marker{Hash: fields[0], User: fields[1], Time: fields[2]}
	if m.Hash != wantHash {
		return Stale, m, nil
	}
	return Trusted, m, nil
}

// Write records trust for ref: a marker naming the hash the user vetted,
// the current user, and the UTC timestamp, then keeps the repo-root
// .gitignore fencing the trust directory out of version control (markers
// are per-user; a checked-in clone stays untrusted until its owner vets
// the code).
func Write(repoRoot string, ref typeref.TypeRef, hash string) (*Marker, error) {
	m := &Marker{Hash: hash, User: currentUser(), Time: time.Now().UTC().Format(time.RFC3339)}
	dir := Dir(repoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating trust directory %s: %w", dir, err)
	}
	if err := os.WriteFile(markerPath(repoRoot, ref), []byte(m.Hash+" "+m.User+" "+m.Time+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("writing trust marker for %s: %w", ref, err)
	}
	if err := EnsureGitignore(repoRoot); err != nil {
		return nil, err
	}
	return m, nil
}

// Bypassed reports whether SMITH_AUTO_TRUST=1 disables the execution gate.
// Exact value "1" only — anything else (including "true") keeps the gate
// shut. It short-circuits the gate only: no markers are written, and the
// inspection views note when it is active.
func Bypassed() bool { return os.Getenv("SMITH_AUTO_TRUST") == "1" }

const (
	gitignoreBlockStart = "# >>> smith trust >>>"
	gitignoreBlockLine  = ".smith/trust/"
	gitignoreBlockEnd   = "# <<< smith trust <<<"
)

// EnsureGitignore keeps markers per-user: unless the repo-root .gitignore
// already fences the trust directory out — smith's sentinel block or any
// ".smith/trust/" line, a hand-written one counts — append the fenced
// block, creating the file when absent. The check is append-if-missing, so
// a user who removes both the block and the line gets it back on the next
// marker write; deliberate repo-shared trust is expressed by removing it
// again.
func EnsureGitignore(repoRoot string) error {
	path := filepath.Join(repoRoot, ".gitignore")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(gitignoreBlock()+"\n"), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if trimmed == gitignoreBlockLine || trimmed == gitignoreBlockStart {
			return nil
		}
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content+gitignoreBlock()+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// gitignoreBlock is the fenced sentinel block appended to the repo-root
// .gitignore.
func gitignoreBlock() string {
	return strings.Join([]string{gitignoreBlockStart, gitignoreBlockLine, gitignoreBlockEnd}, "\n")
}

// markerPath resolves ref's marker file inside the trust store: the flat
// ingot naming with a .trusted suffix, e.g.
// .smith/trust/python-astral-lib@v1.trusted.
func markerPath(repoRoot string, ref typeref.TypeRef) string {
	return filepath.Join(Dir(repoRoot), flatName(ref))
}

// flatName is the marker file name: the artifact's flat "<name>@v<N>" stem
// with a .trusted suffix, delegating to typeref.FlatName — the single
// flattened-naming rule shared with sync's flatArtifactName.
func flatName(ref typeref.TypeRef) string {
	return typeref.FlatName(ref) + ".trusted"
}

// currentUser resolves the vetting user: os/user's login name, the $USER
// environment as fallback, "unknown" when both come up empty.
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if env := os.Getenv("USER"); env != "" {
		return env
	}
	return "unknown"
}
