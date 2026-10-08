// Package sync fetches project types from registries over plain HTTP.
//
// Transport is deliberately minimal: a registry source of the form
// {git: "https://github.com/<org>/<repo>"} maps to the raw-content base
// https://raw.githubusercontent.com/<org>/<repo>/main, and each type costs
// exactly two GETs — <type>/v<N>/project-type.json and its checked-in
// .sha256 sidecar. There is no git clone, no GitHub API, no commit-SHA
// pinning, and no catalog listing: GitHub-only, main-is-truth, and a type is
// immutable by policy (republishing a change means a new version).
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/cliwright/smith/internal/typeref"
)

// ErrNotFound is returned (wrapped) when a registry answers 404 for a type —
// the caller is expected to try the next registry in priority order.
var ErrNotFound = errors.New("type not found in registry")

// githubPrefix is the only supported registry URL shape.
const githubPrefix = "https://github.com/"

// RawBase maps a registry git URL to its raw-content base URL. Only
// github.com is supported for now.
func RawBase(gitURL string) (string, error) {
	if !strings.HasPrefix(gitURL, githubPrefix) {
		return "", fmt.Errorf("registry %q is not a GitHub URL (github.com only for now)", gitURL)
	}
	return "https://raw.githubusercontent.com/" + strings.TrimPrefix(gitURL, githubPrefix) + "/main", nil
}

// FetchedType is one verified project type download.
type FetchedType struct {
	Content []byte
	Hash    string // "sha256:<hex>", as recorded in the lock
}

// Fetch downloads and verifies one type version from a raw-content base URL:
// the project-type.json body and its .sha256 sidecar, which must hash the
// body. A 404 on either GET yields an ErrNotFound wrap; a sidecar that does
// not match the body is a hard error (it also catches a push landing between
// the two GETs).
func Fetch(ctx context.Context, client *http.Client, base string, ref typeref.TypeRef) (*FetchedType, error) {
	dir := fmt.Sprintf("%s/v%d/project-type.json", ref.Name, ref.Version)
	content, err := get(ctx, client, base+"/"+dir)
	if err != nil {
		return nil, fmt.Errorf("type %s: %w", ref, err)
	}
	sidecar, err := get(ctx, client, base+"/"+dir+".sha256")
	if err != nil {
		return nil, fmt.Errorf("type %s: %w", ref, err)
	}

	want, err := parseSidecar(sidecar)
	if err != nil {
		return nil, fmt.Errorf("type %s: registry sidecar is malformed: %w", ref, err)
	}
	sum := sha256.Sum256(content)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return nil, fmt.Errorf("type %s: sidecar mismatch (registry content changed mid-fetch; retry, and if it persists the registry is broken)", ref)
	}
	return &FetchedType{Content: content, Hash: "sha256:" + got}, nil
}

var sidecarHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseSidecar reads a sha256sum-format sidecar: "<64-hex>  project-type.json".
func parseSidecar(data []byte) (string, error) {
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[1] != "project-type.json" || !sidecarHashPattern.MatchString(fields[0]) {
		return "", fmt.Errorf("want %q, got %q", "<sha256>  project-type.json", strings.TrimSpace(string(data)))
	}
	return fields[0], nil
}

// get is a bare GET that treats 404 as ErrNotFound and any other non-2xx as
// an error.
func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", url, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}
