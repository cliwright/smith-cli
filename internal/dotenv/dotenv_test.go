package dotenv

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	vars, err := Parse([]byte(`
# a comment
REGISTRY=ghcr.io

QUOTED="hello world"
SINGLE='hi there'
EMPTY=
SPACED = value with spaces
`), "test.env")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{
		"REGISTRY": "ghcr.io",
		"QUOTED":   "hello world",
		"SINGLE":   "hi there",
		"EMPTY":    "",
		"SPACED":   "value with spaces",
	}
	if !reflect.DeepEqual(vars, want) {
		t.Errorf("vars = %v, want %v", vars, want)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"not a kv line\n": "not a KEY=VALUE line",
		"1BAD=x\n":        "invalid variable name",
		"NO-EQ=x\n":       "invalid variable name",
		"A=1\nA=2\n":      "duplicate key",
		"A=1\n  \nA=2\n":  "duplicate key",
	}
	for content, wantSub := range cases {
		_, err := Parse([]byte(content), "bad.env")
		if err == nil {
			t.Errorf("Parse(%q): no error, want one containing %q", content, wantSub)
			continue
		}
		if !strings.Contains(err.Error(), wantSub) || !strings.Contains(err.Error(), "bad.env") {
			t.Errorf("Parse(%q) error = %q, want %q with file name", content, err, wantSub)
		}
	}
}

func TestParseKeepsHashInValues(t *testing.T) {
	vars, err := Parse([]byte("VALUE=a # b\n"), "x.env")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if vars["VALUE"] != "a # b" {
		t.Errorf("VALUE = %q, want inline hash kept", vars["VALUE"])
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Fatal("LoadFile of missing file: no error")
	}
}

func TestPathsAndValidName(t *testing.T) {
	if got := BasePath("/repo"); got != filepath.Join("/repo", "dotenv", ".env") {
		t.Errorf("BasePath = %q", got)
	}
	if got := OverlayPath("/repo", "dev"); got != filepath.Join("/repo", "dotenv", ".dev.env") {
		t.Errorf("OverlayPath = %q", got)
	}
	for name, want := range map[string]bool{
		"dev": true, "prd-1": true, "a_b": true,
		"": false, "Dev": false, "dev/x": false, "../x": false, ".": false, "-a": false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMergeAndEntries(t *testing.T) {
	merged := Merge(
		map[string]string{"A": "base", "B": "base"},
		map[string]string{"B": "overlay", "C": "overlay"},
	)
	if merged["A"] != "base" || merged["B"] != "overlay" || merged["C"] != "overlay" {
		t.Errorf("Merge = %v", merged)
	}
	if got, want := Entries(map[string]string{"B": "2", "A": "1"}), []string{"A=1", "B=2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Entries = %v, want %v", got, want)
	}
}

func TestLoadFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotenv", ".env")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("K=v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if vars["K"] != "v" {
		t.Errorf("K = %q", vars["K"])
	}
}
