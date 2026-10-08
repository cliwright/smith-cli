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
			json: `{"version":1,"types":{"python/astral/lib":{"registry":"r","hash":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}}`,
			want: "types",
		},
		{
			name: "hash not sha256",
			json: `{"version":1,"types":{"python/astral/lib@1":{"registry":"r","hash":"md5:zzz"}}}`,
			want: "hash",
		},
		{
			name: "pin missing registry",
			json: `{"version":1,"types":{"python/astral/lib@1":{"hash":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}}`,
			want: "registry",
		},
		{
			name: "unknown top-level key",
			json: `{"version":1,"types":{},"extra":1}`,
			want: "extra",
		},
		{
			name: "sources block is no longer allowed",
			json: `{"version":1,"sources":{"r":{"types":{"git":"https://example.com"}}},"types":{}}`,
			want: "sources",
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
		pin.Registry != "cliwright" {
		t.Errorf("round trip = %+v", reloaded)
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.json")
	lk := &Lock{Version: 1, Types: map[string]TypePin{
		"python/astral/lib@1": {Registry: "cliwright", Hash: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}}
	if err := WriteAtomic(path, lk); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if len(reloaded.Types) != 1 {
		t.Errorf("Types = %v, want one pin", reloaded.Types)
	}
	// No temporary files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "lock.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dir contains %v, want only lock.json", names)
	}
}
