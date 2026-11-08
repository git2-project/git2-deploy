# git2ctl

`git2ctl` installs git2 on one Linux or macOS host with Docker (WBS-13.2, UC-01, FR-77). Source: `cmd/git2ctl`, `internal/git2ctl`.

**Status:** `install` works (W3). `upgrade` (FR-78) follows in W4, `restore` (FR-75) in W7–W8.

## install

```sh
go build -o git2ctl ./cmd/git2ctl     # until releases ship binaries (WBS-13.4)

# For other machines: HTTPS, with the certificate in /etc/git2/tls
sudo ./git2ctl install --url https://git.example.com --tls-dir /etc/git2/tls --with-git-svc

# This machine only: plain HTTP on loopback
./git2ctl install --dir ~/git2 --url http://127.0.0.1:8080 --with-git-svc
```

What it does, in order (UC-01):

1. **Preflight.** It writes nothing until every check passes, and names the first one that fails:
   - the directory has no `git2.yaml` yet (otherwise: use `git2ctl upgrade`);
   - the Docker daemon answers, and its architecture is amd64 or arm64 (NFR-24);
   - Docker Compose is 2.24.4 or newer (`compose.tls.yaml` uses `!override`);
   - with TLS, `fullchain.pem` and `privkey.pem` are in `--tls-dir`;
   - the ports are free.
2. **Writes** the install directory: `git2.yaml` (0600), `compose/.env` (0600, generated from `git2.yaml`), `compose/` and `images/` as in this repo. The directory is 0700. Secrets are new random hex: 24 bytes for the database password, 32 for the JWT key.
3. **Starts** `docker compose up -d --build` in that directory, with `compose.tls.yaml` for an https URL and the `git-svc` profile with `--with-git-svc`.
4. **Waits** until git2 core answers through the gateway (`/api/auth/me` is 401) and, with git-svc, until `/readyz` is 200. With TLS the check trusts the instance's own `fullchain.pem` and verifies the external host name.
5. **Prints** the address. Opening it shows the first-run setup.

If the stack does not become ready in `--timeout` (8 minutes by default, inside NFR-15's 10), it prints `docker compose ps` and leaves the containers running so their logs can be read.

| Flag | Default | Meaning |
|---|---|---|
| `--url` | (required) | The address users type. `https://<host>[:port]`, or `http://127.0.0.1:<port>` / `http://localhost:<port>` for this machine only (NFR-04: no plain HTTP for others). |
| `--tls-dir` | | `fullchain.pem` and `privkey.pem`, readable by uid 65532. Required for https. |
| `--http-port` | `80` | With https: the plain-HTTP port, which only redirects. |
| `--with-git-svc` | off | Also run git-svc (Git over HTTPS). Off until git2-codeplane#5 is on main. |
| `--dir` | `/opt/git2` | Install directory. |
| `--project` | `git2` | Compose project name. |
| `--git2-src`, `--codeplane-src` | GitHub, `dev/main` and `main` | Sources to build from, until images are published (WBS-13.4). A local checkout works too. |
| `--timeout` | `8m` | How long to wait for git2 to answer. |

## git2.yaml

The one file an operator keeps (FR-70 backs it up with the instance):

```yaml
version: 1
external_url: https://git.example.com
tls_dir: /etc/git2/tls
http_port: 80
https_port: 443
with_git_svc: true
source:
  git2: https://github.com/git2-project/git2.git#dev/main
  codeplane: https://github.com/git2-project/git2-codeplane.git#main
secrets:
  postgres_password: <hex>
  jwt_secret: <hex>
```

`compose/.env` is derived from it; edit `git2.yaml`, not `.env`. Backup settings (`backup.v1`: target and recipient) join this file with backup-svc (W4–W5).

## Not yet

- `upgrade` (W4): re-render `.env` from `git2.yaml`, pull or build the new images, run migrations, restart.
- Images from GHCR instead of a local build (WBS-13.4). The first install builds for a few minutes; later ones reuse Docker's cache.
- Certificate renewal: copy the new files into `tls_dir` and restart the gateway (ACME is open, git2-deploy#16).
