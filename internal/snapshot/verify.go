package snapshot

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

// ErrDamaged means the snapshot is incomplete, altered or not what its index
// says. A restore must stop before it writes anything.
var ErrDamaged = errors.New("snapshot is damaged")

// maxManifest bounds manifest.json, read into memory.
const maxManifest = 16 << 20

// zstdMaxMemory bounds the decoder's window, so a crafted archive cannot make
// it allocate without limit (klauspost/compress zstd WithDecoderMaxMemory).
const zstdMaxMemory = 512 << 20

// Verify checks a snapshot directory the way a restore must before it writes
// anything (FR-76): snapshot.json is present, the archive has the size and
// sha256 it names (checked before decrypting, so a damaged upload fails with
// a clear message), it decrypts with one of the identities, and every entry
// matches manifest.json, with nothing missing and nothing extra. It returns
// the manifest.
func Verify(dir string, identities ...age.Identity) (*Manifest, error) {
	idx, err := readIndex(dir)
	if err != nil {
		return nil, err
	}
	archive := filepath.Join(dir, idx.Archive.Key)
	if idx.Archive.Key != ArchiveName {
		return nil, fmt.Errorf("%w: archive key %q", ErrDamaged, idx.Archive.Key)
	}
	if err := checkSum(archive, idx.Archive); err != nil {
		return nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec, err := age.Decrypt(f, identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypt the archive: %w", err)
	}
	zr, err := zstd.NewReader(dec, zstd.WithDecoderMaxMemory(zstdMaxMemory))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	seen, manifest, err := readEntries(tar.NewReader(zr))
	if err != nil {
		return nil, err
	}
	if manifest.FormatVersion != FormatVersion || manifest.ID != idx.ID {
		return nil, fmt.Errorf("%w: manifest is for snapshot %q format %d, index for %q", ErrDamaged, manifest.ID, manifest.FormatVersion, idx.ID)
	}
	if err := sameFiles(manifest.Files, seen); err != nil {
		return nil, err
	}
	return manifest, nil
}

func readIndex(dir string) (*Index, error) {
	raw, err := os.ReadFile(filepath.Join(dir, IndexName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: no %s, the snapshot did not finish", ErrDamaged, IndexName)
	}
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, IndexName, err)
	}
	if idx.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("snapshot format %d; this git2 reads format %d", idx.FormatVersion, FormatVersion)
	}
	return &idx, nil
}

func checkSum(name string, want Archive) error {
	f, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return fmt.Errorf("%w: the archive does not match %s (size %d, want %d, or its sha256 differs)", ErrDamaged, IndexName, n, want.Size)
	}
	return nil
}

// readEntries hashes every entry. manifest.json must be the last one.
func readEntries(tr *tar.Reader) (map[string]File, *Manifest, error) {
	seen := map[string]File{}
	var manifest *Manifest
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrDamaged, err)
		}
		if manifest != nil {
			return nil, nil, fmt.Errorf("%w: %s after %s", ErrDamaged, h.Name, ManifestPath)
		}
		if h.Typeflag != tar.TypeReg || !safePath(h.Name) {
			return nil, nil, fmt.Errorf("%w: entry %q is not a plain file with a safe name", ErrDamaged, h.Name)
		}
		if _, dup := seen[h.Name]; dup {
			return nil, nil, fmt.Errorf("%w: %s appears twice", ErrDamaged, h.Name)
		}
		if h.Name == ManifestPath {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, io.LimitReader(tr, maxManifest+1)); err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrDamaged, err)
			}
			if buf.Len() > maxManifest {
				return nil, nil, fmt.Errorf("%w: %s is too large", ErrDamaged, ManifestPath)
			}
			manifest = &Manifest{}
			if err := json.Unmarshal(buf.Bytes(), manifest); err != nil {
				return nil, nil, fmt.Errorf("%w: %s: %v", ErrDamaged, ManifestPath, err)
			}
			continue
		}
		sum := sha256.New()
		n, err := io.Copy(sum, tr)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s: %v", ErrDamaged, h.Name, err)
		}
		seen[h.Name] = File{Path: h.Name, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	}
	if manifest == nil {
		return nil, nil, fmt.Errorf("%w: no %s, the archive is cut short", ErrDamaged, ManifestPath)
	}
	return seen, manifest, nil
}

func sameFiles(listed []File, seen map[string]File) error {
	if len(listed) != len(seen) {
		return fmt.Errorf("%w: manifest lists %d files, the archive has %d", ErrDamaged, len(listed), len(seen))
	}
	for _, want := range listed {
		got, ok := seen[want.Path]
		if !ok {
			return fmt.Errorf("%w: %s is in the manifest but not in the archive", ErrDamaged, want.Path)
		}
		if got != want {
			return fmt.Errorf("%w: %s does not match the manifest", ErrDamaged, want.Path)
		}
	}
	return nil
}

// safePath accepts only the relative, clean paths this package writes:
// no "..", no leading "/", nothing a restore could write outside its target.
func safePath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.HasPrefix(p, "../") && p != ".." && !strings.Contains(p, "\\")
}
