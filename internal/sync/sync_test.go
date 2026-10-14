package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/ingot"
	"github.com/cliwright/smith/internal/typeref"
)

func TestRawBase(t *testing.T) {
	tests := []struct {
		gitURL  string
		want    string
		wantErr bool
	}{
		{
			gitURL: "https://github.com/cliwright/smith-project-types",
			want:   "https://raw.githubusercontent.com/cliwright/smith-project-types/main",
		},
		{
			gitURL:  "https://gitlab.com/cliwright/types",
			wantErr: true,
		},
		{
			gitURL:  "https://example.com/cliwright/types",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		got, err := RawBase(tt.gitURL)
		if tt.wantErr {
			if err == nil {
				t.Errorf("RawBase(%q) = %q, want error", tt.gitURL, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("RawBase(%q): %v", tt.gitURL, err)
			continue
		}
		if got != tt.want {
			t.Errorf("RawBase(%q) = %q, want %q", tt.gitURL, got, tt.want)
		}
	}
}

// packForTest builds an ingot holding the given slash-relative files.
func packForTest(t *testing.T, files map[string]string) []byte {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := ingot.Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// serveIngot registers an ingot and its sha256 sidecar at the registry path
// <name>/v<N>/<flat-ingot-name>.
func serveIngot(t *testing.T, mux *http.ServeMux, name string, version int, ingotBytes []byte) string {
	t.Helper()
	base := fmt.Sprintf("/%s/v%d/%s", name, version, ingot.FileName(name, version))
	mux.HandleFunc(base, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(ingotBytes)
	})
	sidecar := fmt.Sprintf("%s  %s\n", strings.TrimPrefix(ingot.Hash(ingotBytes), "sha256:"), ingot.FileName(name, version))
	mux.HandleFunc(base+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, sidecar)
	})
	return base
}

func mustRef(t *testing.T, s string) typeref.TypeRef {
	t.Helper()
	ref, err := typeref.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestFetch(t *testing.T) {
	const content = `{"name":"python/astral/lib","version":1,"description":"x","tools":["python"],"targets":{"build":["true"]}}`
	ingotBytes := packForTest(t, map[string]string{"project-type.json": content})

	t.Run("happy path", func(t *testing.T) {
		mux := http.NewServeMux()
		serveIngot(t, mux, "python/astral/lib", 1, ingotBytes)
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		ft, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if string(ft.Content) != string(ingotBytes) {
			t.Errorf("Content = %d bytes, want the %d-byte ingot", len(ft.Content), len(ingotBytes))
		}
		if want := ingot.Hash(ingotBytes); ft.Hash != want {
			t.Errorf("Hash = %q, want %q", ft.Hash, want)
		}
	})

	t.Run("404 yields ErrNotFound", func(t *testing.T) {
		server := httptest.NewServer(http.NewServeMux())
		t.Cleanup(server.Close)

		_, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Fetch error = %v, want ErrNotFound wrap", err)
		}
	})

	t.Run("sidecar drift is a hard error", func(t *testing.T) {
		base := fmt.Sprintf("/python/astral/lib/v1/%s", ingot.FileName("python/astral/lib", 1))
		mux := http.NewServeMux()
		mux.HandleFunc(base, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(ingotBytes)
		})
		mux.HandleFunc(base+".sha256", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, "0000000000000000000000000000000000000000000000000000000000000000  %s\n", ingot.FileName("python/astral/lib", 1))
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		_, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if err == nil || !strings.Contains(err.Error(), "sidecar mismatch") {
			t.Errorf("Fetch error = %v, want sidecar mismatch", err)
		}
	})

	t.Run("malformed sidecar", func(t *testing.T) {
		base := fmt.Sprintf("/python/astral/lib/v1/%s", ingot.FileName("python/astral/lib", 1))
		mux := http.NewServeMux()
		mux.HandleFunc(base, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(ingotBytes)
		})
		mux.HandleFunc(base+".sha256", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "not-a-sidecar\n")
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		_, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Errorf("Fetch error = %v, want malformed sidecar", err)
		}
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		mux := http.NewServeMux()
		base := fmt.Sprintf("/python/astral/lib/v1/%s", ingot.FileName("python/astral/lib", 1))
		mux.HandleFunc(base, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		_, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("Fetch error = %v, want HTTP 500", err)
		}
	})
}

// writeLocalRegistry lays out a smelted type under a local registry root:
// <root>/<name>/v<N>/<flat>.ingot + .sha256 — the same layout "smith smelt"
// writes in-place and the sync CLI's types.path source reads.
func writeLocalRegistry(t *testing.T, name string, version int, ingotBytes []byte) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(name), fmt.Sprintf("v%d", version))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, ingot.FileName(name, version))
	if err := os.WriteFile(artifact, ingotBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := fmt.Sprintf("%s  %s\n",
		strings.TrimPrefix(ingot.Hash(ingotBytes), "sha256:"), ingot.FileName(name, version))
	if err := os.WriteFile(artifact+".sha256", []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFetchLocal(t *testing.T) {
	const content = `{"name":"python/astral/lib","version":1,"description":"x","tools":["python"],"targets":{"build":["true"]}}`
	good := packForTest(t, map[string]string{"project-type.json": content})
	artifactPath := func(root string) string {
		return filepath.Join(root, "python", "astral", "lib", "v1", ingot.FileName("python/astral/lib", 1))
	}

	t.Run("ok", func(t *testing.T) {
		root := writeLocalRegistry(t, "python/astral/lib", 1, good)
		ft, err := FetchLocal(root, mustRef(t, "python/astral/lib@1"))
		if err != nil {
			t.Fatalf("FetchLocal: %v", err)
		}
		if ft.Hash != ingot.Hash(good) {
			t.Errorf("Hash = %q, want the artifact's hash %q", ft.Hash, ingot.Hash(good))
		}
	})

	t.Run("missing artifact is ErrNotFound", func(t *testing.T) {
		_, err := FetchLocal(t.TempDir(), mustRef(t, "python/astral/lib@1"))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("missing sidecar is ErrNotFound", func(t *testing.T) {
		root := writeLocalRegistry(t, "python/astral/lib", 1, good)
		if err := os.Remove(artifactPath(root) + ".sha256"); err != nil {
			t.Fatal(err)
		}
		_, err := FetchLocal(root, mustRef(t, "python/astral/lib@1"))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("artifact changed after smelting is a hard error", func(t *testing.T) {
		root := writeLocalRegistry(t, "python/astral/lib", 1, good)
		tampered := packForTest(t, map[string]string{"project-type.json": strings.Replace(content, `"description":"x"`, `"description":"y"`, 1)})
		if err := os.WriteFile(artifactPath(root), tampered, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := FetchLocal(root, mustRef(t, "python/astral/lib@1"))
		if err == nil || !strings.Contains(err.Error(), "sidecar mismatch") {
			t.Fatalf("err = %v, want sidecar mismatch", err)
		}
	})

	t.Run("malformed sidecar is a hard error", func(t *testing.T) {
		root := writeLocalRegistry(t, "python/astral/lib", 1, good)
		if err := os.WriteFile(artifactPath(root)+".sha256", []byte("garbage\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := FetchLocal(root, mustRef(t, "python/astral/lib@1"))
		if err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("err = %v, want malformed sidecar", err)
		}
	})
}
