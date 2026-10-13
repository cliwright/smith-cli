// Package ingot packs and unpacks project-type ingots: the published
// artifact for one type version. An ingot is a tar archive named
// <name>@v<N>.ingot holding project-type.json plus the type's scripts/ and
// help/ directories, published next to its checked-in .sha256 sidecar, which
// hashes the whole artifact. Sync costs two GETs (ingot + sidecar), the
// sidecar→lock→installed chain of custody is unchanged, and the lock pins
// the ingot's hash. Ingots are self-contained: no references across ingots.
package ingot

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// refFormat is the "<name>@v<N>" base every ingot artifact derives from.
const refFormat = "%s@v%d"

// RefName is the canonical "<name>@v<N>" base name of a type version: the
// installed directory name under .smith/types and the ingot name stem.
func RefName(name string, version int) string {
	return fmt.Sprintf(refFormat, name, version)
}

// FileName is the artifact file name: "<name>@v<N>.ingot" with slashes
// flattened to dashes, so the ingot is a single file directly inside its
// version directory (both in the registry and in the installed type
// directory).
func FileName(name string, version int) string {
	return strings.ReplaceAll(RefName(name, version), "/", "-") + ".ingot"
}

// SidecarName is the sidecar file name: "<flat-name>.ingot.sha256".
func SidecarName(name string, version int) string {
	return FileName(name, version) + ".sha256"
}

// List returns the sorted slash-relative names of the regular files in an
// ingot — the manifest a pack command prints.
func List(ingotBytes []byte) ([]string, error) {
	entries, err := entries(ingotBytes)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Hash renders the sha256 of data in the lock's "sha256:<hex>" form.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// zeroTime is every packed file's mtime: a fixed, pre-ordained instant so two
// packs of the same content are byte-identical.
var zeroTime = time.Unix(0, 0).UTC()

// excludedSuffixes are never packed: sidecars (the publisher's attestation
// stays in the registry) and the published artifacts themselves, so repacking
// a directory that already contains them stays self-consistent.
var excludedSuffixes = []string{".sha256", ".ingot"}

// Pack builds the ingot for the type version rooted at dir (a registry-layout
// directory holding project-type.json, optionally scripts/ and help/). The
// archive is reproducible: entries in sorted order, fixed mtimes, no owners,
// and permissions normalized to 0644/0755 — packing the same tree twice
// yields the same bytes, so the sidecar over the artifact is stable.
func Pack(dir string) ([]byte, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("pack: %s is not a regular file (symlinks and devices cannot ship in an ingot)", path)
		}
		for _, suffix := range excludedSuffixes {
			if strings.HasSuffix(path, suffix) {
				return nil
			}
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pack: scanning %s: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("pack: %s contains no files", dir)
	}
	sort.Strings(files)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, path := range files {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil, fmt.Errorf("pack: %s: %w", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("pack: reading %s: %w", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("pack: stat %s: %w", path, err)
		}
		mode := fs.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     filepath.ToSlash(rel),
			Mode:     int64(mode),
			Size:     int64(len(data)),
			ModTime:  zeroTime,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("pack: writing header for %s: %w", rel, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("pack: writing %s: %w", rel, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("pack: finalizing archive: %w", err)
	}
	return buf.Bytes(), nil
}

// unpackLimit bounds the total unpacked payload as a sanity check against a
// corrupt or hostile archive; type content is small by design.
const unpackLimit = 100 << 20

// Unpack extracts ingot into destDir, creating it if needed. Entry names are
// validated to stay inside destDir, and only regular files and directories
// are accepted — an ingot carries no symlinks or devices.
func Unpack(ingot []byte, destDir string) error {
	tr := tar.NewReader(bytes.NewReader(ingot))
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("unpack: reading archive: %w", err)
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unpack: entry %q escapes the destination", hdr.Name)
		}
		target := filepath.Join(destDir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("unpack: %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > unpackLimit {
				return fmt.Errorf("unpack: archive exceeds %d bytes", unpackLimit)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("unpack: %s: %w", hdr.Name, err)
			}
			mode := fs.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("unpack: creating %s: %w", hdr.Name, err)
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return fmt.Errorf("unpack: writing %s: %w", hdr.Name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("unpack: closing %s: %w", hdr.Name, closeErr)
			}
		default:
			return fmt.Errorf("unpack: entry %q has unsupported type %q (ingots carry files and directories only)", hdr.Name, hdr.Typeflag)
		}
	}
}

// VerifyInstalled checks the installed form of one type — the directory
// .smith/types/<name>@v<N> — against the lock's pinned hash: the ingot must
// be present and hash-match, and every unpacked file must byte-match (and
// exec-match) the ingot's entries. The ingot is identified as the single
// *.ingot regular file directly inside dir. The failure modes are distinct
// so the CLI can point at smith sync precisely.
func VerifyInstalled(dir, wantHash string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "*.ingot"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("installed type in %s has no ingot; run smith sync", dir)
	}
	if len(matches) > 1 {
		return fmt.Errorf("installed type in %s has multiple ingots (%s); run smith sync", dir, filepath.Base(matches[0]))
	}
	ingotPath := matches[0]
	ingotBytes, err := os.ReadFile(ingotPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ingotPath, err)
	}
	if got := Hash(ingotBytes); got != wantHash {
		return fmt.Errorf("installed ingot %s failed hash verification; run smith sync", ingotPath)
	}

	expected, err := entries(ingotBytes)
	if err != nil {
		return fmt.Errorf("installed ingot %s: %w", ingotPath, err)
	}
	actual, err := entriesOnDisk(dir, ingotPath)
	if err != nil {
		return err
	}
	var problems []string
	for name, want := range expected {
		got, ok := actual[name]
		if !ok {
			problems = append(problems, "missing "+name)
			continue
		}
		if !bytes.Equal(got.data, want.data) {
			problems = append(problems, "modified "+name)
		} else if got.exec != want.exec {
			problems = append(problems, "permissions changed on "+name)
		}
	}
	for name := range actual {
		if _, ok := expected[name]; !ok {
			problems = append(problems, "unexpected "+name)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("installed contents of %s drifted from the ingot (%s); run smith sync", dir, strings.Join(problems, ", "))
	}
	return nil
}

type entry struct {
	data []byte
	exec bool
}

// entries maps the regular files of an ingot by slash-relative name.
func entries(ingot []byte) (map[string]entry, error) {
	tr := tar.NewReader(bytes.NewReader(ingot))
	out := map[string]entry{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, unpackLimit))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", hdr.Name, err)
		}
		out[hdr.Name] = entry{data: data, exec: hdr.Mode&0o111 != 0}
	}
}

// entriesOnDisk maps the regular files under dir by slash-relative name,
// skipping the ingot artifact itself (passed as a full path).
func entriesOnDisk(dir, skipPath string) (map[string]entry, error) {
	out := map[string]entry{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || path == skipPath {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = entry{data: data, exec: info.Mode()&0o111 != 0}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", dir, err)
	}
	return out, nil
}
