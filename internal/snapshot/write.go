package snapshot

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

// ErrRepositoryGone is what a BundleSource returns for a repository deleted
// after it was listed (git-svc answers NOT_FOUND). It is left out of the
// snapshot; any other error fails the snapshot.
var ErrRepositoryGone = errors.New("repository was deleted during the snapshot")

// BundleSource lists the repositories and writes one Git bundle each, all
// refs (git-svc BundleService.WriteBundle, git2-codeplane#17). An empty
// repository writes nothing: its entry is an empty file.
type BundleSource interface {
	RepositoryIDs(ctx context.Context) ([]string, error)
	WriteBundle(ctx context.Context, id string, w io.Writer) error
}

// Input is everything one snapshot holds.
type Input struct {
	ID          string // UUID
	CreatedAt   time.Time
	Git2Version string
	// Recipient is the operator's age X25519 public key ("age1..."). The
	// private key never touches the server that is backed up (NFR-05).
	Recipient string
	// Database writes `pg_dump --format=custom` of the whole database.
	Database     func(ctx context.Context, w io.Writer) error
	Repositories BundleSource
	// Config is the path of git2.yaml.
	Config string
	// Secrets maps a file name under secrets/ to the local file to copy.
	Secrets map[string]string
}

// Write writes one snapshot under root, in root/<DirName>/, and then points
// root/latest at it. Order (snapshot-format.md): database first, then the
// repositories, then git2.yaml and the secrets, manifest.json last.
//
// tar needs each entry's size before its content, and a dump or a bundle
// streams with no size known in advance, so each entry is first spooled to a
// file in a private (0700) directory under root and hashed on the way, then
// copied into the archive and removed. root therefore needs room for the
// archive plus the largest single entry. On any error the snapshot directory
// is removed; root/latest is untouched.
func Write(ctx context.Context, root string, in Input) (_ *Index, err error) {
	recipient, err := in.check()
	if err != nil {
		return nil, err
	}
	dirName := DirName(in.CreatedAt, in.ID)
	dir := filepath.Join(root, dirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	spool, err := os.MkdirTemp(root, ".spool-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(spool) }()

	archive, err := writeArchive(ctx, dir, spool, recipient, in)
	if err != nil {
		return nil, err
	}
	idx := &Index{
		FormatVersion: FormatVersion, ID: in.ID, CreatedAt: in.CreatedAt.UTC(),
		Git2Version: in.Git2Version, Recipient: in.Recipient, Archive: archive,
	}
	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	// snapshot.json only once the archive is complete, latest only after it.
	if err := writeFileAtomic(filepath.Join(dir, IndexName), append(body, '\n')); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(root, LatestName), []byte(dirName+"\n")); err != nil {
		return nil, err
	}
	return idx, nil
}

func (in Input) check() (age.Recipient, error) {
	if _, err := uuid.Parse(in.ID); err != nil {
		return nil, fmt.Errorf("snapshot id %q is not a UUID", in.ID)
	}
	if in.CreatedAt.IsZero() || in.Database == nil || in.Repositories == nil || in.Config == "" {
		return nil, errors.New("snapshot input needs created_at, a database dump, a repository source and git2.yaml")
	}
	for name := range in.Secrets {
		if !safeName(name) {
			return nil, fmt.Errorf("secret file name %q: only a-z, A-Z, 0-9, '.', '_' and '-'", name)
		}
	}
	r, err := age.ParseX25519Recipient(in.Recipient)
	if err != nil {
		return nil, fmt.Errorf("backup recipient: %w", err)
	}
	return r, nil
}

// writeArchive streams tar -> zstd -> age into dir/ArchiveName and returns
// its size and the sha256 of the encrypted bytes.
func writeArchive(ctx context.Context, dir, spool string, r age.Recipient, in Input) (Archive, error) {
	partial := filepath.Join(dir, ArchiveName+".partial")
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Archive{}, err
	}
	defer f.Close()
	sum := sha256.New()
	count := &counter{}
	enc, err := age.Encrypt(io.MultiWriter(f, sum, count), r)
	if err != nil {
		return Archive{}, err
	}
	zw, err := zstd.NewWriter(enc)
	if err != nil {
		return Archive{}, err
	}
	tw := tar.NewWriter(zw)
	m := Manifest{FormatVersion: FormatVersion, ID: in.ID, CreatedAt: in.CreatedAt.UTC(), Git2Version: in.Git2Version}
	if err := addEntries(ctx, tw, spool, in, &m); err != nil {
		return Archive{}, err
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Archive{}, err
	}
	if err := addBytes(tw, ManifestPath, append(body, '\n')); err != nil {
		return Archive{}, err
	}
	// Each layer flushes into the next; age encrypts its last chunk only on
	// Close, so every Close error counts (filippo.io/age, ExampleEncrypt).
	for _, c := range []io.Closer{tw, zw, enc} {
		if err := c.Close(); err != nil {
			return Archive{}, fmt.Errorf("finish archive: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return Archive{}, err
	}
	if err := f.Close(); err != nil {
		return Archive{}, err
	}
	if err := os.Rename(partial, filepath.Join(dir, ArchiveName)); err != nil {
		return Archive{}, err
	}
	return Archive{Key: ArchiveName, Size: count.n, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

func addEntries(ctx context.Context, tw *tar.Writer, spool string, in Input, m *Manifest) error {
	add := func(path string, fill func(io.Writer) error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := addSpooled(tw, spool, path, fill)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		m.Files = append(m.Files, f)
		return nil
	}
	if err := add(DatabasePath, func(w io.Writer) error { return in.Database(ctx, w) }); err != nil {
		return err
	}
	ids, err := in.Repositories.RepositoryIDs(ctx)
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("repository id %q is not a UUID", id)
		}
		err := add(reposDir+id+".bundle", func(w io.Writer) error { return in.Repositories.WriteBundle(ctx, id, w) })
		if errors.Is(err, ErrRepositoryGone) {
			continue
		}
		if err != nil {
			return err
		}
	}
	if err := add(ConfigPath, copyFrom(in.Config)); err != nil {
		return err
	}
	names := make([]string, 0, len(in.Secrets))
	for name := range in.Secrets {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := add(secretsDir+name, copyFrom(in.Secrets[name])); err != nil {
			return err
		}
	}
	return nil
}

// addSpooled runs fill into a spool file, then adds that file to the tar.
// A fill that fails leaves nothing in the archive.
func addSpooled(tw *tar.Writer, spool, path string, fill func(io.Writer) error) (File, error) {
	tmp, err := os.CreateTemp(spool, "entry-")
	if err != nil {
		return File{}, err
	}
	defer func() { tmp.Close(); _ = os.Remove(tmp.Name()) }()
	sum := sha256.New()
	if err := fill(io.MultiWriter(tmp, sum)); err != nil {
		return File{}, err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return File{}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return File{}, err
	}
	if err := tw.WriteHeader(header(path, size)); err != nil {
		return File{}, err
	}
	if _, err := io.Copy(tw, tmp); err != nil {
		return File{}, err
	}
	return File{Path: path, Size: size, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

func addBytes(tw *tar.Writer, path string, body []byte) error {
	if err := tw.WriteHeader(header(path, int64(len(body)))); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

// header is a plain file entry. Owner, mode and time are fixed: they carry
// nothing a restore needs, and fixed values keep the archive free of the
// backed-up server's user names.
func header(path string, size int64) *tar.Header {
	return &tar.Header{Typeflag: tar.TypeReg, Name: path, Size: size, Mode: 0o600, Format: tar.FormatPAX}
}

func copyFrom(path string) func(io.Writer) error {
	return func(w io.Writer) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
}

// writeFileAtomic writes to a temporary name in the same directory, syncs and
// renames, so a reader sees the old file or the whole new one.
func writeFileAtomic(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after the rename
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func safeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

type counter struct{ n int64 }

func (c *counter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
