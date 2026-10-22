package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cliwright/smith/internal/lock"
	"github.com/cliwright/smith/internal/manifest"
	"github.com/cliwright/smith/internal/projecttype"
	"github.com/cliwright/smith/internal/trust"
	"github.com/cliwright/smith/internal/typeref"
)

// markerPathFor resolves ref's trust marker file the way the trust package
// does (tests delete or rewrite it to stage untrusted/stale states).
func markerPathFor(repo string, ref typeref.TypeRef) string {
	return filepath.Join(trust.Dir(repo), typeref.FlatName(ref)+".trusted")
}

// removeTrustMarker deletes ref's marker, leaving the repo's other markers
// intact — the way to make one type untrusted.
func removeTrustMarker(t *testing.T, repo string, ref typeref.TypeRef) {
	t.Helper()
	if err := os.Remove(markerPathFor(repo, ref)); err != nil {
		t.Fatal(err)
	}
}

// untrustedSyncRepo builds a repo whose single type installs from a fake
// remote git registry via "smith sync" — which never auto-trusts.
func untrustedSyncRepo(t *testing.T, f *fakeRegistries) string {
	t.Helper()
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, testTypeContent)
	repo := t.TempDir()
	writeSyncRepo(t, repo, testRegs, map[string][2]string{
		"libs/demo": {"demo", "python/astral/lib@1"},
	})
	t.Chdir(repo)
	if _, err := run(t, "sync"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return repo
}

func TestGateRefusesUntrustedType(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)
	untrustedSyncRepo(t, f)

	fake := &fakeExec{}
	useFakeExec(t, fake)

	// The namespace-scoped and repo-wide readings of the same run both
	// funnel through the gate.
	for _, args := range [][]string{{"libs", "demo", "build"}, {"build"}} {
		_, err := run(t, args...)
		if err == nil {
			t.Fatalf("%v: want the trust gate refusal, got nil", args)
		}
		for _, want := range []string{
			"smith: refusing to run: 1 type(s) not trusted:",
			`python/astral/lib@1  (untrusted — installed from registry "cliwright" via github)`,
			"inspect: smith <ns> <name> help   trust: smith trust <ref>   (or smith trust --all)",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v error missing %q:\n%s", args, want, err)
			}
		}
	}
	if len(fake.runs) != 0 {
		t.Errorf("%d steps ran despite the gate, want 0", len(fake.runs))
	}
}

func TestGateAllowsTrustedType(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)
	untrustedSyncRepo(t, f)

	out, err := run(t, "trust", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("trust: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "python/astral/lib@1: trusted (by ") {
		t.Errorf("trust output missing the confirmation:\n%s", out)
	}

	lk, err := lock.Load(lockPathIn(mustGetwd(t)))
	if err != nil {
		t.Fatal(err)
	}
	ref := typeref.TypeRef{Name: "python/astral/lib", Version: 1}
	status, _, err := trust.Check(mustGetwd(t), ref, lk.Types[ref.String()].Hash)
	if err != nil || status != trust.Trusted {
		t.Fatalf("marker status = %v, err = %v; want Trusted", status, err)
	}

	fake := &fakeExec{}
	useFakeExec(t, fake)
	out, err = run(t, "libs", "demo", "build")
	if err != nil {
		t.Fatalf("run after trusting: %v\noutput:\n%s", err, out)
	}
	if got := fmt.Sprintf("%v", fake.runs); got != "[uv build]" {
		t.Errorf("steps = %s, want [uv build]", got)
	}
}

func TestGateRefusesStaleType(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)
	repo := untrustedSyncRepo(t, f)

	if _, err := run(t, "trust", "python/astral/lib@1"); err != nil {
		t.Fatalf("trust: %v", err)
	}
	ref := typeref.TypeRef{Name: "python/astral/lib", Version: 1}

	// Publisher churn: the same ref now serves different bytes. --force
	// re-downloads (the up-to-date shortcut would keep the old content);
	// the marker survives the wipe but no longer matches the pin.
	const churned = `{"name":"python/astral/lib","version":1,"description":"churned","tools":["python","uv"],"targets":{"build":["uv build"]}}`
	f.serveType(t, "cliwright/types", "python/astral/lib", 1, churned)
	if _, err := run(t, "sync", "--force"); err != nil {
		t.Fatalf("sync --force: %v", err)
	}
	if _, err := os.Stat(markerPathFor(repo, ref)); err != nil {
		t.Fatalf("marker did not survive sync --force: %v", err)
	}

	fake := &fakeExec{}
	useFakeExec(t, fake)
	_, err := run(t, "libs", "demo", "build")
	if err == nil {
		t.Fatal("stale marker: want the trust gate refusal, got nil")
	}
	for _, want := range []string{
		"smith: refusing to run: 1 type(s) not trusted:",
		"python/astral/lib@1  (stale — content changed since it was trusted; re-vet and re-trust)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err)
		}
	}
	if len(fake.runs) != 0 {
		t.Errorf("%d steps ran despite the gate, want 0", len(fake.runs))
	}
}

func TestGateBypassed(t *testing.T) {
	f := newFakeRegistries(t)
	useFakeRegistries(t, f)
	untrustedSyncRepo(t, f)
	t.Setenv("SMITH_AUTO_TRUST", "1")

	fake := &fakeExec{}
	useFakeExec(t, fake)
	out, err := run(t, "libs", "demo", "build")
	if err != nil {
		t.Fatalf("bypassed run: %v\noutput:\n%s", err, out)
	}
	if len(fake.runs) != 1 {
		t.Errorf("steps = %v, want the one build step to run", fake.runs)
	}
}

func TestGateCoversDependencyClosure(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/app": {name: "app", typ: "mock/std/lib@1",
			deps: []manifest.Dependency{{Project: "db", Targets: []string{"build"}, Before: []string{"build"}}}},
		"libs/db": {name: "db", typ: "mock/std/other@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1":   {targets: map[string][]string{"build": {"echo app-build"}}},
		"mock/std/other@1": {targets: map[string][]string{"build": {"echo db-build"}}},
	})
	t.Chdir(repo)
	// The dependency's type is untrusted while the root project's own type
	// stays trusted: the closure is gated, not just the roots.
	removeTrustMarker(t, repo, typeref.TypeRef{Name: "mock/std/other", Version: 1})

	fake := &fakeExec{}
	useFakeExec(t, fake)
	_, err := run(t, "libs", "app", "build")
	if err == nil {
		t.Fatal("dependency-closure run: want the trust gate refusal, got nil")
	}
	if !strings.Contains(err.Error(), "mock/std/other@1") || !strings.Contains(err.Error(), "untrusted") {
		t.Errorf("error = %v, want it naming the dependency's untrusted type", err)
	}
	if len(fake.runs) != 0 {
		t.Errorf("%d steps ran despite the gate, want 0", len(fake.runs))
	}
}

func TestInspectionCommandsWorkUntrusted(t *testing.T) {
	repo := t.TempDir()
	writeDispatchRepo(t, repo, map[string]manifestSpec{
		"libs/demo": {name: "demo", typ: "mock/std/lib@1"},
	}, map[string]typeSpec{
		"mock/std/lib@1": {targets: map[string][]string{"build": {"echo build"}}},
	})
	t.Chdir(repo)
	if err := os.RemoveAll(trust.Dir(repo)); err != nil {
		t.Fatal(err)
	}

	// Inspecting is part of deciding: none of these execute a plan, so the
	// gate never blocks them, and the untrusted repo must stay usable.
	if out, err := run(t, "libs", "demo", "list"); err != nil || !strings.Contains(out, "build") {
		t.Errorf("list: err = %v, out = %q", err, out)
	}
	if out, err := run(t, "tree"); err != nil || !strings.Contains(out, "demo") {
		t.Errorf("tree: err = %v, out = %q", err, out)
	}
	if out, err := run(t, "libs", "demo", "build", "help"); err != nil || out == "" {
		t.Errorf("target help: err = %v, out = %q", err, out)
	}
	if out, err := run(t, "libs", "demo", "build", "--context"); err != nil || out == "" {
		t.Errorf("--context: err = %v, out = %q", err, out)
	}
	if out, err := run(t, "types"); err != nil || !strings.Contains(out, "untrusted") {
		t.Errorf("types: err = %v, out missing the trust annotation:\n%s", err, out)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

// trustCmdRepo is an initialized repo with the standard types installed
// and pinned, no markers written.
func trustCmdRepo(t *testing.T) (string, map[string]string) {
	t.Helper()
	repo := buildMockRepo(t, []string{"libs"}, nil)
	hashes := installStandardTypes(t, repo)
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"python/astral/lib@1": {Registry: "r", Hash: hashes["python/astral/lib@1"]},
		"python/astral/lib@2": {Registry: "r", Hash: hashes["python/astral/lib@2"]},
		"repo/uv/workspace@1": {Registry: "r", Hash: hashes["repo/uv/workspace@1"]},
	}}
	if err := lock.Save(lockPathIn(repo), lk); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	return repo, hashes
}

func TestTrustOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := run(t, "trust")
	if err == nil || !strings.Contains(err.Error(), "not a Smith repository") {
		t.Fatalf("trust error = %v, want not-a-repo", err)
	}
}

func TestTrustListEmpty(t *testing.T) {
	repo := buildMockRepo(t, []string{"libs"}, nil)
	t.Chdir(repo)
	out, err := run(t, "trust")
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	if !strings.Contains(out, "no project types installed") {
		t.Errorf("output = %q, want the empty-cache suggestion", out)
	}
}

func TestTrustListStatuses(t *testing.T) {
	repo, hashes := trustCmdRepo(t)
	lib1 := typeref.TypeRef{Name: "python/astral/lib", Version: 1}
	lib2 := typeref.TypeRef{Name: "python/astral/lib", Version: 2}
	m, err := trust.Write(repo, lib1, hashes["python/astral/lib@1"])
	if err != nil {
		t.Fatal(err)
	}
	// A stale marker: recorded against content that is no longer installed.
	if _, err := trust.Write(repo, lib2, "sha256:0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "trust")
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	want := fmt.Sprintf("python/astral/lib@1  trusted (by %s at %s)\n"+
		"python/astral/lib@2  stale — content changed\n"+
		"repo/uv/workspace@1  untrusted\n", m.User, m.Time)
	if out != want {
		t.Errorf("listing mismatch:\n%s\nwant:\n%s", out, want)
	}
}

func TestTrustListBypassNote(t *testing.T) {
	trustCmdRepo(t)
	t.Setenv("SMITH_AUTO_TRUST", "1")
	out, err := run(t, "trust")
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	if !strings.HasPrefix(out, "SMITH_AUTO_TRUST=1 — trust model bypassed\n") {
		t.Errorf("listing missing the bypass note:\n%s", out)
	}
}

func TestTrustOne(t *testing.T) {
	repo, hashes := trustCmdRepo(t)
	out, err := run(t, "trust", "python/astral/lib@1")
	if err != nil {
		t.Fatalf("trust: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "python/astral/lib@1: trusted (by ") {
		t.Errorf("output missing the confirmation:\n%s", out)
	}
	status, _, err := trust.Check(repo, typeref.TypeRef{Name: "python/astral/lib", Version: 1}, hashes["python/astral/lib@1"])
	if err != nil || status != trust.Trusted {
		t.Errorf("marker status = %v, err = %v; want Trusted against the pin", status, err)
	}
}

func TestTrustOneUnpinned(t *testing.T) {
	trustCmdRepo(t)
	_, err := run(t, "trust", "python/astral/lib@9")
	if err == nil || !strings.Contains(err.Error(), "not pinned in "+filepath.Join(cacheDirName, lockJSONName)) ||
		!strings.Contains(err.Error(), "run smith sync") {
		t.Fatalf("error = %v, want the not-pinned refusal", err)
	}
}

func TestTrustOneCustodyFailureRefused(t *testing.T) {
	repo, hashes := trustCmdRepo(t)
	// Pin lib@2 with the wrong hash: the custody check must refuse.
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"python/astral/lib@2": {Registry: "r", Hash: hashes["python/astral/lib@1"]},
	}}
	if err := lock.Save(lockPathIn(repo), lk); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "trust", "python/astral/lib@2")
	if err == nil || !strings.Contains(err.Error(), "refusing to trust content that fails the custody check") ||
		!strings.Contains(err.Error(), "run smith sync") {
		t.Fatalf("error = %v, want the custody refusal", err)
	}
}

func TestTrustOneBadRef(t *testing.T) {
	trustCmdRepo(t)
	_, err := run(t, "trust", "nope")
	if err == nil || !strings.Contains(err.Error(), "invalid type ref") {
		t.Fatalf("error = %v, want the grammar error", err)
	}
}

func TestTrustArgsValidation(t *testing.T) {
	trustCmdRepo(t)
	_, err := run(t, "trust", "python/astral/lib@1", "python/astral/lib@2")
	if err == nil || !strings.Contains(err.Error(), "trust takes at most one type ref") {
		t.Fatalf("two refs: error = %v, want the arity error", err)
	}
	_, err = run(t, "trust", "python/astral/lib@1", "--all")
	if err == nil || !strings.Contains(err.Error(), "--all takes no type ref") {
		t.Fatalf("ref with --all: error = %v, want the mutual-exclusion error", err)
	}
}

func TestTrustAll(t *testing.T) {
	repo, hashes := trustCmdRepo(t)
	out, err := run(t, "trust", "--all")
	if err != nil {
		t.Fatalf("trust --all: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "trusted 3 of 3 installed type(s)") {
		t.Errorf("output missing the summary:\n%s", out)
	}
	for refKey, hash := range hashes {
		ref, err := typeref.Parse(refKey)
		if err != nil {
			t.Fatal(err)
		}
		status, _, err := trust.Check(repo, ref, hash)
		if err != nil || status != trust.Trusted {
			t.Errorf("%s marker status = %v, err = %v; want Trusted", refKey, status, err)
		}
	}
}

func TestTrustAllSkipsBroken(t *testing.T) {
	repo, hashes := trustCmdRepo(t)
	// lib@2: pinned, but the unpacked tree drifts from the ingot. The
	// tampered copy stays schema-valid (scan must still load it) while
	// differing from the packed bytes, so the custody check is what fails.
	pt := libTypeV2()
	pt.Description = "tampered after install"
	data, err := projecttype.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installedDir(t, repo, "python/astral/lib@v2"), "project-type.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// workspace@1: drop its pin.
	lk := &lock.Lock{Version: 1, Types: map[string]lock.TypePin{
		"python/astral/lib@1": {Registry: "r", Hash: hashes["python/astral/lib@1"]},
		"python/astral/lib@2": {Registry: "r", Hash: hashes["python/astral/lib@2"]},
	}}
	if err := lock.Save(lockPathIn(repo), lk); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "trust", "--all")
	if err != nil {
		t.Fatalf("trust --all: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"python/astral/lib@1: trusted (by ",
		"warning: python/astral/lib@2:",
		"run smith sync (skipped)",
		"warning: repo/uv/workspace@1: not pinned",
		"trusted 1 of 3 installed type(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	status, _, err := trust.Check(repo, typeref.TypeRef{Name: "python/astral/lib", Version: 1}, hashes["python/astral/lib@1"])
	if err != nil || status != trust.Trusted {
		t.Errorf("lib@1 marker status = %v, err = %v; want Trusted", status, err)
	}
}
