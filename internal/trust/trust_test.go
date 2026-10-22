package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cliwright/smith/internal/typeref"
)

var testRef = typeref.TypeRef{Name: "python/astral/lib", Version: 1}

const testHash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestWriteThenCheckTrusted(t *testing.T) {
	repo := t.TempDir()
	m, err := Write(repo, testRef, testHash)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if m.Hash != testHash {
		t.Errorf("marker hash = %q, want the vetted hash", m.Hash)
	}
	if m.User == "" {
		t.Error("marker user is empty")
	}
	if _, err := time.Parse(time.RFC3339, m.Time); err != nil {
		t.Errorf("marker time %q is not RFC3339: %v", m.Time, err)
	}

	status, got, err := Check(repo, testRef, testHash)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != Trusted {
		t.Errorf("status = %v, want Trusted", status)
	}
	if got.Hash != m.Hash || got.User != m.User || got.Time != m.Time {
		t.Errorf("round-tripped marker = %+v, want %+v", got, m)
	}

	// The marker is exactly one line: hash, user, timestamp.
	data, err := os.ReadFile(filepath.Join(Dir(repo), flatName(testRef)))
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if strings.Count(string(data), "\n") != 1 || !strings.HasSuffix(string(data), "\n") {
		t.Errorf("marker = %q, want a single newline-terminated line", data)
	}
}

func TestCheckUntrustedWhenMarkerMissing(t *testing.T) {
	repo := t.TempDir()
	status, m, err := Check(repo, testRef, testHash)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != Untrusted || m != nil {
		t.Errorf("status = %v, marker = %+v; want Untrusted with no marker", status, m)
	}
}

func TestCheckUntrustedWhenMarkerEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(repo), flatName(testRef)), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	status, m, err := Check(repo, testRef, testHash)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != Untrusted || m != nil {
		t.Errorf("status = %v, marker = %+v; want Untrusted with no marker", status, m)
	}
}

func TestCheckStaleOnHashMismatch(t *testing.T) {
	repo := t.TempDir()
	m, err := Write(repo, testRef, testHash)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	changed := "sha256:ffffffffffabcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	status, got, err := Check(repo, testRef, changed)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != Stale {
		t.Errorf("status = %v, want Stale", status)
	}
	if got.Hash != m.Hash {
		t.Errorf("stale marker hash = %q, want the recorded hash %q", got.Hash, m.Hash)
	}
}

func TestCheckMalformedMarkerErrors(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(repo), flatName(testRef))
	if err := os.WriteFile(path, []byte("only-one-field\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(repo, testRef, testHash)
	if err == nil || !strings.Contains(err.Error(), "malformed trust marker") {
		t.Fatalf("Check error = %v, want malformed-marker", err)
	}
}

func TestBypassedExactValue(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", false},
		{"0", false},
		{"", false},
		{"01", false},
	} {
		t.Run("SMITH_AUTO_TRUST="+tt.value, func(t *testing.T) {
			t.Setenv("SMITH_AUTO_TRUST", tt.value)
			if got := Bypassed(); got != tt.want {
				t.Errorf("Bypassed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureGitignoreCreatesFile(t *testing.T) {
	repo := t.TempDir()
	if err := EnsureGitignore(repo); err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	want := "# >>> smith trust >>>\n.smith/trust/\n# <<< smith trust <<<\n"
	if string(data) != want {
		t.Errorf(".gitignore = %q, want %q", data, want)
	}
}

func TestEnsureGitignoreIdempotent(t *testing.T) {
	repo := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := EnsureGitignore(repo); err != nil {
			t.Fatalf("EnsureGitignore run %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "smith trust >>>") != 1 {
		t.Errorf("block appended more than once:\n%s", data)
	}
}

func TestEnsureGitignoreRespectsExistingLine(t *testing.T) {
	repo := t.TempDir()
	existing := "bin/\n.smith/trust/\n"
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitignore(repo); err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Errorf(".gitignore = %q, want untouched %q", data, existing)
	}
}

func TestEnsureGitignoreAppendsAfterExistingContent(t *testing.T) {
	repo := t.TempDir()
	existing := "bin/\ndist/\n"
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitignore(repo); err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	want := existing + "# >>> smith trust >>>\n.smith/trust/\n# <<< smith trust <<<\n"
	if string(data) != want {
		t.Errorf(".gitignore = %q, want %q", data, want)
	}
}

func TestEnsureGitignoreReappendsAfterRemoval(t *testing.T) {
	// The append-if-missing rule is deliberate: a user who removes both
	// the block and the line gets it back on the next marker write
	// (deliberate repo-shared trust is expressed by removing it again).
	repo := t.TempDir()
	if err := EnsureGitignore(repo); err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("bin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitignore(repo); err != nil {
		t.Fatalf("EnsureGitignore after removal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	want := "bin/\n# >>> smith trust >>>\n.smith/trust/\n# <<< smith trust <<<\n"
	if string(data) != want {
		t.Errorf(".gitignore = %q, want the block re-appended:\n%s", data, want)
	}
}

func TestWriteEnsuresGitignore(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, testRef, testHash); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !strings.Contains(string(data), ".smith/trust/") {
		t.Errorf(".gitignore missing the trust fence:\n%s", data)
	}
}

func TestFlatName(t *testing.T) {
	for _, tt := range []struct {
		ref  typeref.TypeRef
		want string
	}{
		{typeref.TypeRef{Name: "python/astral/lib", Version: 1}, "python-astral-lib@v1.trusted"},
		{typeref.TypeRef{Name: "tofu/aws-trivy/module", Version: 12}, "tofu-aws-trivy-module@v12.trusted"},
	} {
		if got := flatName(tt.ref); got != tt.want {
			t.Errorf("flatName(%s) = %q, want %q", tt.ref, got, tt.want)
		}
	}
	// The marker stem matches the ingot artifact naming, per the design.
	ref := typeref.TypeRef{Name: "python/astral/lib", Version: 1}
	if stem := strings.TrimSuffix(flatName(ref), ".trusted"); stem != "python-astral-lib@v1" {
		t.Errorf("marker stem = %q, want the ingot stem python-astral-lib@v1", stem)
	}
}
