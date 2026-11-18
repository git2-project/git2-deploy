package git2ctl_test

import (
	"context"
	"errors"
	"github.com/git2-project/git2-deploy/internal/git2ctl"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// installed runs a real Install against the fake Docker and returns the
// installer, the directory and the config it wrote.
func installed(t *testing.T) (*git2ctl.Installer, *fakeDocker, *git2ctl.Config) {
	t.Helper()
	d := okDocker()
	in, _ := installer(t, d, readyProbe)
	c := plainConfig(t)
	if err := in.Install(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	d.calls = nil
	return in, d, c
}

func TestUpgradeKeepsSecretsAndMovesTheSource(t *testing.T) {
	in, d, before := installed(t)
	const tag = "https://github.com/git2-project/git2-codeplane.git#v0.3.0"
	if err := in.Upgrade(context.Background(), git2ctl.UpgradeOptions{CodeplaneSrc: tag}); err != nil {
		t.Fatal(err)
	}
	after, err := git2ctl.LoadConfig(in.Dir)
	if err != nil {
		t.Fatal(err)
	}
	// New secrets would lock the instance out of its own database.
	if after.Secrets != before.Secrets {
		t.Fatal("upgrade changed the secrets")
	}
	if after.Source.Codeplane != tag || after.Source.Git2 != before.Source.Git2 {
		t.Fatalf("source = %+v, want codeplane moved to %s and git2 kept", after.Source, tag)
	}
	env, err := os.ReadFile(filepath.Join(in.Dir, git2ctl.EnvFile))
	if err != nil || !strings.Contains(string(env), "CODEPLANE_SRC="+tag+"\n") ||
		!strings.Contains(string(env), "POSTGRES_PASSWORD="+before.Secrets.PostgresPassword+"\n") {
		t.Fatalf(".env not re-rendered from git2.yaml: %s %v", env, err)
	}
	up := d.composeCall("up")
	if !slices.Contains(up, "--build") || !slices.Contains(up, "--remove-orphans") || !slices.Contains(up, "git2-test") {
		t.Fatalf("compose up = %v, want a rebuild of project git2-test with --remove-orphans", up)
	}
}

func TestUpgradeNeedsAnInstall(t *testing.T) {
	d := okDocker()
	in, dir := installer(t, d, readyProbe)
	err := in.Upgrade(context.Background(), git2ctl.UpgradeOptions{})
	if !errors.Is(err, git2ctl.ErrNotInstalled) || !strings.Contains(err.Error(), "git2ctl install") {
		t.Fatalf("err = %v, want git2ctl.ErrNotInstalled pointing at install", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("upgrade created the directory")
	}
	if len(d.calls) != 0 {
		t.Fatalf("docker ran: %v", d.calls)
	}
}

// A git2.yaml upgrade cannot read must stay as it is: rewriting it would
// drop the unknown setting, or the secrets.
func TestUpgradeRefusesAConfigItCannotRead(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"unknown key":   func(s string) string { return s + "backup_target: s3://x\n" },
		"other version": func(s string) string { return strings.Replace(s, "version: 1", "version: 2", 1) },
		"no secrets":    func(s string) string { return s[:strings.Index(s, "\nsecrets:")+1] },
	} {
		t.Run(name, func(t *testing.T) {
			in, d, _ := installed(t)
			path := filepath.Join(in.Dir, git2ctl.ConfigFile)
			raw, _ := os.ReadFile(path)
			changed := edit(string(raw))
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := in.Upgrade(context.Background(), git2ctl.UpgradeOptions{}); err == nil {
				t.Fatal("upgrade accepted the config")
			}
			if now, _ := os.ReadFile(path); string(now) != changed {
				t.Fatal("git2.yaml was rewritten")
			}
			if d.composeCall("up") != nil {
				t.Fatal("compose up ran")
			}
		})
	}
}

func TestUpgradeWritesNothingWithoutDocker(t *testing.T) {
	in, d, _ := installed(t)
	d.infoErr = true
	path := filepath.Join(in.Dir, git2ctl.ConfigFile)
	before, _ := os.ReadFile(path)
	if err := in.Upgrade(context.Background(), git2ctl.UpgradeOptions{Git2Src: "../elsewhere"}); err == nil {
		t.Fatal("upgrade went on without Docker")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("git2.yaml changed although nothing could run")
	}
}
