package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check func(t *testing.T, lk *Lock)
	}{
		{
			name: "example lock.json",
			path: "testdata/lock.json",
			check: func(t *testing.T, lk *Lock) {
				t.Helper()
				if lk.Version != 1 {
					t.Errorf("Version = %d, want 1", lk.Version)
				}
				if len(lk.Sources) != 1 {
					t.Fatalf("len(Sources) = %d, want 1", len(lk.Sources))
				}
				cliwright, ok := lk.Sources["cliwright"]
				if !ok {
					t.Fatalf("Sources missing %q", "cliwright")
				}
				if cliwright.Types.Git != "https://github.com/cliwright/smith-project-types" {
					t.Errorf("Types.Git = %q", cliwright.Types.Git)
				}
				if cliwright.Types.Rev != "9f2c1eab40d3a7b5c88e61f2c0d4e6a78b31d592" {
					t.Errorf("Types.Rev = %q", cliwright.Types.Rev)
				}
				if cliwright.Templates.Git != "https://github.com/cliwright/smith-project-templates" {
					t.Errorf("Templates.Git = %q", cliwright.Templates.Git)
				}
				if len(lk.Types) != 1 {
					t.Fatalf("len(Types) = %d, want 1", len(lk.Types))
				}
				pin, ok := lk.Types["python/astral/lib@1"]
				if !ok {
					t.Fatalf("Types missing %q", "python/astral/lib@1")
				}
				if pin.Registry != "cliwright" {
					t.Errorf("pin.Registry = %q, want %q", pin.Registry, "cliwright")
				}
				if pin.Hash != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
					t.Errorf("pin.Hash = %q", pin.Hash)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lk, err := Load(tt.path)
			if err != nil {
				t.Fatalf("Load(%q): %v", tt.path, err)
			}
			tt.check(t, lk)
		})
	}
}

func TestLoadRejectsInvalidDocs(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "types key not a name@version ref",
			json: `{"version":1,"sources":{"r":{"types":{"path":"/x"},"templates":{"path":"/y"}}},"types":{"python/astral/lib":{"registry":"r","hash":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}}`,
			want: "types",
		},
		{
			name: "hash not sha256",
			json: `{"version":1,"sources":{"r":{"types":{"path":"/x"},"templates":{"path":"/y"}}},"types":{"python/astral/lib@1":{"registry":"r","hash":"md5:zzz"}}}`,
			want: "hash",
		},
		{
			name: "malformed rev",
			json: `{"version":1,"sources":{"r":{"types":{"git":"https://example.com","rev":"notasha"},"templates":{"path":"/y"}}},"types":{"python/astral/lib@1":{"registry":"r","hash":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}}`,
			want: "rev",
		},
		{
			name: "unknown top-level key",
			json: `{"version":1,"sources":{"r":{"types":{"path":"/x"},"templates":{"path":"/y"}}},"types":{},"extra":1}`,
			want: "extra",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load(%q) succeeded, want error containing %q", path, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load(%q) error = %q, want substring %q", path, err, tt.want)
			}
		})
	}
}

// TestLoadGitSourceWithoutRev verifies the pre-sync shape written by
// `smith init`: a git source with no rev is valid.
func TestLoadGitSourceWithoutRev(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	doc := `{"version":1,"sources":{"cliwright":{"types":{"git":"https://example.com/types"},"templates":{"git":"https://example.com/templates"}}},"types":{}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	lk, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	src := lk.Sources["cliwright"].Types
	if src.Git != "https://example.com/types" {
		t.Errorf("Git = %q", src.Git)
	}
	if src.Rev != "" {
		t.Errorf("Rev = %q, want empty before the first sync", src.Rev)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	lk, err := Load("testdata/lock.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	path := filepath.Join(t.TempDir(), "lock.json")
	if err := Save(path, lk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	pin := reloaded.Types["python/astral/lib@1"]
	if pin.Hash != lk.Types["python/astral/lib@1"].Hash ||
		pin.Registry != "cliwright" ||
		len(reloaded.Sources) != 1 {
		t.Errorf("round trip = %+v", reloaded)
	}
}
