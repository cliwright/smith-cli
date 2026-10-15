package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/ingot"
	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/typeref"
)

const testTypeContent = `{"name":"python/astral/lib","version":1,"description":"Python library on the Astral toolchain","tools":["python","uv"],"targets":{"build":["uv build"]}}`

// fakeRegistries is an httptest server pretending to be raw.githubusercontent.com.
type fakeRegistries struct {
	server *httptest.Server
	store  map[string][]byte
	hits   int32 // requests served, so tests can assert a registry was never tried
}

func newFakeRegistries(t *testing.T) *fakeRegistries {
	t.Helper()
	f := &fakeRegistries{store: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.hits, 1)
		body, ok := f.store[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// serveType publishes a type version as an ingot with a correct sidecar at
// /<orgRepo>/<name>/v<version>/<flat-ingot-name>.
func (f *fakeRegistries) serveType(t *testing.T, orgRepo, name string, version int, content string) {
	f.serveTypeFiles(t, orgRepo, name, version, map[string]string{"project-type.json": content})
}

// serveTypeFiles publishes a type version whose ingot holds the given files.
func (f *fakeRegistries) serveTypeFiles(t *testing.T, orgRepo, name string, version int, files map[string]string) {
	ingotBytes := packFilesForTest(t, files)
	base := fmt.Sprintf("/%s/%s/v%d/%s", orgRepo, name, version, ingot.FileName(name, version))
	f.store[base] = ingotBytes
	f.store[base+".sha256"] = []byte(fmt.Sprintf("%s  %s\n",
		strings.TrimPrefix(ingot.Hash(ingotBytes), "sha256:"), ingot.FileName(name, version)))
}

func (f *fakeRegistries) corruptSidecar(orgRepo, name string, version int) {
	base := fmt.Sprintf("/%s/%s/v%d/%s", orgRepo, name, version, ingot.FileName(name, version))
	f.store[base+".sha256"] = []byte(fmt.Sprintf("0000000000000000000000000000000000000000000000000000000000000000  %s\n", ingot.FileName(name, version)))
}

// useFakeRegistries redirects rawBaseURL at the fake server.
func useFakeRegistries(t *testing.T, f *fakeRegistries) {
	t.Helper()
	old := rawBaseURL
	rawBaseURL = func(gitURL string) (string, error) {
		return f.server.URL + "/" + strings.TrimPrefix(gitURL, "https://github.com/"), nil
	}
	t.Cleanup(func() { rawBaseURL = old })
}

type regEntry struct{ name, src string }

// writeSyncRepo writes an initialized repo with the given registries (in
// repo.yml priority order) and manifests (dir -> {project name, type ref}).
// Each registry's src is the types-source body as written in repo.yml
// ("git: <url>" or "path: <dir>").
func writeSyncRepo(t *testing.T, repo string, regs []regEntry, manifests map[string][2]string) {
	t.Helper()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("version: 1\nname: mock\nregistries:\n")
	for _, reg := range regs {
		fmt.Fprintf(&b, "  - name: %s\n    types: {%s}\n    templates: {git: https://example.com/templates}\n", reg.name, reg.src)
	}
	b.WriteString("workspace:\n  project_roots:\n    - libs\n")
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, nt := range manifests {
		full := filepath.Join(repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("version: 1\nname: %s\ntype: %s\n", nt[0], nt[1])
		if err := os.WriteFile(filepath.Join(full, "smith.yml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func lockPathIn(repo string) string {
	return filepath.Join(repo, cacheDirName, lockJSONName)
}

var testRegs = []regEntry{
	{"cliwright", "git: https://github.com/cliwright/types"},
	{"acme", "git: https://github.com/acme/types"},
}

// testTypeIngotHash is the lock hash a registry serving testTypeContent
// pins: the hash of the ingot packing it, not of the bare JSON.
func testTypeIngotHash(t *testing.T) string {
	t.Helper()
	return ingot.Hash(packFilesForTest(t, map[string]string{"project-type.json": testTypeContent}))
}

func TestSyncFetchesManifestRefs(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	t.Chdir(repo)

	out, err := run(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}

	installed, err := os.ReadFile(filepath.Join(installedDir(t, repo, "python/astral/lib@v1"), "project-type.json"))
	if err != nil {
		t.Fatalf("installed type missing: %v", err)
	}
	if string(installed) != testTypeContent {
		t.Errorf("installed content = %q", installed)
	}

	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	pin, ok := lk.Types["python/astral/lib@1"]
	if !ok {
		t.Fatalf("lock missing pin for python/astral/lib@1")
	}
	if pin.Registry != "cliwright" {
		t.Errorf("pin.Registry = %q, want cliwright", pin.Registry)
	}
	if want := testTypeIngotHash(t); pin.Hash != want {
		t.Errorf("pin.Hash = %q, want the ingot hash %q", pin.Hash, want)
	}

	for _, want := range []string{"fetched from cliwright", "lock written"} {
		if !strings.Contains(out, want) {
			t.Errorf("sync output missing %q:\n%s", want, out)
		}
	}
}

func TestSyncExplicitRefs(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	out, err := run(t, "sync", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(installedDir(t, repo, "python/astral/lib@v1")); err != nil {
		t.Errorf("installed type missing: %v", err)
	}
	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	if lk.Types["python/astral/lib@1"].Registry != "cliwright" {
		t.Errorf("pin = %+v", lk.Types["python/astral/lib@1"])
	}
}

func TestSyncFallsBackToSecondRegistry(t *testing.T) {
	f := newFakeRegistries(t)
	// cliwright does not serve the type; acme does.
	f.serveType(t, "acme/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	out, err := run(t, "sync", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}
	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	if lk.Types["python/astral/lib@1"].Registry != "acme" {
		t.Errorf("pin.Registry = %q, want acme", lk.Types["python/astral/lib@1"].Registry)
	}
	if !strings.Contains(out, "fetched from acme") {
		t.Errorf("sync output missing fallback notice:\n%s", out)
	}
	if _, err := os.Stat(installedDir(t, repo, "python/astral/lib@v1")); err != nil {
		t.Errorf("installed type missing: %v", err)
	}
}

func TestSyncNotFoundNamesTheRef(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	_, err := run(t, "sync", "python/astral/lib@1")
	if err == nil {
		t.Fatal("sync succeeded, want not-found error")
	}
	if !strings.Contains(err.Error(), "type python/astral/lib@1 not found in any configured registry") {
		t.Errorf("error = %v", err)
	}
	if _, err := os.Stat(lockPathIn(repo)); !os.IsNotExist(err) {
		t.Errorf("lock written despite failure (stat err = %v)", err)
	}
}

func TestSyncSidecarMismatch(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	f.corruptSidecar("cliwright/types", "python/astral/lib", 1)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	_, err := run(t, "sync", "python/astral/lib@1")
	if err == nil || !strings.Contains(err.Error(), "sidecar mismatch") {
		t.Fatalf("sync error = %v, want sidecar mismatch", err)
	}
	if _, err := os.Stat(lockPathIn(repo)); !os.IsNotExist(err) {
		t.Errorf("lock written despite mismatch (stat err = %v)", err)
	}
}

func TestSyncSecondRunUpToDate(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	if _, err := run(t, "sync", "python/astral/lib@1"); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	lockBefore, err := os.ReadFile(lockPathIn(repo))
	if err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "sync", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if !strings.Contains(out, "up to date") || !strings.Contains(out, "all types up to date") {
		t.Errorf("second sync output = %q, want up-to-date report", out)
	}
	lockAfter, err := os.ReadFile(lockPathIn(repo))
	if err != nil {
		t.Fatal(err)
	}
	if string(lockAfter) != string(lockBefore) {
		t.Errorf("second run rewrote the lock:\nbefore: %s\nafter: %s", lockBefore, lockAfter)
	}
	installedBefore, err := os.ReadFile(filepath.Join(installedDir(t, repo, "python/astral/lib@v1"), "project-type.json"))
	if err != nil {
		t.Fatal(err)
	}
	installedAfter, err := os.ReadFile(filepath.Join(installedDir(t, repo, "python/astral/lib@v1"), "project-type.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installedAfter) != string(installedBefore) {
		t.Error("second run rewrote the installed type")
	}
}

func TestSyncNoLockWhenAnyRefFails(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo":  {"demo", "python/astral/lib@1"},
		"libs/demo2": {"demo2", "python/astral/lib@2"},
	})
	t.Chdir(repo)

	_, err := run(t, "sync")
	if err == nil || !strings.Contains(err.Error(), "not found in any configured registry") {
		t.Fatalf("sync error = %v, want not-found for lib@2", err)
	}
	if _, err := os.Stat(lockPathIn(repo)); !os.IsNotExist(err) {
		t.Errorf("lock written despite failure (stat err = %v)", err)
	}
	// The successful fetch was staged in memory only — nothing installed.
	if _, err := os.Stat(installedDir(t, repo, "python/astral/lib@v1")); !os.IsNotExist(err) {
		t.Errorf("type installed despite later failure (stat err = %v)", err)
	}
}

func TestSyncNoRefsNoop(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	out, err := run(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(out, "no project types to sync") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(lockPathIn(repo)); !os.IsNotExist(err) {
		t.Errorf("lock written despite nothing to do (stat err = %v)", err)
	}
}

// TestSyncUnionsAttachmentTypes: repo_targets types join the manifest-derived
// fetch set and are pinned in the lock like any other type.
func TestSyncUnionsAttachmentTypes(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	f.serveType(t, "cliwright/types", "repo/uv/workspace", 1, `{"name":"repo/uv/workspace","version":1,"description":"uv workspace root environment","tools":["uv"],"targets":{"setup":["uv venv"]}}`)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"registries:\n" +
		"  - name: cliwright\n" +
		"    types: {git: https://github.com/cliwright/types}\n" +
		"    templates: {git: https://github.com/cliwright/templates}\n" +
		"workspace:\n  project_roots:\n    - libs\n" +
		"repo_targets:\n" +
		"  python:\n" +
		"    type: repo/uv/workspace@1\n"
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "libs", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nname: demo\ntype: python/astral/lib@1\n"
	if err := os.WriteFile(filepath.Join(repo, "libs", "demo", "smith.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, err := run(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}

	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	if lk.Types["python/astral/lib@1"].Registry != "cliwright" {
		t.Errorf("lib pin = %+v", lk.Types["python/astral/lib@1"])
	}
	attPin, ok := lk.Types["repo/uv/workspace@1"]
	if !ok || attPin.Registry != "cliwright" {
		t.Errorf("attachment pin = %+v (present %v)", attPin, ok)
	}
	if _, err := os.Stat(installedDir(t, repo, "repo/uv/workspace@v1")); err != nil {
		t.Errorf("attachment type not installed: %v", err)
	}
}

func TestSyncOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := run(t, "sync")
	if err == nil || !strings.Contains(err.Error(), "not a Smith repository") {
		t.Fatalf("sync error = %v, want not-a-repo", err)
	}
}

// writeToolsRepo creates an initialized repo whose repo.yml carries a
// comment (to prove the tools-union write preserves it) and an optional
// existing tools list.
func writeToolsRepo(t *testing.T, repo, toolsLine string) {
	t.Helper()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoYML := "version: 1\nname: mock\n" +
		"# Registry sources, in priority order: first match wins.\n" +
		"registries:\n" +
		"  - name: cliwright\n" +
		"    types: {git: https://github.com/cliwright/types}\n" +
		"    templates: {git: https://github.com/cliwright/templates}\n" +
		"workspace:\n  project_roots:\n    - libs\n" +
		toolsLine
	if err := os.WriteFile(filepath.Join(smithDir, "repo.yml"), []byte(repoYML), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncUnionsToolsIntoRepoYML(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeToolsRepo(t, repo, "")
	t.Chdir(repo)

	out, err := run(t, "sync", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "added tools: python, uv") {
		t.Errorf("output missing tools-union notice:\n%s", out)
	}

	cfg, err := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Equal(cfg.Tools, []string{"python", "uv"}) {
		t.Errorf("tools = %v, want [python uv]", cfg.Tools)
	}
	raw, err := os.ReadFile(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# Registry sources, in priority order: first match wins.") {
		t.Error("repo.yml lost its comments in the tools union")
	}

	// Second sync: everything up to date, repo.yml byte-identical.
	before, err := os.ReadFile(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "sync", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if !strings.Contains(out, "all types up to date") {
		t.Errorf("second sync output = %q", out)
	}
	after, err := os.ReadFile(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("second sync rewrote repo.yml:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestSyncToolsUnionSkipsExisting(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeToolsRepo(t, repo, "tools:\n  - python\n  - git\n")
	t.Chdir(repo)

	if _, err := run(t, "sync", "python/astral/lib@1"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	cfg, err := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Equal(cfg.Tools, []string{"python", "git", "uv"}) {
		t.Errorf("tools = %v, want [python git uv]", cfg.Tools)
	}
}

func TestSyncToolsUnionOnlyOnSuccess(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeToolsRepo(t, repo, "")
	t.Chdir(repo)

	_, err := run(t, "sync", "python/astral/lib@1")
	if err == nil {
		t.Fatal("sync succeeded, want not-found error")
	}
	cfg, loadErr := config.Load(filepath.Join(repo, ".smith", "repo.yml"))
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	if len(cfg.Tools) != 0 {
		t.Errorf("tools = %v, want untouched on failure", cfg.Tools)
	}
}

// TestSyncFetchErrorsSurfaceBeforeStaleLock: a repo whose on-disk lock no
// longer validates must still get the honest fetch error when a type is
// missing from every registry — the lock is only consulted for skip
// decisions and the final merge.
func TestSyncFetchErrorsSurfaceBeforeStaleLock(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	t.Chdir(repo)

	staleLock := `{"version":1,"sources":{"cliwright":{"types":{"git":"https://github.com/cliwright/smith-project-types"},"templates":{"git":"https://github.com/cliwright/smith-project-templates"}}},"types":{}}`
	if err := os.WriteFile(lockPathIn(repo), []byte(staleLock), 0o644); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(lockPathIn(repo))
	if err != nil {
		t.Fatal(err)
	}

	_, err = run(t, "sync")
	if err == nil || !strings.Contains(err.Error(), "not found in any configured registry") {
		t.Fatalf("sync error = %v, want the not-found error to surface first", err)
	}
	lockAfter, err := os.ReadFile(lockPathIn(repo))
	if err != nil {
		t.Fatal(err)
	}
	if string(lockAfter) != string(lockBefore) {
		t.Error("failed sync rewrote the stale lock")
	}
}

func TestSyncForceRefetchesAndRestoresInstalled(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	t.Chdir(repo)

	if _, err := run(t, "sync"); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Tamper with the unpacked copy — the tree now drifts from the ingot, so
	// even a plain sync would re-fetch; --force must re-download and restore.
	if err := os.WriteFile(filepath.Join(installedDir(t, repo, "python/astral/lib@v1"), "project-type.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "sync", "--force")
	if err != nil {
		t.Fatalf("sync --force: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "fetched from cliwright") || strings.Contains(out, "up to date") {
		t.Errorf("force output missing the re-fetch:\n%s", out)
	}
	restored, err := os.ReadFile(filepath.Join(installedDir(t, repo, "python/astral/lib@v1"), "project-type.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != testTypeContent {
		t.Errorf("installed content = %q, want the registry bytes restored", restored)
	}
}

func TestSyncForceRebuildsLockFromScratch(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	t.Chdir(repo)

	// Seed a lock pinning a type the repo no longer references.
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"python/astral/lib@1": {Registry: "cliwright", Hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
		"mock/stale/lib@1":    {Registry: "cliwright", Hash: "sha256:1111111111111111111111111111111111111111111111111111111111111111"},
	}}
	if err := lock.Save(lockPathIn(repo), lk); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "sync", "--force")
	if err != nil {
		t.Fatalf("sync --force: %v\noutput:\n%s", err, out)
	}
	rebuilt, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	if _, ok := rebuilt.Types["mock/stale/lib@1"]; ok {
		t.Errorf("stale pin survived the from-scratch rebuild: %v", rebuilt.Types)
	}
	pin, ok := rebuilt.Types["python/astral/lib@1"]
	if !ok {
		t.Fatalf("fresh pin missing after force rebuild")
	}
	if want := testTypeIngotHash(t); pin.Hash != want {
		t.Errorf("pin.Hash = %q, want the freshly fetched ingot hash %q", pin.Hash, want)
	}
}

func TestSyncForceRejectsTypeArguments(t *testing.T) {
	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	_, err := run(t, "sync", "--force", "python/astral/lib@1")
	if err == nil || !strings.Contains(err.Error(), "takes no type arguments") {
		t.Fatalf("error = %v, want the args guard", err)
	}
}

// TestSyncInstallsIngotForm: the installed type is a directory holding the
// ingot artifact and the unpacked tree (project-type.json plus scripts/).
// The registry sidecar stays in the registry.
func TestSyncInstallsIngotForm(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveTypeFiles(t, "cliwright/types", "python/astral/lib", 1, map[string]string{
		"project-type.json": testTypeContent,
		"scripts/build.sh":  "#!/bin/sh\necho build\n",
	})
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	t.Chdir(repo)

	if _, err := run(t, "sync"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	dir := installedDir(t, repo, "python/astral/lib@v1")
	artifact, err := os.ReadFile(filepath.Join(dir, "python-astral-lib@v1.ingot"))
	if err != nil {
		t.Fatalf("installed ingot artifact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "python-astral-lib@v1.ingot.sha256")); !os.IsNotExist(err) {
		t.Errorf("registry sidecar must not be installed (stat err = %v)", err)
	}
	script, err := os.ReadFile(filepath.Join(dir, "scripts", "build.sh"))
	if err != nil {
		t.Fatalf("unpacked script missing: %v", err)
	}
	if string(script) != "#!/bin/sh\necho build\n" {
		t.Errorf("unpacked script = %q", script)
	}
	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatal(err)
	}
	if pin := lk.Types["python/astral/lib@1"]; pin.Hash != ingot.Hash(artifact) {
		t.Errorf("pin.Hash = %q, want the installed ingot's hash", pin.Hash)
	}
}

// TestSyncMigratesLegacyFlatInstall: a pre-ingot flat <name>@v<N>.json is
// replaced by the directory form on the next sync.
func TestSyncMigratesLegacyFlatInstall(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	legacy := filepath.Join(repo, cacheDirName, "types", "python", "astral", "lib@v1.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(testTypeContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if _, err := run(t, "sync"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy flat install survived migration (stat err = %v)", err)
	}
	if _, err := os.Stat(installedDir(t, repo, "python/astral/lib@v1")); err != nil {
		t.Errorf("ingot form missing after migration: %v", err)
	}
}

// TestSyncForcePrunesUnreferencedTypes: --force is a clean slate — an
// installed type the repo no longer references is removed from .smith/types
// and dropped from the rebuilt lock.
func TestSyncForcePrunesUnreferencedTypes(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	// An installed type (and pin) the repo no longer references.
	staleRef := typeref.TypeRef{Name: "mock/stale/lib", Version: 1}
	staleHash := installIngotForTest(t, repo, staleRef, map[string]string{
		"project-type.json": `{"name":"mock/stale/lib","version":1,"description":"stale","tools":[],"targets":{"build":["true"]}}`,
	})
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"mock/stale/lib@1": {Registry: "cliwright", Hash: staleHash},
	}}
	if err := lock.Save(lockPathIn(repo), lk); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if _, err := run(t, "sync", "--force"); err != nil {
		t.Fatalf("sync --force: %v", err)
	}
	if _, err := os.Stat(installedDir(t, repo, "mock/stale/lib@v1")); !os.IsNotExist(err) {
		t.Errorf("unreferenced type survived --force (stat err = %v)", err)
	}
	rebuilt, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rebuilt.Types["mock/stale/lib@1"]; ok {
		t.Errorf("stale pin survived --force: %v", rebuilt.Types)
	}
	if _, err := os.Stat(installedDir(t, repo, "python/astral/lib@v1")); err != nil {
		t.Errorf("referenced type missing after --force: %v", err)
	}
	if pin := rebuilt.Types["python/astral/lib@1"]; pin.Hash != testTypeIngotHash(t) {
		t.Errorf("pin.Hash = %q, want the freshly fetched ingot hash", pin.Hash)
	}
}

// TestSyncForceWipesLegacyFlatInstalls: --force clears .smith/types entirely,
// including pre-ingot flat files that a plain sync would only migrate per
// referenced type.
func TestSyncForceWipesLegacyFlatInstalls(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	stray := filepath.Join(repo, cacheDirName, "types", "mock", "stray", "lib@v9.json")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if _, err := run(t, "sync", "--force"); err != nil {
		t.Fatalf("sync --force: %v", err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("stray legacy install survived --force (stat err = %v)", err)
	}
}

// writeLocalType lays out a smelted type in a local registry root:
// <root>/<name>/v<N>/<flat>.ingot + .sha256 — exactly what
// "smith new smith/type" + "smith smelt" leave in a repo's types/ tree.
func writeLocalType(t *testing.T, root, name string, version int, content string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(name), fmt.Sprintf("v%d", version))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ingotBytes := packFilesForTest(t, map[string]string{"project-type.json": content})
	artifact := filepath.Join(dir, ingot.FileName(name, version))
	if err := os.WriteFile(artifact, ingotBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := fmt.Sprintf("%s  %s\n",
		strings.TrimPrefix(ingot.Hash(ingotBytes), "sha256:"), ingot.FileName(name, version))
	if err := os.WriteFile(artifact+".sha256", []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncFetchesFromLocalPathRegistry(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f) // the HTTP registry must never be contacted

	repo := t.TempDir()
	writeSyncRepo(t, repo, []regEntry{
		{"local", "path: types"},
		{"cliwright", "git: https://github.com/cliwright/types"},
	}, nil)
	writeLocalType(t, filepath.Join(repo, "types"), "python/acme/lib", 1, testTypeContent)
	t.Chdir(repo)

	out, err := run(t, "sync", "python/acme/lib@1")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "fetched from local") {
		t.Errorf("output missing local-fetch line:\n%s", out)
	}
	if got := atomic.LoadInt32(&f.hits); got != 0 {
		t.Errorf("HTTP registry got %d request(s), want 0 (local hit short-circuits)", got)
	}

	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	pin := lk.Types["python/acme/lib@1"]
	if pin.Registry != "local" {
		t.Errorf("pin.Registry = %q, want local", pin.Registry)
	}
	if pin.Hash != testTypeIngotHash(t) {
		t.Errorf("pin.Hash = %q, want the ingot hash %q", pin.Hash, testTypeIngotHash(t))
	}
	if _, err := os.Stat(installedDir(t, repo, "python/acme/lib@v1")); err != nil {
		t.Errorf("installed type missing: %v", err)
	}
}

func TestSyncLocalMissFallsBackToHTTPRegistry(t *testing.T) {
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/acme/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, []regEntry{
		{"local", "path: types"}, // empty tree: the type is not local
		{"cliwright", "git: https://github.com/cliwright/types"},
	}, nil)
	if err := os.MkdirAll(filepath.Join(repo, "types"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, err := run(t, "sync", "python/acme/lib@1")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "fetched from cliwright") {
		t.Errorf("output missing fallback line:\n%s", out)
	}
}

func TestSyncLocalNotFoundAnywhere(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, []regEntry{
		{"local", "path: types"},
		{"cliwright", "git: https://github.com/cliwright/types"},
	}, nil)
	t.Chdir(repo)

	_, err := run(t, "sync", "python/acme/lib@1")
	if err == nil || !strings.Contains(err.Error(), "not found in any configured registry") {
		t.Fatalf("sync error = %v, want not-found-anywhere", err)
	}
	if got := atomic.LoadInt32(&f.hits); got == 0 {
		t.Error("HTTP registry was never tried")
	}
}

func TestRegistryBasesResolvesRelativePathsAgainstRepoRoot(t *testing.T) {
	cfg := &config.RepoConfig{Registries: []config.Registry{
		{Name: "rel", Types: config.Source{Path: "types"}, Templates: config.Source{Git: "https://example.com/t"}},
		{Name: "abs", Types: config.Source{Path: "/var/types"}, Templates: config.Source{Git: "https://example.com/t"}},
		{Name: "git", Types: config.Source{Git: "https://github.com/cliwright/types"}, Templates: config.Source{Git: "https://example.com/t"}},
	}}
	bases, err := registryBases(cfg, "/repo")
	if err != nil {
		t.Fatalf("registryBases: %v", err)
	}
	if len(bases) != 3 {
		t.Fatalf("bases = %d, want 3", len(bases))
	}
	if bases[0].local != filepath.Join("/repo", "types") {
		t.Errorf("relative path resolved to %q, want %q", bases[0].local, filepath.Join("/repo", "types"))
	}
	if bases[1].local != "/var/types" {
		t.Errorf("absolute path became %q", bases[1].local)
	}
	if bases[2].base == "" || bases[2].local != "" {
		t.Errorf("git registry = %+v, want HTTP base only", bases[2])
	}
}

// TestSyncFetchesMultiTypeManifestRefs: a manifest listing several type refs
// (array form) puts every ref in the fetch set — each is installed and
// pinned exactly as a single-type manifest's ref is.
func TestSyncFetchesMultiTypeManifestRefs(t *testing.T) {
	const docsTypeContent = `{"name":"mock/std/docs","version":1,"description":"docs builder","tools":["sh"],"targets":{"docs":["mkdocs build"]}}`
	f := newFakeRegistries(t)
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	f.serveType(t, "cliwright/types", "mock/std/docs", 1, docsTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	alpha := filepath.Join(repo, "libs", "alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	multi := "version: 1\nname: alpha\ntype: [python/astral/lib@1, mock/std/docs@1]\n"
	if err := os.WriteFile(filepath.Join(alpha, "smith.yml"), []byte(multi), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, err := run(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}

	lk, err := lock.Load(lockPathIn(repo))
	if err != nil {
		t.Fatalf("lock.Load: %v", err)
	}
	for _, ref := range []string{"python/astral/lib@1", "mock/std/docs@1"} {
		if _, ok := lk.Types[ref]; !ok {
			t.Errorf("lock missing pin for %s", ref)
		}
	}
	for _, dir := range []string{"python/astral/lib@v1", "mock/std/docs@v1"} {
		if _, err := os.Stat(filepath.Join(repo, ".smith", "types", filepath.FromSlash(dir), "project-type.json")); err != nil {
			t.Errorf("installed type missing: %s (%v)", dir, err)
		}
	}
	if !strings.Contains(out, "mock/std/docs@1: fetched from cliwright") {
		t.Errorf("sync output missing the second ref's fetch line:\n%s", out)
	}
}
