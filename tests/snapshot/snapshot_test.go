// Tests for internal/snapshot (WBS-12.1). They live under tests/ by the
// course rule; each one checks a promise of docs/api/snapshot-format.md.
package snapshot_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"

	"github.com/git2-project/git2-deploy/internal/snapshot"
)

const (
	snapID   = "0193a5f0-7c2e-7b3a-9d41-2f6e8c1b5a90"
	repoA    = "11111111-1111-4111-8111-111111111111"
	repoB    = "22222222-2222-4222-8222-222222222222"
	repoGone = "33333333-3333-4333-8333-333333333333"
	// secretValue must never appear outside the encrypted archive.
	secretValue = "jwt-key-4f9c2a7e1b3d5f60"
	configBody  = "version: 1\nexternal_url: https://git.example.test\n"
)

var createdAt = time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)

// fakeRepos answers like git-svc: a bundle per repository, nothing for an
// empty one, ErrRepositoryGone for one deleted after the listing.
type fakeRepos struct {
	bundles map[string]string
	gone    map[string]bool
	fail    map[string]error
}

func (f fakeRepos) RepositoryIDs(context.Context) ([]string, error) {
	var ids []string
	for id := range f.bundles {
		ids = append(ids, id)
	}
	for id := range f.gone {
		ids = append(ids, id)
	}
	for id := range f.fail {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f fakeRepos) WriteBundle(_ context.Context, id string, w io.Writer) error {
	if f.gone[id] {
		return snapshot.ErrRepositoryGone
	}
	if err := f.fail[id]; err != nil {
		return err
	}
	_, err := io.WriteString(w, f.bundles[id])
	return err
}

type fixture struct {
	root string
	id   *age.X25519Identity
	in   snapshot.Input
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	cfg := filepath.Join(src, "git2.yaml")
	jwt := filepath.Join(src, "jwt")
	if err := os.WriteFile(cfg, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jwt, []byte(secretValue), 0o600); err != nil {
		t.Fatal(err)
	}
	return &fixture{root: t.TempDir(), id: id, in: snapshot.Input{
		ID: snapID, CreatedAt: createdAt, Git2Version: "1.0.0-test", Recipient: id.Recipient().String(),
		Database: func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "PGDMP fake dump")
			return err
		},
		Repositories: fakeRepos{bundles: map[string]string{repoA: "# v2 git bundle\nA", repoB: ""}},
		Config:       cfg,
		Secrets:      map[string]string{"jwt_secret": jwt},
	}}
}

func (f *fixture) dir() string { return filepath.Join(f.root, snapshot.DirName(createdAt, snapID)) }

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestRoundTrip(t *testing.T) {
	f := newFixture(t)
	idx, err := snapshot.Write(context.Background(), f.root, f.in)
	if err != nil {
		t.Fatal(err)
	}
	m, err := snapshot.Verify(f.dir(), f.id)
	if err != nil {
		t.Fatalf("verify a fresh snapshot: %v", err)
	}
	want := []snapshot.File{
		{Path: "database/git2.dump", Size: 15, SHA256: hash("PGDMP fake dump")},
		{Path: "repositories/" + repoA + ".bundle", Size: 17, SHA256: hash("# v2 git bundle\nA")},
		{Path: "repositories/" + repoB + ".bundle", Size: 0, SHA256: hash("")},
		{Path: "config/git2.yaml", Size: int64(len(configBody)), SHA256: hash(configBody)},
		{Path: "secrets/jwt_secret", Size: int64(len(secretValue)), SHA256: hash(secretValue)},
	}
	if len(m.Files) != len(want) {
		t.Fatalf("manifest files = %+v, want %+v", m.Files, want)
	}
	// Database first, then repositories (snapshot-format.md, "Order").
	for i := range want {
		if m.Files[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, m.Files[i], want[i])
		}
	}
	if m.ID != snapID || idx.ID != snapID || idx.Recipient != f.in.Recipient || idx.Archive.Key != snapshot.ArchiveName {
		t.Errorf("index %+v / manifest id %s", idx, m.ID)
	}
	latest, err := os.ReadFile(filepath.Join(f.root, snapshot.LatestName))
	if err != nil || strings.TrimSpace(string(latest)) != filepath.Base(f.dir()) {
		t.Errorf("latest = %q, %v", latest, err)
	}
	entries, _ := os.ReadDir(f.root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".spool-") {
			t.Errorf("spool directory %s left behind", e.Name())
		}
	}
	snapFiles, _ := os.ReadDir(f.dir())
	if len(snapFiles) != 2 {
		t.Errorf("snapshot directory holds %d files, want the archive and snapshot.json", len(snapFiles))
	}
}

// NFR-05: without the private key the snapshot cannot be read, and no secret
// or plain content is visible in the archive or the index.
func TestKeylessOpenFails(t *testing.T) {
	f := newFixture(t)
	if _, err := snapshot.Write(context.Background(), f.root, f.in); err != nil {
		t.Fatal(err)
	}
	other, _ := age.GenerateX25519Identity()
	var noMatch *age.NoIdentityMatchError
	if _, err := snapshot.Verify(f.dir(), other); !errors.As(err, &noMatch) {
		t.Fatalf("verify with another key: %v, want age's no-identity-match error", err)
	}
	for _, name := range []string{snapshot.ArchiveName, snapshot.IndexName} {
		raw, err := os.ReadFile(filepath.Join(f.dir(), name))
		if err != nil {
			t.Fatal(err)
		}
		for _, plain := range []string{secretValue, "PGDMP", "external_url", "jwt_secret"} {
			if bytes.Contains(raw, []byte(plain)) {
				t.Errorf("%s contains %q in plain text", name, plain)
			}
		}
	}
}

func TestDamageIsCaughtBeforeDecrypting(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"flipped byte": func(t *testing.T, dir string) {
			p := filepath.Join(dir, snapshot.ArchiveName)
			raw, _ := os.ReadFile(p)
			raw[len(raw)/2] ^= 0x01
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"cut short": func(t *testing.T, dir string) {
			if err := os.Truncate(filepath.Join(dir, snapshot.ArchiveName), 100); err != nil {
				t.Fatal(err)
			}
		},
		"no index": func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, snapshot.IndexName)); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := snapshot.Write(context.Background(), f.root, f.in); err != nil {
				t.Fatal(err)
			}
			damage(t, f.dir())
			// A wrong key too: the damage must be found by the checksum, not
			// by a failed decryption.
			other, _ := age.GenerateX25519Identity()
			if _, err := snapshot.Verify(f.dir(), other); !errors.Is(err, snapshot.ErrDamaged) {
				t.Fatalf("err = %v, want ErrDamaged", err)
			}
		})
	}
}

// An archive with a valid checksum and key but a crafted entry is refused:
// a restore must never write outside its target or trust an extra file.
func TestCraftedArchivesAreRefused(t *testing.T) {
	type entry struct{ name, body string }
	manifest := func(files ...snapshot.File) entry {
		b, _ := json.Marshal(snapshot.Manifest{FormatVersion: 1, ID: snapID, CreatedAt: createdAt, Files: files})
		return entry{snapshot.ManifestPath, string(b)}
	}
	dump := snapshot.File{Path: "database/git2.dump", Size: 1, SHA256: hash("x")}
	cases := map[string][]entry{
		"path escapes":         {{"../evil", "x"}, manifest(snapshot.File{Path: "../evil", Size: 1, SHA256: hash("x")})},
		"absolute path":        {{"/etc/passwd", "x"}, manifest(snapshot.File{Path: "/etc/passwd", Size: 1, SHA256: hash("x")})},
		"file not listed":      {{"database/git2.dump", "x"}, {"secrets/extra", "y"}, manifest(dump)},
		"listed, missing":      {manifest(dump)},
		"content changed":      {{"database/git2.dump", "z"}, manifest(dump)},
		"entry after manifest": {manifest(dump), {"database/git2.dump", "x"}},
		"no manifest":          {{"database/git2.dump", "x"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			dir := f.dir()
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			enc, _ := age.Encrypt(&buf, f.id.Recipient())
			zw, _ := zstd.NewWriter(enc)
			tw := tar.NewWriter(zw)
			for _, e := range entries {
				_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: e.name, Size: int64(len(e.body)), Mode: 0o600})
				_, _ = io.WriteString(tw, e.body)
			}
			_ = tw.Close()
			_ = zw.Close()
			_ = enc.Close()
			sum := sha256.Sum256(buf.Bytes())
			idx, _ := json.Marshal(snapshot.Index{FormatVersion: 1, ID: snapID, CreatedAt: createdAt,
				Archive: snapshot.Archive{Key: snapshot.ArchiveName, Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}})
			_ = os.WriteFile(filepath.Join(dir, snapshot.ArchiveName), buf.Bytes(), 0o600)
			_ = os.WriteFile(filepath.Join(dir, snapshot.IndexName), idx, 0o600)
			if _, err := snapshot.Verify(dir, f.id); !errors.Is(err, snapshot.ErrDamaged) {
				t.Fatalf("err = %v, want ErrDamaged", err)
			}
		})
	}
}

func TestRepositories(t *testing.T) {
	t.Run("deleted during the snapshot is skipped", func(t *testing.T) {
		f := newFixture(t)
		f.in.Repositories = fakeRepos{bundles: map[string]string{repoA: "A"}, gone: map[string]bool{repoGone: true}}
		if _, err := snapshot.Write(context.Background(), f.root, f.in); err != nil {
			t.Fatal(err)
		}
		m, err := snapshot.Verify(f.dir(), f.id)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range m.Files {
			if strings.Contains(file.Path, repoGone) {
				t.Fatalf("deleted repository %s is in the manifest", repoGone)
			}
		}
	})
	t.Run("any other error fails the snapshot", func(t *testing.T) {
		f := newFixture(t)
		f.in.Repositories = fakeRepos{bundles: map[string]string{repoA: "A"}, fail: map[string]error{repoB: errors.New("git-svc unavailable")}}
		assertFailedCleanly(t, f)
	})
	t.Run("an id that is not a UUID", func(t *testing.T) {
		f := newFixture(t)
		f.in.Repositories = fakeRepos{bundles: map[string]string{"../../etc": "x"}}
		assertFailedCleanly(t, f)
	})
}

func TestFailuresLeaveNoSnapshot(t *testing.T) {
	f := newFixture(t)
	f.in.Database = func(context.Context, io.Writer) error { return errors.New("pg_dump: connection refused") }
	assertFailedCleanly(t, f)

	f = newFixture(t)
	f.in.Secrets = map[string]string{"../jwt": f.in.Config}
	assertFailedCleanly(t, f)

	f = newFixture(t)
	f.in.Recipient = "age1notakey"
	assertFailedCleanly(t, f)

	f = newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshot.Write(ctx, f.root, f.in); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
}

// assertFailedCleanly: Write fails, leaves no snapshot directory or spool,
// and an earlier latest still points at the earlier snapshot.
func assertFailedCleanly(t *testing.T, f *fixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, snapshot.LatestName), []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Write(context.Background(), f.root, f.in); err == nil {
		t.Fatal("write succeeded")
	}
	entries, _ := os.ReadDir(f.root)
	for _, e := range entries {
		if e.Name() != snapshot.LatestName {
			t.Errorf("%s left behind after a failed snapshot", e.Name())
		}
	}
	if latest, _ := os.ReadFile(filepath.Join(f.root, snapshot.LatestName)); string(latest) != "earlier\n" {
		t.Errorf("latest changed to %q", latest)
	}
}

// TestPgDump runs the real pg_dump against TEST_POSTGRES_DSN (no password
// in it) with TEST_POSTGRES_PASSWORD, and checks the archive holds a custom
// format dump that pg_restore can list.
func TestPgDump(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	bin, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump not installed")
	}
	f := newFixture(t)
	f.in.Database = snapshot.PgDump(bin, dsn, os.Getenv("TEST_POSTGRES_PASSWORD"))
	if _, err := snapshot.Write(context.Background(), f.root, f.in); err != nil {
		t.Fatal(err)
	}
	m, err := snapshot.Verify(f.dir(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[0].Path != snapshot.DatabasePath || m.Files[0].Size < 100 {
		t.Fatalf("dump entry %+v", m.Files[0])
	}

	f2 := newFixture(t)
	f2.in.Database = snapshot.PgDump(bin, dsn, "wrong-password")
	if _, err := snapshot.Write(context.Background(), f2.root, f2.in); err == nil || strings.Contains(err.Error(), "wrong-password") {
		t.Fatalf("wrong password: err = %v, want a failure that does not echo the password", err)
	}
}
