package sync

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// serveType registers a type version and its sha256 sidecar on the mux.
func serveType(mux *http.ServeMux, path, content string) {
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, content)
	})
	sum := sha256.Sum256([]byte(content))
	mux.HandleFunc(path+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%x  project-type.json\n", sum[:])
	})
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

	t.Run("happy path", func(t *testing.T) {
		mux := http.NewServeMux()
		serveType(mux, "/python/astral/lib/v1/project-type.json", content)
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		ft, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if string(ft.Content) != content {
			t.Errorf("Content = %q", ft.Content)
		}
		sum := sha256.Sum256([]byte(content))
		if want := fmt.Sprintf("sha256:%x", sum[:]); ft.Hash != want {
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
		mux := http.NewServeMux()
		mux.HandleFunc("/python/astral/lib/v1/project-type.json", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, content)
		})
		mux.HandleFunc("/python/astral/lib/v1/project-type.json.sha256", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "0000000000000000000000000000000000000000000000000000000000000000  project-type.json\n")
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		_, err := Fetch(context.Background(), server.Client(), server.URL, mustRef(t, "python/astral/lib@1"))
		if err == nil || !strings.Contains(err.Error(), "sidecar mismatch") {
			t.Errorf("Fetch error = %v, want sidecar mismatch", err)
		}
	})

	t.Run("malformed sidecar", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/python/astral/lib/v1/project-type.json", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, content)
		})
		mux.HandleFunc("/python/astral/lib/v1/project-type.json.sha256", func(w http.ResponseWriter, _ *http.Request) {
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
		mux.HandleFunc("/python/astral/lib/v1/project-type.json", func(w http.ResponseWriter, _ *http.Request) {
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
