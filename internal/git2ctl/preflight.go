package git2ctl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// minCompose is the oldest Compose that reads compose.tls.yaml: its
// `ports: !override` tag came in 2.24.4 (docs.docker.com, "Merge Compose
// files", reset and override values).
var minCompose = [3]int{2, 24, 4}

// supportedArch are Docker's names for the architectures git2's images are
// built for (NFR-24).
var supportedArch = map[string]bool{"x86_64": true, "amd64": true, "aarch64": true, "arm64": true}

// Runner runs a command and returns its combined output. ExecRunner is the
// real one; tests pass a fake.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ErrInstalled means the directory already holds an instance (UC-01, A3).
var ErrInstalled = errors.New("git2 is already installed here")

// Preflight checks the host before anything is written or started (UC-01,
// step 2). The first failed check is returned, naming what to fix.
func Preflight(ctx context.Context, run Runner, dir, project string, c *Config) error {
	if _, err := os.Stat(filepath.Join(dir, ConfigFile)); err == nil {
		return fmt.Errorf("%w (%s): use `git2ctl upgrade`, or start over with %s and then remove the directory",
			ErrInstalled, dir, startOver(project))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", dir, err)
	}
	if err := checkDocker(ctx, run); err != nil {
		return err
	}
	if err := projectUnused(ctx, run, project); err != nil {
		return err
	}
	return tlsAndPorts(c)
}

// checkDocker checks that the Docker daemon answers on a supported
// architecture and that Compose is new enough. Install and upgrade both need it.
func checkDocker(ctx context.Context, run Runner) error {
	arch, err := run.Run(ctx, "docker", "info", "--format", "{{.Architecture}}")
	if err != nil {
		return fmt.Errorf("the Docker daemon is not running or not reachable (docker info: %s)", firstLine(arch))
	}
	if a := strings.TrimSpace(string(arch)); !supportedArch[a] {
		return fmt.Errorf("the CPU architecture %q is not supported: git2 runs on amd64 and arm64", a)
	}
	out, err := run.Run(ctx, "docker", "compose", "version", "--short")
	if err != nil {
		return fmt.Errorf("the Docker Compose v2 plugin is missing (docker compose version: %s)", firstLine(out))
	}
	if v := strings.TrimSpace(string(out)); !atLeast(v, minCompose) {
		return fmt.Errorf("docker compose %s is too old: git2 needs %d.%d.%d or newer", v, minCompose[0], minCompose[1], minCompose[2])
	}
	return nil
}

// tlsAndPorts is the rest of install's preflight.
func tlsAndPorts(c *Config) error {
	if c.TLS() {
		for _, f := range []string{"fullchain.pem", "privkey.pem"} {
			if _, err := os.Stat(filepath.Join(c.TLSDir, f)); err != nil {
				return fmt.Errorf("the TLS file %s is not in %s", f, c.TLSDir)
			}
		}
	}
	for _, p := range c.Ports() {
		if err := portFree(c.bind(), p); err != nil {
			return err
		}
	}
	return nil
}

// projectUnused refuses a Compose project that already has containers or
// volumes. Its data volumes live in Docker, not in the install directory, so
// they outlive a removed directory, a failed start or a timed-out install.
// PostgreSQL applies POSTGRES_PASSWORD only to an empty volume: a new install
// with fresh secrets on an old volume could never sign in to its database.
// Compose labels everything it creates with the project name.
func projectUnused(ctx context.Context, run Runner, project string) error {
	label := "label=com.docker.compose.project=" + project
	for _, q := range []struct {
		what string
		args []string
	}{
		{"containers", []string{"ps", "-aq", "--filter", label}},
		{"volumes", []string{"volume", "ls", "-q", "--filter", label}},
	} {
		out, err := run.Run(ctx, "docker", q.args...)
		if err != nil {
			return fmt.Errorf("list the %s of Compose project %q: %s", q.what, project, firstLine(out))
		}
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("the Compose project %q already has %s, from an earlier install: "+
				"start over with %s, or install as another project with --project",
				project, q.what, startOver(project))
		}
	}
	return nil
}

// startOver is the command that removes a project's containers and volumes.
func startOver(project string) string {
	return fmt.Sprintf("`docker compose -p %s down -v` (this deletes its database and repositories)", project)
}

// listenTCP is net.Listen; tests replace it to simulate a refused bind.
var listenTCP = net.Listen

// portFree tries to listen where the gateway will. It is a check, not a
// reservation: something could still take the port before Compose does.
// A permission error is not "in use": an operator in the docker group may
// not bind 80 or 443 itself, but the Docker daemon publishes them for it.
func portFree(host string, port int) error {
	l, err := listenTCP("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	switch {
	case errors.Is(err, syscall.EACCES):
		return nil
	case err != nil:
		return fmt.Errorf("port %d in use on %s: stop what holds it or choose another port", port, host)
	}
	return l.Close()
}

// atLeast compares a version like "2.24.4", "v2.29.1-desktop.1" or "5.5.0"
// with min, part by part. A version it cannot read is refused.
func atLeast(v string, min [3]int) bool {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return false
	}
	for i := 0; i < 3; i++ {
		n := 0
		if i < len(parts) {
			var err error
			if n, err = strconv.Atoi(parts[i]); err != nil {
				return false
			}
		}
		if n != min[i] {
			return n > min[i]
		}
	}
	return true
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no output"
	}
	return s
}
