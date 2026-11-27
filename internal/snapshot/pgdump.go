package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// PgDump returns a Database source that runs
// `pg_dump --format=custom` against dsn and streams the dump.
//
// pg_dump takes one snapshot for the whole run, so every schema is from the
// same moment even while the services keep writing (PostgreSQL docs,
// "pg_dump": "consistent backups even if the database is being used
// concurrently"). The custom format is the one pg_restore reads selectively
// and in parallel.
//
// The password goes in PGPASSWORD, never on the command line, where other
// users of the host could read it from the process list. dsn must not carry
// it either.
func PgDump(bin, dsn, password string) func(ctx context.Context, w io.Writer) error {
	return func(ctx context.Context, w io.Writer) error {
		cmd := exec.CommandContext(ctx, bin, "--format=custom", "--no-password", "--dbname="+dsn)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		cmd.Stdout = w
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			// pg_dump's stderr names the failing object; it never echoes the
			// password, which is only in the environment.
			return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(lastLine(stderr.String())))
		}
		return nil
	}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
