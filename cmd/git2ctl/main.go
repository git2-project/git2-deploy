// Command git2ctl installs git2 on one Docker host (WBS-13.2, UC-01).
//
//	git2ctl install --url https://git.example.com --tls-dir /etc/git2/tls
//	git2ctl install --url http://127.0.0.1:8080      # this machine only
//	git2ctl upgrade --codeplane-src https://github.com/git2-project/git2-codeplane.git#v0.3.0
//
// Reference: docs/api/git2ctl.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/git2-project/git2-deploy/internal/git2ctl"
)

const usage = `git2ctl installs and runs git2 on one Docker host.

Commands:
  install   check the host, write git2.yaml and start git2
  upgrade   rebuild and restart an installed git2, keeping its data and secrets

Run "git2ctl <command> -h" for its flags.
`

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "git2ctl:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("no command")
	}
	switch args[0] {
	case "install":
		return install(ctx, args[1:], stdout, stderr)
	case "upgrade":
		return upgrade(ctx, args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func install(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o git2ctl.InstallOptions
	dir := fs.String("dir", "/opt/git2", "install directory: git2.yaml, .env and the Compose files")
	project := fs.String("project", "git2", "Compose project name")
	timeout := fs.Duration("timeout", git2ctl.DefaultReadyTimeout, "how long to wait for git2 to answer")
	fs.StringVar(&o.ExternalURL, "url", "", "address users type: https://<host>[:port], or http://127.0.0.1:<port> for this machine only (required)")
	fs.StringVar(&o.TLSDir, "tls-dir", "", "directory with fullchain.pem and privkey.pem (required for https)")
	fs.IntVar(&o.HTTPPort, "http-port", 0, "with https: the plain-HTTP port that redirects (default 80)")
	fs.BoolVar(&o.WithGitSvc, "with-git-svc", true, "run git-svc, the Git engine (Git over HTTPS); false for an instance without Git")
	fs.StringVar(&o.Git2Src, "git2-src", "", "git2 source to build from (default "+git2ctl.DefaultGit2Src+")")
	fs.StringVar(&o.CodeplaneSrc, "codeplane-src", "", "git2-codeplane source to build from (default "+git2ctl.DefaultCodeplaneSrc+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.ExternalURL == "" {
		fs.Usage()
		return errors.New("--url is required")
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	cfg, err := git2ctl.NewConfig(o)
	if err != nil {
		return err
	}
	in := &git2ctl.Installer{
		Dir: absDir, Project: *project, Run: git2ctl.ExecRunner{Dir: absDir}, Out: stdout,
		ReadyTimeout: *timeout, PollEvery: 2 * time.Second,
	}
	return in.Install(ctx, cfg)
}

func upgrade(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o git2ctl.UpgradeOptions
	dir := fs.String("dir", "/opt/git2", "install directory with git2.yaml")
	project := fs.String("project", "git2", "Compose project name, as at install")
	timeout := fs.Duration("timeout", git2ctl.DefaultReadyTimeout, "how long to wait for git2 to answer")
	fs.StringVar(&o.Git2Src, "git2-src", "", "move git2 to this source (default: keep the one in git2.yaml)")
	fs.StringVar(&o.CodeplaneSrc, "codeplane-src", "", "move git2-codeplane to this source, e.g. a newer tag (default: keep)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	in := &git2ctl.Installer{
		Dir: absDir, Project: *project, Run: git2ctl.ExecRunner{Dir: absDir}, Out: stdout,
		ReadyTimeout: *timeout, PollEvery: 2 * time.Second,
	}
	return in.Upgrade(ctx, o)
}
