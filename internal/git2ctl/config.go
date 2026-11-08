// Package git2ctl installs and runs git2 on one Docker host (WBS-13.2,
// UC-01). It writes the embedded Compose files and an operator-facing
// git2.yaml into one directory and drives `docker compose` there.
package git2ctl

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ConfigVersion is the git2.yaml format this git2ctl writes and reads.
const ConfigVersion = 1

// Default sources until the image pipeline (WBS-13.4) publishes images:
// Compose builds from these Git URLs.
const (
	DefaultGit2Src      = "https://github.com/git2-project/git2.git#dev/main"
	DefaultCodeplaneSrc = "https://github.com/git2-project/git2-codeplane.git#main"
)

// Config is git2.yaml: everything an instance needs, in the one file the
// operator edits and the backup keeps (FR-70). It holds the secrets, so it
// is written 0600.
type Config struct {
	Version     int    `yaml:"version"`
	ExternalURL string `yaml:"external_url"` // what users type
	// TLSDir holds fullchain.pem and privkey.pem. Empty: plain HTTP, which
	// is allowed only on loopback (NFR-04).
	TLSDir     string  `yaml:"tls_dir,omitempty"`
	HTTPPort   int     `yaml:"http_port"`            // plain HTTP; with TLS only the redirect
	HTTPSPort  int     `yaml:"https_port,omitempty"` // with TLS
	WithGitSvc bool    `yaml:"with_git_svc"`
	Source     Source  `yaml:"source"`
	Secrets    Secrets `yaml:"secrets"`
}

type Source struct {
	Git2      string `yaml:"git2"`
	Codeplane string `yaml:"codeplane"`
}

type Secrets struct {
	PostgresPassword string `yaml:"postgres_password"`
	JWTSecret        string `yaml:"jwt_secret"`
}

// InstallOptions are what the operator chooses on the command line.
type InstallOptions struct {
	ExternalURL  string
	TLSDir       string
	HTTPPort     int // 0: 80 with TLS, the URL's port without
	WithGitSvc   bool
	Git2Src      string
	CodeplaneSrc string
}

// NewConfig checks the options and generates the secrets.
func NewConfig(o InstallOptions) (*Config, error) {
	u, err := url.Parse(o.ExternalURL)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("--url must be scheme://host[:port], got %q", o.ExternalURL)
	}
	// Compose resolves a relative path against the install directory, not
	// where git2ctl ran, so the TLS directory is stored absolute.
	if o.TLSDir != "" {
		if o.TLSDir, err = filepath.Abs(o.TLSDir); err != nil {
			return nil, err
		}
	}
	c := &Config{
		Version:     ConfigVersion,
		ExternalURL: u.Scheme + "://" + u.Host,
		TLSDir:      o.TLSDir,
		WithGitSvc:  o.WithGitSvc,
		Source:      Source{Git2: or(o.Git2Src, DefaultGit2Src), Codeplane: or(o.CodeplaneSrc, DefaultCodeplaneSrc)},
	}
	switch u.Scheme {
	case "https":
		if o.TLSDir == "" {
			return nil, fmt.Errorf("an https URL needs --tls-dir with fullchain.pem and privkey.pem")
		}
		if c.HTTPSPort, err = port(u, 443); err != nil {
			return nil, err
		}
		c.HTTPPort = o.HTTPPort
		if c.HTTPPort == 0 {
			c.HTTPPort = 80
		}
	case "http":
		// NFR-04: no UI or API over plain HTTP, except on this machine.
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("plain HTTP is for this machine only (127.0.0.1 or localhost); use an https URL with --tls-dir")
		}
		if o.TLSDir != "" {
			return nil, fmt.Errorf("--tls-dir needs an https URL")
		}
		if c.HTTPPort, err = port(u, 80); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("--url must start with https:// (or http:// for 127.0.0.1)")
	}
	if c.Secrets.PostgresPassword, err = randomHex(24); err != nil {
		return nil, err
	}
	if c.Secrets.JWTSecret, err = randomHex(32); err != nil {
		return nil, err
	}
	return c, nil
}

// TLS reports whether the gateway terminates TLS.
func (c *Config) TLS() bool { return c.TLSDir != "" }

// Ports are the host ports the instance publishes.
func (c *Config) Ports() []int {
	if c.TLS() {
		return []int{c.HTTPSPort, c.HTTPPort}
	}
	return []int{c.HTTPPort}
}

// bind is the host address the gateway listens on: every interface with
// TLS, loopback without.
func (c *Config) bind() string {
	if c.TLS() {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// YAML is git2.yaml's content.
func (c *Config) YAML() ([]byte, error) {
	body, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	header := "# git2.yaml: written by `git2ctl install`. Holds the instance's secrets:\n" +
		"# keep it private (0600) and in the backup. Docs: git2-deploy docs/api/git2ctl.md\n"
	return append([]byte(header), body...), nil
}

// Env is the .env Compose reads, derived from git2.yaml. Values are hex,
// URLs and paths; a newline in any of them is refused so no line can add a
// variable.
func (c *Config) Env() ([]byte, error) {
	vars := [][2]string{
		{"POSTGRES_PASSWORD", c.Secrets.PostgresPassword},
		{"JWT_SECRET", c.Secrets.JWTSecret},
		{"GIT2_BIND", c.bind()},
		{"GIT2_HTTP_PORT", strconv.Itoa(c.HTTPPort)},
		{"GIT2_SRC", c.Source.Git2},
		{"CODEPLANE_SRC", c.Source.Codeplane},
	}
	if c.TLS() {
		vars = append(vars,
			[2]string{"GIT2_EXTERNAL_URL", c.ExternalURL},
			[2]string{"GIT2_TLS_DIR", c.TLSDir},
			[2]string{"GIT2_HTTPS_PORT", strconv.Itoa(c.HTTPSPort)})
	}
	var b strings.Builder
	b.WriteString("# Generated by git2ctl from git2.yaml. Edit git2.yaml, not this file.\n")
	for _, kv := range vars {
		if strings.ContainsAny(kv[1], "\r\n") {
			return nil, fmt.Errorf("%s contains a line break", kv[0])
		}
		fmt.Fprintf(&b, "%s=%s\n", kv[0], kv[1])
	}
	return []byte(b.String()), nil
}

func port(u *url.URL, fallback int) (int, error) {
	if u.Port() == "" {
		return fallback, nil
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("bad port in --url: %q", u.Port())
	}
	return p, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func or(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
