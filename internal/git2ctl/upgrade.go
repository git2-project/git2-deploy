package git2ctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// ErrNotInstalled means the directory has no git2.yaml (FR-78).
var ErrNotInstalled = errors.New("git2 is not installed here")

// UpgradeOptions move the pinned sources; empty keeps what git2.yaml has.
type UpgradeOptions struct {
	Git2Src      string
	CodeplaneSrc string
}

// LoadConfig reads git2.yaml from dir. Unknown keys and another format
// version are refused, so a typo is not silently dropped on the rewrite.
func LoadConfig(dir string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w (%s): run `git2ctl install` first", ErrNotInstalled, dir)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("read %s: %w", ConfigFile, err)
	}
	if c.Version != ConfigVersion {
		return nil, fmt.Errorf("%s has version %d; this git2ctl reads version %d", ConfigFile, c.Version, ConfigVersion)
	}
	// The secrets must be the ones the volumes were created with: never
	// generate new ones here (PostgreSQL would refuse them).
	if c.Secrets.PostgresPassword == "" || c.Secrets.JWTSecret == "" {
		return nil, fmt.Errorf("%s has no secrets: restore it from the backup, it cannot be regenerated", ConfigFile)
	}
	return &c, nil
}

// Upgrade runs FR-78 on an installed instance: move the sources if asked,
// rewrite the Compose files, .env and git2.yaml from the config (same
// secrets), rebuild, let migrate run again and restart, then wait until git2
// answers as install does. Nothing is written if git2.yaml cannot be read
// or Docker is not usable.
func (in *Installer) Upgrade(ctx context.Context, o UpgradeOptions) error {
	c, err := LoadConfig(in.Dir)
	if err != nil {
		return err
	}
	if err := checkDocker(ctx, in.Run); err != nil {
		return err
	}
	c.Source = Source{Git2: or(o.Git2Src, c.Source.Git2), Codeplane: or(o.CodeplaneSrc, c.Source.Codeplane)}
	if err := writeInstall(in.Dir, c); err != nil {
		return err
	}
	fmt.Fprintf(in.Out, "upgrading git2 in %s (git2 %s, codeplane %s)\n", in.Dir, c.Source.Git2, c.Source.Codeplane)
	// --remove-orphans: a service this git2ctl's stack no longer has is stopped.
	up := append(composeArgs(in.Dir, in.Project, c), "up", "-d", "--build", "--quiet-pull", "--remove-orphans")
	if out, err := in.Run.Run(ctx, "docker", up...); err != nil {
		return fmt.Errorf("docker compose up failed (see what runs: docker compose -p %s ps -a): %s", in.Project, tail(out, 20))
	}
	return in.ready(ctx, c, "upgraded")
}
