package ingot

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTypeDir creates a registry-layout type directory with the given files
// (slash-relative name → content) and returns its path.
func writeTypeDir(t *testing.T, files map[string]string) string {
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
	return dir
}

func TestPackIsReproducible(t *testing.T) {
	files := map[string]string{
		"project-type.json": `{"name":"python/astral/lib","version":1}`,
		"scripts/build.sh":  "#!/bin/sh\necho build\n",
		"help/project.md":   "# project\n",
		"help/build.md":     "# build\n",
	}
	first, err := Pack(writeTypeDir(t, files))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Pack(writeTypeDir(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("packing the same tree twice produced different bytes")
	}
	if Hash(first) != Hash(second) {
		t.Fatal("packing the same tree twice produced different hashes")
	}
}

func TestPackSkipsSidecarsAndPublishedArtifactsAndVcs(t *testing.T) {
	dir := writeTypeDir(t, map[string]string{
		"project-type.json":        `{"name":"python/astral/lib","version":1}`,
		"project-type.json.sha256": "deadbeef  project-type.json\n",
		".git/config":              "[core]\n",
	})
	// Simulate a directory that was packed and published before.
	first, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	name := "python-astral-lib@v1.ingot"
	if err := os.WriteFile(filepath.Join(dir, name), first, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte("deadbeef"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("repacking a published directory must not include sidecars, the ingot, or .git")
	}
	names, err := List(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "project-type.json" {
		t.Errorf("ingot entries = %v, want [project-type.json]", names)
	}
}

func TestPackRejectsSymlinks(t *testing.T) {
	dir := writeTypeDir(t, map[string]string{"project-type.json": "{}"})
	if err := os.Symlink("project-type.json", filepath.Join(dir, "evil-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("packing a symlink must fail, got %v", err)
	}
}

func TestUnpackRoundTrip(t *testing.T) {
	files := map[string]string{
		"project-type.json": `{"name":"python/astral/lib","version":1}`,
		"scripts/build.sh":  "#!/bin/sh\necho build\n",
	}
	ingotBytes, err := Pack(writeTypeDir(t, files))
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := Unpack(ingotBytes, dest); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s: want %q, got %q", name, want, got)
		}
	}
	// Executable bits survive: pack a +x script and unpack it.
	src := writeTypeDir(t, map[string]string{"scripts/run.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(src, "scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	execIngot, err := Pack(src)
	if err != nil {
		t.Fatal(err)
	}
	execDest := t.TempDir()
	if err := Unpack(execIngot, execDest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(execDest, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("unpacked script lost its executable bit")
	}
}

func TestUnpackRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(buf.Bytes(), t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "escapes the destination") {
		t.Fatalf("traversal entry must be rejected, got %v", err)
	}
}

func TestVerifyInstalled(t *testing.T) {
	files := map[string]string{
		"project-type.json": `{"name":"python/astral/lib","version":1}`,
		"scripts/build.sh":  "#!/bin/sh\n",
	}
	ingotBytes, err := Pack(writeTypeDir(t, files))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Unpack(ingotBytes, dir); err != nil {
		t.Fatal(err)
	}
	name := "python-astral-lib@v1.ingot" // flat artifact name inside the installed dir
	if err := os.WriteFile(filepath.Join(dir, name), ingotBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	good := Hash(ingotBytes)
	if err := VerifyInstalled(dir, good); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyInstalled(dir, "sha256:0000000000000000000000000000000000000000000000000000000000000000"); err == nil ||
		!strings.Contains(err.Error(), "hash verification") {
		t.Fatalf("hash mismatch must be reported, got %v", err)
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalled(dir, good); err == nil ||
		!strings.Contains(err.Error(), "no ingot") {
		t.Fatalf("missing ingot must be reported, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), ingotBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Hand-modify an unpacked file: drift must be caught.
	if err := os.WriteFile(filepath.Join(dir, "scripts", "build.sh"), []byte("#!/bin/sh\necho tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalled(dir, good); err == nil ||
		!strings.Contains(err.Error(), "drifted") {
		t.Fatalf("content drift must be reported, got %v", err)
	}
	// An extra unexpected file is drift too.
	if err := os.WriteFile(filepath.Join(dir, "scripts", "build.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalled(dir, good); err == nil ||
		!strings.Contains(err.Error(), "unexpected stray.txt") {
		t.Fatalf("unexpected file must be reported, got %v", err)
	}
}

func TestNames(t *testing.T) {
	if got := RefName("python/astral/lib", 1); got != "python/astral/lib@v1" {
		t.Errorf("RefName: %q", got)
	}
	if got := FileName("python/astral/lib", 1); got != "python-astral-lib@v1.ingot" {
		t.Errorf("FileName: %q", got)
	}
	if got := SidecarName("python/astral/lib", 1); got != "python-astral-lib@v1.ingot.sha256" {
		t.Errorf("SidecarName: %q", got)
	}
}
