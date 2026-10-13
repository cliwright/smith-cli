package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/ingot"
	"github.com/cliwright/smith/internal/typeref"
)

// packFilesForTest builds an ingot from the given slash-relative files.
func packFilesForTest(t *testing.T, files map[string]string) []byte {
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

// installIngotForTest installs a type the way smith sync does: the directory
// .smith/types/<name>@v<N> holding the flat ingot artifact and its unpacked
// contents. Returns the ingot hash for lock pinning.
func installIngotForTest(t *testing.T, repo string, ref typeref.TypeRef, files map[string]string) string {
	t.Helper()
	ingotBytes := packFilesForTest(t, files)
	dir := filepath.Join(repo, cacheDirName, "types", installRelDir(ref))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, flatArtifactName(ref)), ingotBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ingot.Unpack(ingotBytes, dir); err != nil {
		t.Fatal(err)
	}
	return ingot.Hash(ingotBytes)
}

// parseNameVer converts the "@v<N>" install-name form to a parsed typeref.
func parseNameVer(nameVer string) (typeref.TypeRef, error) {
	name, ver, found := strings.Cut(nameVer, "@")
	if !found {
		return typeref.TypeRef{}, fmt.Errorf("want <name>@v<N>, got %q", nameVer)
	}
	return typeref.Parse(name + "@" + strings.TrimPrefix(ver, "v"))
}

// refFromV converts the "@v<N>" install-name form to a parsed typeref.
func refFromV(t *testing.T, nameVer string) typeref.TypeRef {
	t.Helper()
	ref, err := parseNameVer(nameVer)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// installedDir resolves the installed directory of a "<name>@v<N>" string.
func installedDir(t *testing.T, repo, nameVer string) string {
	t.Helper()
	ref, err := parseNameVer(nameVer)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repo, cacheDirName, "types", installRelDir(ref))
}
