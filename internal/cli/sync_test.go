package cli

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/lock"
)

const testTypeContent = `{"name":"python/astral/lib","version":1,"description":"Python library on the Astral toolchain","tools":["python"],"targets":{"build":["uv build"]}}`

// fakeRegistries is an httptest server pretending to be raw.githubusercontent.com.
type fakeRegistries struct {
	server *httptest.Server
	store  map[string][]byte
}

func newFakeRegistries(t *testing.T) *fakeRegistries {
	t.Helper()
	f := &fakeRegistries{store: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

// serveType publishes a type version with a correct sidecar under
// /<orgRepo>/<name>/v<version>/project-type.json.
func (f *fakeRegistries) serveType(orgRepo, name string, version int, content string) {
	base := fmt.Sprintf("/%s/%s/v%d/project-type.json", orgRepo, name, version)
	f.store[base] = []byte(content)
	sum := sha256.Sum256([]byte(content))
	f.store[base+".sha256"] = []byte(fmt.Sprintf("%x  project-type.json\n", sum[:]))
}

func (f *fakeRegistries) corruptSidecar(orgRepo, name string, version int) {
	base := fmt.Sprintf("/%s/%s/v%d/project-type.json", orgRepo, name, version)
	f.store[base+".sha256"] = []byte("0000000000000000000000000000000000000000000000000000000000000000  project-type.json\n")
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

type regEntry struct{ name, git string }

// writeSyncRepo writes an initialized repo with the given registries (in
// repo.yml priority order) and manifests (dir -> {project name, type ref}).
func writeSyncRepo(t *testing.T, repo string, regs []regEntry, manifests map[string][2]string) {
	t.Helper()
	smithDir := filepath.Join(repo, ".smith")
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("version: 1\nname: mock\nregistries:\n")
	for _, reg := range regs {
		fmt.Fprintf(&b, "  - name: %s\n    types: {git: %s}\n    templates: {git: https://example.com/templates}\n", reg.name, reg.git)
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

func installedPath(home, ref string) string {
	return filepath.Join(home, cacheDirName, "project-types", filepath.FromSlash(ref)+".json")
}

var testRegs = []regEntry{
	{"cliwright", "https://github.com/cliwright/types"},
	{"acme", "https://github.com/acme/types"},
}

func TestSyncFetchesManifestRefs(t *testing.T) {
	home := setupHome(t)
	f := newFakeRegistries(t)
	f.serveType("cliwright/types", "python/astral/lib", 1, testTypeContent)
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

	installed, err := os.ReadFile(installedPath(home, "python/astral/lib@v1"))
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
	sum := sha256.Sum256([]byte(testTypeContent))
	if want := fmt.Sprintf("sha256:%x", sum[:]); pin.Hash != want {
		t.Errorf("pin.Hash = %q, want %q", pin.Hash, want)
	}

	for _, want := range []string{"fetched from cliwright", "lock written"} {
		if !strings.Contains(out, want) {
			t.Errorf("sync output missing %q:\n%s", want, out)
		}
	}
}

func TestSyncExplicitRefs(t *testing.T) {
	home := setupHome(t)
	f := newFakeRegistries(t)
	f.serveType("cliwright/types", "python/astral/lib", 1, testTypeContent)
	useFakeRegistries(t, f)

	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, nil)
	t.Chdir(repo)

	out, err := run(t, "sync", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("sync: %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(installedPath(home, "python/astral/lib@v1")); err != nil {
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
	home := setupHome(t)
	f := newFakeRegistries(t)
	// cliwright does not serve the type; acme does.
	f.serveType("acme/types", "python/astral/lib", 1, testTypeContent)
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
	if _, err := os.Stat(installedPath(home, "python/astral/lib@v1")); err != nil {
		t.Errorf("installed type missing: %v", err)
	}
}

func TestSyncNotFoundNamesTheRef(t *testing.T) {
	setupHome(t)
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
	setupHome(t)
	f := newFakeRegistries(t)
	f.serveType("cliwright/types", "python/astral/lib", 1, testTypeContent)
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
	home := setupHome(t)
	f := newFakeRegistries(t)
	f.serveType("cliwright/types", "python/astral/lib", 1, testTypeContent)
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
	installedBefore, err := os.ReadFile(installedPath(home, "python/astral/lib@v1"))
	if err != nil {
		t.Fatal(err)
	}
	installedAfter, err := os.ReadFile(installedPath(home, "python/astral/lib@v1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installedAfter) != string(installedBefore) {
		t.Error("second run rewrote the installed type")
	}
}

func TestSyncNoLockWhenAnyRefFails(t *testing.T) {
	home := setupHome(t)
	f := newFakeRegistries(t)
	f.serveType("cliwright/types", "python/astral/lib", 1, testTypeContent)
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
	if _, err := os.Stat(installedPath(home, "python/astral/lib@v1")); !os.IsNotExist(err) {
		t.Errorf("type installed despite later failure (stat err = %v)", err)
	}
}

func TestSyncNoRefsNoop(t *testing.T) {
	setupHome(t)
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

func TestSyncOutsideRepo(t *testing.T) {
	setupHome(t)
	t.Chdir(t.TempDir())
	_, err := run(t, "sync")
	if err == nil || !strings.Contains(err.Error(), "not a Smith repository") {
		t.Fatalf("sync error = %v, want not-a-repo", err)
	}
}

// TestSyncFetchErrorsSurfaceBeforeStaleLock: a repo whose on-disk lock no
// longer validates must still get the honest fetch error when a type is
// missing from every registry — the lock is only consulted for skip
// decisions and the final merge.
func TestSyncFetchErrorsSurfaceBeforeStaleLock(t *testing.T) {
	setupHome(t)
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
