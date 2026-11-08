package git2ctl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// fakeDocker answers docker commands from a table and records every call.
type fakeDocker struct {
	arch, compose string
	infoErr       bool
	upErr         bool
	calls         [][]string
}

func (f *fakeDocker) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch {
	case slices.Equal(args, []string{"info", "--format", "{{.Architecture}}"}):
		if f.infoErr {
			return []byte("Cannot connect to the Docker daemon"), errors.New("exit 1")
		}
		return []byte(f.arch + "\n"), nil
	case slices.Equal(args, []string{"compose", "version", "--short"}):
		return []byte(f.compose + "\n"), nil
	case slices.Contains(args, "up"):
		if f.upErr {
			return []byte("pull access denied"), errors.New("exit 1")
		}
		return nil, nil
	}
	return nil, nil
}

func (f *fakeDocker) composeCall(verb string) []string {
	for _, c := range f.calls {
		if len(c) > 1 && c[1] == "compose" && slices.Contains(c, verb) {
			return c
		}
	}
	return nil
}

func okDocker() *fakeDocker { return &fakeDocker{arch: "aarch64", compose: "2.29.1"} }

// freePort returns a port nothing listens on right now.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func plainConfig(t *testing.T) *Config {
	t.Helper()
	c, err := NewConfig(InstallOptions{ExternalURL: "http://127.0.0.1:" + strconv.Itoa(freePort(t))})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tlsDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{"fullchain.pem", "privkey.pem"} {
		if err := os.WriteFile(filepath.Join(d, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestNewConfig(t *testing.T) {
	tls := tlsDir(t)
	cases := []struct {
		name    string
		o       InstallOptions
		wantErr string
		check   func(*testing.T, *Config)
	}{
		{name: "https with TLS", o: InstallOptions{ExternalURL: "https://git.example.test", TLSDir: tls},
			check: func(t *testing.T, c *Config) {
				if c.HTTPSPort != 443 || c.HTTPPort != 80 || !c.TLS() || c.bind() != "0.0.0.0" {
					t.Errorf("ports %d/%d tls %v bind %s", c.HTTPSPort, c.HTTPPort, c.TLS(), c.bind())
				}
			}},
		{name: "https on another port", o: InstallOptions{ExternalURL: "https://git.example.test:8443/", TLSDir: tls, HTTPPort: 8080},
			check: func(t *testing.T, c *Config) {
				if c.HTTPSPort != 8443 || c.HTTPPort != 8080 || c.ExternalURL != "https://git.example.test:8443" {
					t.Errorf("got %+v", c)
				}
			}},
		{name: "plain HTTP on loopback", o: InstallOptions{ExternalURL: "http://127.0.0.1:8080"},
			check: func(t *testing.T, c *Config) {
				if c.HTTPPort != 8080 || c.TLS() || c.bind() != "127.0.0.1" {
					t.Errorf("got %+v", c)
				}
			}},
		{name: "https without TLS files", o: InstallOptions{ExternalURL: "https://git.example.test"}, wantErr: "--tls-dir"},
		{name: "plain HTTP for other machines", o: InstallOptions{ExternalURL: "http://git.example.test"}, wantErr: "this machine only"},
		{name: "plain HTTP on all interfaces", o: InstallOptions{ExternalURL: "http://0.0.0.0:8080"}, wantErr: "this machine only"},
		{name: "TLS files with plain HTTP", o: InstallOptions{ExternalURL: "http://127.0.0.1", TLSDir: tls}, wantErr: "https URL"},
		{name: "a path", o: InstallOptions{ExternalURL: "https://git.example.test/git2", TLSDir: tls}, wantErr: "scheme://host"},
		{name: "another scheme", o: InstallOptions{ExternalURL: "ftp://git.example.test"}, wantErr: "https://"},
		{name: "bad port", o: InstallOptions{ExternalURL: "https://git.example.test:99999", TLSDir: tls}, wantErr: "port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := NewConfig(c.o)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one mentioning %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, cfg)
		})
	}
}

func TestSecretsAreFreshAndLongEnough(t *testing.T) {
	a, b := plainConfig(t), plainConfig(t)
	if len(a.Secrets.JWTSecret) < 32 || len(a.Secrets.PostgresPassword) < 32 {
		t.Fatalf("secrets too short: %d, %d", len(a.Secrets.JWTSecret), len(a.Secrets.PostgresPassword))
	}
	if a.Secrets.JWTSecret == b.Secrets.JWTSecret || a.Secrets.PostgresPassword == b.Secrets.PostgresPassword {
		t.Fatal("two installs got the same secret")
	}
}

func TestEnvHasTLSSettingsOnlyWithTLS(t *testing.T) {
	plain, _ := plainConfig(t).Env()
	if bytes.Contains(plain, []byte("GIT2_TLS_DIR")) || !bytes.Contains(plain, []byte("GIT2_BIND=127.0.0.1\n")) {
		t.Errorf("plain env:\n%s", plain)
	}
	c, err := NewConfig(InstallOptions{ExternalURL: "https://git.example.test", TLSDir: tlsDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := c.Env()
	for _, want := range []string{"GIT2_EXTERNAL_URL=https://git.example.test\n", "GIT2_HTTPS_PORT=443\n", "GIT2_HTTP_PORT=80\n", "GIT2_BIND=0.0.0.0\n", "GIT2_TLS_DIR=/"} {
		if !bytes.Contains(env, []byte(want)) {
			t.Errorf("TLS env lacks %q:\n%s", want, env)
		}
	}
}

func TestEnvRefusesLineBreaks(t *testing.T) {
	c := plainConfig(t)
	c.Source.Git2 = "../../git2\nJWT_SECRET=attacker"
	if _, err := c.Env(); err == nil {
		t.Fatal("a value with a line break could add a variable to .env")
	}
}

func TestPreflightNamesWhatIsMissing(t *testing.T) {
	cases := []struct {
		name    string
		docker  *fakeDocker
		setup   func(t *testing.T, dir string, c *Config)
		wantErr string
	}{
		{"docker not running", &fakeDocker{infoErr: true}, nil, "Docker daemon is not running"},
		{"unsupported architecture", &fakeDocker{arch: "s390x", compose: "2.29.1"}, nil, `"s390x" is not supported`},
		{"compose too old", &fakeDocker{arch: "x86_64", compose: "2.24.3"}, nil, "2.24.3 is too old"},
		{"compose version unreadable", &fakeDocker{arch: "x86_64", compose: "dev"}, nil, "too old"},
		{"port taken", okDocker(), func(t *testing.T, _ string, c *Config) {
			l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(c.HTTPPort))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { l.Close() })
		}, "in use"},
		{"already installed", okDocker(), func(t *testing.T, dir string, _ *Config) {
			if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte("version: 1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "already installed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cfg := t.TempDir(), plainConfig(t)
			if c.setup != nil {
				c.setup(t, dir, cfg)
			}
			err := Preflight(context.Background(), c.docker, dir, cfg)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, c.wantErr)
			}
		})
	}
	if err := Preflight(context.Background(), okDocker(), t.TempDir(), plainConfig(t)); err != nil {
		t.Fatalf("a good host failed preflight: %v", err)
	}
}

func TestPreflightNeedsTheTLSFiles(t *testing.T) {
	d := t.TempDir()
	c, err := NewConfig(InstallOptions{ExternalURL: "https://git.example.test:" + strconv.Itoa(freePort(t)), TLSDir: d, HTTPPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := Preflight(context.Background(), okDocker(), t.TempDir(), c); err == nil || !strings.Contains(err.Error(), "fullchain.pem") {
		t.Fatalf("err = %v, want the missing fullchain.pem named", err)
	}
}

func TestComposeVersions(t *testing.T) {
	for v, want := range map[string]bool{
		"2.24.4": true, "2.24.3": false, "2.25.0": true, "v2.29.1-desktop.1": true,
		"5.5.0": true, "2.24": false, "1.29.2": false, "": false, "x.y.z": false,
	} {
		if got := atLeast(v, minCompose); got != want {
			t.Errorf("atLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

func installer(t *testing.T, d *fakeDocker, probe Prober) (*Installer, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "git2")
	return &Installer{Dir: dir, Project: "git2-test", Run: d, Out: io.Discard,
		ReadyTimeout: time.Second, PollEvery: 5 * time.Millisecond, Probe: probe}, dir
}

func readyProbe(_ context.Context, path string) (int, error) {
	if path == "/api/auth/me" {
		return http.StatusUnauthorized, nil
	}
	return http.StatusOK, nil
}

func TestInstallWritesPrivateFilesAndStartsCompose(t *testing.T) {
	d := okDocker()
	in, dir := installer(t, d, readyProbe)
	c := plainConfig(t)
	if err := in.Install(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{ConfigFile: 0o600, envFile: 0o600, "compose/compose.yaml": 0o644,
		"compose/compose.tls.yaml": 0o644, "images/gateway/Dockerfile": 0o644} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", name, st.Mode().Perm(), want)
		}
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("install dir mode %v, want 0700", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ConfigFile))
	var back Config
	if err := yaml.Unmarshal(raw, &back); err != nil || back.Secrets.JWTSecret != c.Secrets.JWTSecret || back.Version != ConfigVersion {
		t.Fatalf("git2.yaml does not read back: %v", err)
	}
	up := d.composeCall("up")
	if up == nil || !slices.Contains(up, "--build") || slices.Contains(up, "--profile") ||
		slices.Contains(up, filepath.Join(dir, "compose", "compose.tls.yaml")) {
		t.Fatalf("compose up = %v", up)
	}
	if !slices.Contains(up, "git2-test") || !slices.Contains(up, filepath.Join(dir, envFile)) {
		t.Fatalf("compose up does not use the project and the env file: %v", up)
	}
}

func TestInstallWithTLSAndGitSvc(t *testing.T) {
	d := okDocker()
	var probed []string
	in, dir := installer(t, d, func(ctx context.Context, path string) (int, error) {
		probed = append(probed, path)
		return readyProbe(ctx, path)
	})
	c, err := NewConfig(InstallOptions{ExternalURL: "https://git.example.test:" + strconv.Itoa(freePort(t)), TLSDir: tlsDir(t),
		HTTPPort: freePort(t), WithGitSvc: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Install(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	up := d.composeCall("up")
	if !slices.Contains(up, filepath.Join(dir, "compose", "compose.tls.yaml")) || !slices.Contains(up, "git-svc") {
		t.Fatalf("compose up = %v, want compose.tls.yaml and the git-svc profile", up)
	}
	if !slices.Contains(probed, "/readyz") {
		t.Fatalf("probed %v, want /readyz checked when git-svc runs", probed)
	}
}

func TestInstallWritesNothingWhenPreflightFails(t *testing.T) {
	d := &fakeDocker{infoErr: true}
	in, dir := installer(t, d, readyProbe)
	if err := in.Install(context.Background(), plainConfig(t)); err == nil {
		t.Fatal("install went on without Docker")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the install directory was created: %v", err)
	}
	if d.composeCall("up") != nil {
		t.Fatal("compose up ran after a failed preflight")
	}
}

func TestInstallReportsAFailedStart(t *testing.T) {
	t.Run("compose up fails", func(t *testing.T) {
		d := okDocker()
		d.upErr = true
		in, _ := installer(t, d, readyProbe)
		err := in.Install(context.Background(), plainConfig(t))
		if err == nil || !strings.Contains(err.Error(), "pull access denied") {
			t.Fatalf("err = %v, want compose's message", err)
		}
	})
	t.Run("never ready", func(t *testing.T) {
		d := okDocker()
		in, _ := installer(t, d, func(context.Context, string) (int, error) { return http.StatusServiceUnavailable, nil })
		err := in.Install(context.Background(), plainConfig(t))
		if err == nil || !strings.Contains(err.Error(), "did not become ready") || !strings.Contains(err.Error(), "logs") {
			t.Fatalf("err = %v, want a timeout that points at the logs", err)
		}
		if d.composeCall("ps") == nil {
			t.Fatal("no `compose ps` to show which service failed")
		}
		if d.composeCall("down") != nil {
			t.Fatal("the containers were removed; E3 leaves them running for the logs")
		}
	})
}
