# pgAdmin deployment and operations

The production stack publishes pgAdmin only through a dedicated Nginx reverse proxy at
`https://pg.<domain>:14433`. The pgAdmin container has no host port. Nginx and
pgAdmin share a dedicated internal Docker network; pgAdmin listens only on that
network address. A second, separate internal network connects only pgAdmin and
PostgreSQL. The dedicated proxy shares no network with the application, so
pgAdmin cannot reach the application, Redis, observability, or host-facing
services.

Access has two independent login steps:

1. Nginx HTTP Basic Authentication, using a bcrypt `htpasswd` secret.
2. pgAdmin's built-in `internal` email/password authentication.

PostgreSQL remains private: Compose does not publish a PostgreSQL host port.

## Prepare credentials

Create the Git-ignored secret directory in the checkout as the same unprivileged
user that runs deployments. The deployment script rejects symlinks and unsafe
permissions for these files. Do not run deployment with `sudo` when using this
layout.

```bash
cd /srv/yamata
install -d -m 0700 .secrets
install -m 0600 /dev/null .secrets/pgadmin.env
${EDITOR:-vi} .secrets/pgadmin.env
```

The environment file must contain the pgAdmin administrator email only:

```env
PGADMIN_DEFAULT_EMAIL=database-admin@example.com
```

Create a long random initial pgAdmin password in its own file. Compose
file-backed secrets retain the host file's ownership and mode. The pgAdmin
image runs as UID `5050` with primary GID `0`, so the deployment user must own
this file, its group must be `root`, and its mode must be `0640`. Store the
value in the organisation's password manager before deployment.

```bash
(umask 077; openssl rand -base64 48 > .secrets/pgadmin-default-password)
sudo chgrp root .secrets/pgadmin-default-password
chmod 0640 .secrets/pgadmin-default-password
```

Create a separate Nginx Basic Auth identity. `htpasswd` prompts for the
password and stores a bcrypt hash, never the clear-text value. Nginx workers
run as UID/GID 65534, so the deployment user must own the hash file and it
must be group-readable by numeric group `65534`, but inaccessible to other
users. Confirm that host group `65534` has no untrusted local members.

```bash
htpasswd -B -C 12 -c .secrets/pgadmin-nginx.htpasswd pgadmin-edge-admin
sudo chgrp 65534 .secrets/pgadmin-nginx.htpasswd
chmod 0640 .secrets/pgadmin-nginx.htpasswd
```

Set these path references in `.env.beta` (the template already provides these
values). They are file paths only, not credentials:

```env
PGADMIN_ENV_FILE=./.secrets/pgadmin.env
PGADMIN_DEFAULT_PASSWORD_FILE=./.secrets/pgadmin-default-password
PGADMIN_NGINX_HTPASSWD_FILE=./.secrets/pgadmin-nginx.htpasswd
# The IPv4 address of the host interface intended for administrator access.
PGADMIN_LISTEN_BIND_IP=10.8.0.1
```

Do not use `0.0.0.0` or a loopback address for `PGADMIN_LISTEN_BIND_IP`; the
deployment rejects both. A public interface is supported when the host's PAM,
firewall, and organization access controls protect it. Keep TCP/14433 limited
to the intended administrator population.

The primary certificate at `/etc/letsencrypt/live/<domain>/fullchain.pem` must
include `pg.<domain>` as a SAN or through a wildcard name. The deployment
script verifies this before it starts Nginx.

## Deploy and verify

Run the normal production deployment; it starts and health-checks pgAdmin
before recreating Nginx.

```bash
./scripts/deploy-production-beta.sh --domain example.com
```

Verify the two authentication layers without putting credentials on the
command line:

```bash
curl -I https://pg.example.com:14433
# Expected: HTTP/2 401 with WWW-Authenticate: Basic

curl --user pgadmin-edge-admin https://pg.example.com:14433
# Expected: pgAdmin's own login page
```

Use pgAdmin to register the internal database with host `pgadmin-postgres` and
port `5432`. Use a named PostgreSQL administrator role scoped to the required
maintenance work, not the `postgres` superuser or the application's service
account. Database-password saving is disabled, so enter that credential when
connecting from a password manager. Do not publish a PostgreSQL port or create
a host-level port-forward as a shortcut.

The initial pgAdmin email/password is applied only when the pgAdmin data volume
is first initialized. Rotating that password later must be done through the
pgAdmin UI; rotating the Nginx credential requires regenerating the htpasswd
file and rerunning the normal deployment so Nginx is recreated.

The Query Tool is configured to show autocomplete suggestions as you type. The
deployment applies this setting to the configured pgAdmin administrator on
every run, including an existing pgAdmin data volume. Close and reopen any
Query Tool tabs after deployment for the preference to take effect. It can also
be triggered manually on a deployed host:

```bash
docker exec --user 5050:0 yamata-pgadmin-beta sh -ec \
  'cd /pgadmin4 && exec /venv/bin/python3 setup.py set-prefs \
  "$PGADMIN_DEFAULT_EMAIL" \
  "sqleditor:auto_completion:autocomplete_on_key_press=true"'
```

## Security controls

- `dpage/pgadmin4:9.17@sha256:2f4ce946ddf8360680d7eff4eaba1d91859eb6b4003e6623bad5c63a322c2f4d`
  pins the official pgAdmin 9.17 multi-architecture OCI index; review and
  advance the reference for new upstream security releases.
- pgAdmin's root filesystem is read-only, Linux capabilities are dropped, and
  no-new-privileges is enabled. Its persistent data is the dedicated Docker
  volume only; its required runtime and application-log directories are
  non-persistent tmpfs mounts.
- The dedicated Nginx proxy uses non-persistent request-body and upstream
  temporary storage. Its otherwise capability-restricted master retains only
  the `CHOWN` capability needed to hand these directories to its unprivileged
  workers during startup.
- pgAdmin joins only two internal networks: one with Nginx and one with
  PostgreSQL. The Nginx listener is the only published path, on TCP/14433 and
  bound to the explicit interface selected by `PGADMIN_LISTEN_BIND_IP`.
- Nginx terminates TLS with the existing domain certificate, requires Basic
  Auth for every pgAdmin request, applies request/connection limits, keeps
  connection establishment short, allows five minutes for upstream query
  responses, disables response caching, and preserves pgAdmin's
  application-aware CSP.
- pgAdmin accepts only `pg.<domain>` as its host header, trusts exactly one
  reverse proxy, uses secure/HTTP-only/Strict cookies, enforces internal
  authentication, locks accounts after three failed pgAdmin logins, and
  disables SSH tunnelling, database-password saving/query history, and Gravatar
  lookups.
