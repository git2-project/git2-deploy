// Package snapshot writes and verifies git2 snapshots in the format of
// docs/api/snapshot-format.md (WBS-12.1, FR-70, NFR-05): one archive, tar
// compressed with zstd and encrypted with age, plus a plain snapshot.json
// index next to it.
package snapshot

import "time"

// FormatVersion is the snapshot format this package writes and reads, in
// both manifest.json and snapshot.json.
const FormatVersion = 1

// Names inside a snapshot directory and inside the archive.
const (
	ArchiveName  = "snapshot.tar.zst.age"
	IndexName    = "snapshot.json"
	LatestName   = "latest"
	ManifestPath = "manifest.json"
	DatabasePath = "database/git2.dump"
	ConfigPath   = "config/git2.yaml"
	reposDir     = "repositories/"
	secretsDir   = "secrets/"
)

// File is one entry of the archive, as manifest.json lists it.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
}

// Manifest is manifest.json, the last entry of the archive. It lists every
// other entry with its size and checksum, so a restore can tell a complete,
// undamaged archive from anything else before it writes to the new server.
type Manifest struct {
	FormatVersion int       `json:"format_version"`
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Git2Version   string    `json:"git2_version"`
	Files         []File    `json:"files"`
}

// Archive describes the encrypted archive in snapshot.json.
type Archive struct {
	Key    string `json:"key"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
}

// Index is snapshot.json: plain text and free of secrets. It is written only
// after the archive is complete, so a snapshot without it is never restored.
type Index struct {
	FormatVersion int       `json:"format_version"`
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Git2Version   string    `json:"git2_version"`
	Recipient     string    `json:"recipient"`
	Archive       Archive   `json:"archive"`
}

// DirName is the snapshot's directory: UTC time first, so a plain listing is
// in time order.
func DirName(createdAt time.Time, id string) string {
	return createdAt.UTC().Format("20060102T150405Z") + "_" + id
}
