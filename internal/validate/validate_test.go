package validate

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		docName string
		doc     any
		wantErr string
	}{
		{
			name:    "valid lock document",
			docName: Lock,
			doc: map[string]any{
				"version": 1,
				"types": map[string]any{
					"python/astral/lib@1": map[string]any{
						"registry": "cliwright",
						"hash":     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					},
				},
			},
		},
		{
			name:    "lock rejects a sources block",
			docName: Lock,
			doc: map[string]any{
				"version": 1,
				"sources": map[string]any{"cliwright": map[string]any{}},
				"types":   map[string]any{},
			},
			wantErr: "schema validation failed",
		},
		{
			name:    "missing required field",
			docName: Lock,
			doc:     map[string]any{"version": 1},
			wantErr: "schema validation failed",
		},
		{
			name:    "unknown document name",
			docName: "nope",
			doc:     map[string]any{},
			wantErr: "unknown document schema",
		},
		{
			name:    "repo config with lookahead pattern violation",
			docName: RepoConfig,
			doc: map[string]any{
				"version": 1,
				"name":    "demo",
				"registries": []any{
					map[string]any{"name": "r", "types": map[string]any{"git": "https://example.com"}, "templates": map[string]any{"path": "/x"}},
				},
				"workspace": map[string]any{"project_roots": []any{"../escape"}},
			},
			wantErr: "schema validation failed",
		},
		{
			name:    "repo config valid project roots",
			docName: RepoConfig,
			doc: map[string]any{
				"version": 1,
				"name":    "demo",
				"registries": []any{
					map[string]any{"name": "r", "types": map[string]any{"git": "https://example.com"}, "templates": map[string]any{"path": "/x"}},
				},
				"workspace": map[string]any{"project_roots": []any{"libs", "services/deep"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.docName, tt.doc)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate(%q) = %v, want nil", tt.docName, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want error containing %q", tt.docName, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate(%q) error = %q, want substring %q", tt.docName, err, tt.wantErr)
			}
		})
	}
}
