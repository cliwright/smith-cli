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
