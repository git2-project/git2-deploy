package git2ctl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	deploy "github.com/git2-project/git2-deploy"
)

// The install directory mirrors the repo: compose/ and images/ from the
// embedded stack, git2.yaml at the top, and the .env Compose reads next to
// compose.yaml.
const (
	ConfigFile = "git2.yaml"
	composeDir = "compose"
	envFile    = "compose/.env"
)

// DefaultReadyTimeout leaves room for the first build inside NFR-15's 10
// minutes; with published images (WBS-13.4) start-up takes seconds.
const DefaultReadyTimeout = 8 * time.Minute

// Prober asks the running instance for a path's HTTP status.
type Prober func(ctx context.Context, path string) (int, error)

// Installer installs git2 into Dir as Compose project Project.
type Installer struct {
	Dir          string
	Project      string
	Run          Runner
	Out          io.Writer
	ReadyTimeout time.Duration
	PollEvery    time.Duration
	// Probe, when nil, is built from the config (see probe).
	Probe Prober
}

// Install runs UC-01: preflight, write the files, start the stack, wait
// until git2 answers. Nothing is written or started if preflight fails. If
// the stack does not become ready, the containers are left running so the
// operator can read their logs (E3).
func (in *Installer) Install(ctx context.Context, c *Config) error {
	if err := Preflight(ctx, in.Run, in.Dir, c); err != nil {
		return err
	}
	if err := writeInstall(in.Dir, c); err != nil {
		return err
	}
	fmt.Fprintf(in.Out, "wrote %s; starting git2 (the first start builds the images)\n", filepath.Join(in.Dir, ConfigFile))
	up := append(composeArgs(in.Dir, in.Project, c), "up", "-d", "--build", "--quiet-pull")
	if out, err := in.Run.Run(ctx, "docker", up...); err != nil {
		return fmt.Errorf("docker compose up failed, nothing is running: %s", tail(out, 20))
	}
	probe := in.Probe
	if probe == nil {
		var err error
		if probe, err = newProbe(c); err != nil {
			return err
		}
	}
	if err := in.waitReady(ctx, c, probe); err != nil {
		ps, _ := in.Run.Run(ctx, "docker", append(composeArgs(in.Dir, in.Project, c), "ps", "-a")...)
		return fmt.Errorf("%w\n%s\nlogs: docker compose -p %s logs <service>; the containers are left running", err, ps, in.Project)
	}
	fmt.Fprintf(in.Out, "git2 is running at %s\nOpen it to create the first administrator.\n", c.ExternalURL)
	return nil
}

// waitReady waits until git2 core answers through the gateway (an
// unauthenticated /api/auth/me is 401 from core, 503 while it is down) and,
// with git-svc, until /readyz is 200.
func (in *Installer) waitReady(ctx context.Context, c *Config, probe Prober) error {
	checks := []struct {
		path string
		want int
	}{{"/api/auth/me", http.StatusUnauthorized}}
	if c.WithGitSvc {
		checks = append(checks, struct {
			path string
			want int
		}{"/readyz", http.StatusOK})
	}
	ctx, cancel := context.WithTimeout(ctx, in.ReadyTimeout)
	defer cancel()
	for _, ch := range checks {
		for {
			if code, err := probe(ctx, ch.path); err == nil && code == ch.want {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("git2 did not become ready within %s (%s did not answer %d)", in.ReadyTimeout, ch.path, ch.want)
			case <-time.After(in.PollEvery):
			}
		}
	}
	return nil
}

// composeArgs are the arguments every docker compose call in Dir shares.
func composeArgs(dir, project string, c *Config) []string {
	cd := filepath.Join(dir, composeDir)
	args := []string{"compose", "--project-directory", cd, "-p", project,
		"--env-file", filepath.Join(dir, envFile), "-f", filepath.Join(cd, "compose.yaml")}
	if c.TLS() {
		args = append(args, "-f", filepath.Join(cd, "compose.tls.yaml"))
	}
	if c.WithGitSvc {
		args = append(args, "--profile", "git-svc")
	}
	return args
}

// writeInstall writes the Compose files, git2.yaml and .env. The two that
// hold secrets are 0600 in a 0700 directory. Each file is written to a
// temporary name and renamed, so a crash never leaves half a git2.yaml.
func writeInstall(dir string, c *Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg, err := c.YAML()
	if err != nil {
		return err
	}
	env, err := c.Env()
	if err != nil {
		return err
	}
	if err := writeStack(dir); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, envFile), env, 0o600); err != nil {
		return err
	}
	// git2.yaml last: its presence is what marks the directory as installed.
	return writeFile(filepath.Join(dir, ConfigFile), cfg, 0o600)
}

// writeStack copies the embedded compose/ and images/ into dir.
func writeStack(dir string) error {
	return fs.WalkDir(deploy.Stack, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := deploy.Stack.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFile(target, body, 0o644)
	})
}

func writeFile(path string, body []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after the rename
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// newProbe talks to the gateway on this host. With TLS it trusts the
// instance's own fullchain.pem and checks the external host name, so the
// probe verifies the certificate the users will get instead of skipping
// verification.
func newProbe(c *Config) (Prober, error) {
	base := url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(c.HTTPPort)}
	client := &http.Client{Timeout: 5 * time.Second}
	if c.TLS() {
		pem, err := os.ReadFile(filepath.Join(c.TLSDir, "fullchain.pem"))
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificate in %s", filepath.Join(c.TLSDir, "fullchain.pem"))
		}
		ext, err := url.Parse(c.ExternalURL)
		if err != nil {
			return nil, err
		}
		base = url.URL{Scheme: "https", Host: "127.0.0.1:" + strconv.Itoa(c.HTTPSPort)}
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: roots, ServerName: ext.Hostname(), MinVersion: tls.VersionTLS12,
		}}
	}
	return func(ctx context.Context, path string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+path, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}, nil
}

// ExecRunner runs real commands, in Dir once it exists. Compose's builder
// (Bake) reads build contexts without asking only under the working
// directory, and the images/ contexts live in the install directory.
type ExecRunner struct {
	Dir string
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if st, err := os.Stat(r.Dir); err == nil && st.IsDir() {
		cmd.Dir = r.Dir
	}
	return cmd.CombinedOutput()
}

func tail(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
