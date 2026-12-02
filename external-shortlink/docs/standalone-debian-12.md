# Running external-shortlink on the standalone Debian 12 server

This runbook deploys the redirect service and its private PostgreSQL database
on the dedicated Debian 12 VM. The project directory must be owned by the
`debian` account and located at:

```text
/home/debian/Yamata-no-Orochi/
```

The external-shortlink source can be either the `external-shortlink/`
subdirectory of that checkout or its own clean Git repository at that path.

The public short-link domain is **`jzbe.ir`**. Supplying the production
application's fixed, globally routable egress IP restricts `/api/` to that
address. It is optional; without it, `/api/` relies on bearer-token
authentication alone.

## Deploy

The checked-in deployment script is the supported path. Its legacy filename is
retained for compatibility, but it rejects every host except Debian 12. Run it
from the service source directory as the `debian` account with `sudo`:

```sh
cd /home/debian/Yamata-no-Orochi/external-shortlink

sudo install -o root -g root -m 0600 /dev/null /root/external-shortlink-api-token
sudoedit /root/external-shortlink-api-token

sudo ./scripts/deploy-debian-12.sh deploy \
  --api-token-file /root/external-shortlink-api-token \
  --acme-email 'ops@example.com' \
  --configure-ufw
```

If the production host has a fixed egress address, add
`--production-ip 'THE_PRODUCTION_EGRESS_IP'` to apply an Nginx source-IP
allowlist to `/api/`. Otherwise omit it; the API remains protected by its
bearer token, but accepts connections from any source address.

The token file must contain the exact 32+-character URL-safe token configured
on the production application. It must contain one value using only letters,
numbers, `.`, `_`, `~`, or `-`; never add it to the repository or pass it on
the command line. `openssl rand -hex 32` generates an appropriate value.

Before making changes, the script validates root access, Debian 12, systemd,
the `debian` account and project ownership, x86_64/aarch64, at least 4 vCPUs,
4 GB RAM, 40 GiB free on `/`, the locked source tree, an optional production
egress IP, and required environment values. It then installs any missing Debian packages,
configures Docker's official Debian repository, installs Docker Engine with
Compose v2, installs Rust 1.85 for `debian`, and builds the release binary as
that account.

The host must not already have conflicting Debian Docker packages installed:
`docker.io`, `docker-compose`, `docker-doc`, `docker-buildx`, `podman-docker`,
`containerd`, or `runc`. The script stops with the package names instead of
removing them, because they may belong to another workload.

The script stores root-owned release files in `/opt/external-shortlink` and
runs the public service as the separate unprivileged `external-shortlink`
account. This keeps the source checkout writable by `debian` without allowing
that account to replace the running binary.

`--acme-email` is needed only until a valid certificate exists. The first
certificate request requires the `jzbe.ir` A/AAAA record to point to the VM and
TCP port 80 to be reachable. `--configure-ufw` safely allows the current SSH
port and Nginx HTTP/HTTPS profile; omit it if a different firewall policy is
managed outside the VM. Never open ports 5432, 5433, or 8081.

## Safe retry behavior

It is safe to rerun the same `deploy` command after a recoverable failure. The
script preserves existing database passwords, refuses to silently change the
API token, validates both environment files before use, reapplies the schema
and runtime-role grants idempotently, and uses `docker compose up -d`, managed
systemd units, and Nginx symlinks that can be applied repeatedly. It also
refuses a dirty or untracked external-service source tree, so the installed
release always comes from the reviewed Git commit.

Every non-log command writes console output and failure diagnostics to the
root-only file:

```text
/var/log/external-shortlink/deploy.log
```

On an operational failure it also records the current systemd state, the last
100 service journal entries, and the PostgreSQL Compose status/log tail. No
script command prints the API token or database passwords.

## Operate the service

All commands below are idempotent where applicable. `stop` stops only the Rust
redirect service; PostgreSQL stays running so data remains available for a
quick start.

```sh
cd /home/debian/Yamata-no-Orochi/external-shortlink

sudo ./scripts/deploy-debian-12.sh start
sudo ./scripts/deploy-debian-12.sh stop
sudo ./scripts/deploy-debian-12.sh restart
sudo ./scripts/deploy-debian-12.sh status
```

View recent service history, start history from a time bound, or continue
watching the live journal:

```sh
sudo ./scripts/deploy-debian-12.sh logs
sudo ./scripts/deploy-debian-12.sh logs --lines 500 --since '2 hours ago'
sudo ./scripts/deploy-debian-12.sh logs --follow
```

After a deployment or restart, verify local readiness and the proxy path:

```sh
curl -fsS http://127.0.0.1:8081/healthz
curl -fsS http://127.0.0.1:8081/readyz
curl -fsSI https://jzbe.ir/healthz
```

## Production connection and backup

Apply production migration `0135_external_short_link_sync.sql`, configure the
production application with `EXTERNAL_SHORTLINK_BASE_URL=https://jzbe.ir` and
the same API token, then test an authenticated mapping upload from the actual
production egress address before enabling the integration.

Back up PostgreSQL off-host and include the durable SQLite spool at
`/var/lib/external-shortlink/click-spool.sqlite3` together with its `-wal` and
`-shm` files in recovery planning. A Docker volume snapshot alone is not a
tested backup strategy.
