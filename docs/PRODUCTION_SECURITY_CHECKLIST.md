# Production security checklist

Use this checklist with the active production Compose deployment. It distinguishes
controls already present in the repository from controls that still require
host, provider, or organizational work. A checked box must reflect the deployed
environment, not merely the existence of a configuration field.

## Before deployment

### Host and network

- [ ] Apply supported OS, Docker Engine, Compose, and package security updates.
- [ ] Restrict SSH to authorized keys and operators; disable password/root login
  where operationally appropriate.
- [ ] Permit only required inbound traffic. The Yamata stack should publish
  only TCP 80/443; expose SSH through the host’s controlled management path.
- [ ] Confirm PostgreSQL, Redis, Grafana, Prometheus, exporters, the API, and
  the campaign scheduler are not directly published.
- [ ] Confirm Docker subnet `172.30.0.0/24` does not conflict with host, VPN, or
  upstream networks.
- [ ] Protect Docker socket access as root-equivalent and review membership of
  the `docker` group.

### Secrets and identities

- [ ] Keep `.env.beta` a regular file with mode `0600`; never commit it.
- [ ] Replace every `env.template` placeholder. The production parser rejects
  the known `$domain`, `$db_password`, and related provisioning placeholders.
- [ ] Use unique, high-entropy JWT, database, Redis, GlitchTip, Grafana, bot,
  payment, SMS, and messaging credentials.
- [ ] Verify system/tax UUIDs, mobiles, emails, wallets, and Sheba identity
  values against the intended production records.
- [ ] Store backups, environment files, provider tokens, private keys, and
  prompt-related API keys in an approved encrypted secret/backup system.
- [ ] Establish credential rotation and emergency revocation procedures.

### TLS and DNS

- [ ] Provision valid certificates for every hostname referenced by the
  rendered nginx configuration before deployment.
- [ ] Run `scripts/check-yamata-certificates.sh`; it validates but does not issue
  or renew certificates.
- [ ] Configure an authorized renewal mechanism outside this repository and
  test it before expiry.
- [ ] Keep `yamata-cert-monitor-beta` alerts configured; monitoring is not a
  substitute for renewal.
- [ ] Review DNS ownership, registrar MFA, DNSSEC support, and cutover/rollback
  TTLs.

### Database and data

- [ ] Use strong SCRAM credentials and keep PostgreSQL private.
- [ ] Encrypt disks and backup destinations; encrypt backup transfers.
- [ ] Test both full-stack restoration and any selective data restore used in
  operations.
- [ ] Grant export/repair roles only the required tables and operations.
- [ ] Treat PostgreSQL dumps as untrusted. Use the checked-in restore tools,
  which allowlist `COPY` sections, rather than executing arbitrary dump SQL.
- [ ] Define retention and deletion rules for audience identifiers, phone
  numbers, campaign data, logs, and backups.

## Application and edge checks

### Authentication and authorization

- [ ] `JWT_SECRET_KEY` is at least 32 characters and differs between
  environments.
- [ ] Access and refresh TTLs match the approved session policy.
- [ ] Customer, admin, and bot credentials cannot be used across the wrong
  authentication surface.
- [ ] Admin routes enforce both admin authentication and the central permission
  registry.
- [ ] Maker-checker permissions and privileged admin operations have been
  exercised in a staging environment.
- [ ] OTP bypass and forwarding lists contain only approved numbers and are
  empty when not required.

### Request controls

- [ ] Review the actual hard-coded production Fiber CORS allowlist in
  `app/router/routes.go`; `CORS_ALLOWED_ORIGINS` applies only to development.
- [ ] Review Fiber’s hard-coded limits (2,000 API and 20 auth requests/minute)
  and nginx’s stricter zones (100 API, 5 auth, and 200 general requests/minute
  per IP), including burst behavior.
- [ ] Confirm proxy headers can only arrive through trusted nginx paths and
  test the effective client IP used for limiting and audit logs.
- [ ] Confirm nginx rejects proxy methods, absolute-form URIs, suspicious
  proxy headers, dot files, backup/config extensions, and server-status probes.
- [ ] Review upload limits: 100 MiB for media and bot audience uploads, 50 MiB
  for admin short-link CSV, and 10 MiB as nginx’s general default.
- [ ] Validate uploaded media type, storage permissions, malware policy, and
  retention with representative files.

### Headers and transport

- [ ] Verify HTTP redirects to HTTPS and no application service is exposed over
  plain HTTP outside the Docker network.
- [ ] Verify the negotiated TLS versions/ciphers. Nginx currently permits TLS
  1.2 and 1.3; `TLS_MIN_VERSION` is not the nginx source of truth.
- [ ] Verify HSTS, CSP, frame, content-type, referrer, permissions, and
  cross-origin headers on the actual main, API, monitoring, and Sentry routes.
- [ ] Reconcile the edge and Fiber header policies where they differ; test the
  final header seen by clients.

### Containers and dependencies

- [ ] Build from the reviewed release commit and record image digests.
- [ ] Scan Go modules, Python dependencies, base images, and frontend images.
- [ ] Confirm the API runs as `appuser`, uses a read-only root filesystem,
  mounts only required writable volumes/tmpfs paths, and has
  `no-new-privileges`.
- [ ] Review every service without `read_only`, `cap_drop`, a non-root user, or
  explicit resource limits. The Compose stack is not uniformly hardened.
- [ ] Pin or formally accept floating images such as the current
  `glitchtip/glitchtip:latest` before production use.
- [ ] Confirm prompt files and nginx/database configuration bind mounts are
  read-only and contain no secrets that belong in environment/secret storage.

## Worker and operational safety

- [ ] `yamata-app-beta` has campaign execution off, exact-capacity and Test
  sampling scheduling on, execution-calculation scheduling on, and smart-tag
  scheduling on.
- [ ] `yamata-campaign-scheduler-beta` has campaign execution on and exact
  capacity, Test sampling, execution calculation, Tag Test reporting, audience
  reports, bundle action-file processing, refund reconciliation, and smart-tag
  schedulers off.
- [ ] `yamata-app-beta` owns campaign-audience reports, bundle action-file
  processing, refund reconciliation, and external short-link synchronization.
- [ ] `CAMPAIGN_MESSAGE_SEND_MOCK_ENABLED=false` in production.
- [ ] The scheduler has no published ports and uses
  `http://app-beta:8080` for its internal bot client.
- [ ] Deployments use `scripts/deploy-production-beta.sh`; a raw restart is not
  accepted as an environment reload.
- [ ] Migrations and selective restores stop both writer containers.
- [ ] Destructive repair tools are dry-run by default or require an explicit
  reviewed apply flag.

## Logging, monitoring, and response

- [ ] Request IDs appear in access/application logs and can be followed across
  the edge and API.
- [ ] GlitchTip/Sentry DSN, environment, release, and server names distinguish
  API and campaign scheduler events.
- [ ] Logs redact tokens, passwords, phone numbers, dump contents, and provider
  response bodies where required by policy.
- [ ] Log and metric volumes have retention, capacity alerts, and tamper/access
  controls.
- [ ] PostgreSQL, Redis, API, nginx, certificate, queue/worker, disk, backup,
  and provider failure alerts have owners and tested escalation paths.
- [ ] Incident response, breach notification, forensic preservation, service
  rollback, and credential rotation runbooks have named owners.

## Verification commands

Replace `example.com` and `/srv/yamata` with the deployed values.

```bash
/srv/yamata/scripts/check-yamata-production.sh /srv/yamata
docker compose --env-file /srv/yamata/.env.beta \
  -f /srv/yamata/docker-compose.beta.yml ps
curl --fail --show-error https://example.com/api/v1/health
curl --head https://example.com/
openssl s_client -connect example.com:443 -servername example.com </dev/null
```

Inspect the published ports and security settings:

```bash
docker ps --format 'table {{.Names}}\t{{.Ports}}'
docker inspect yamata-app-beta \
  --format 'User={{.Config.User}} Readonly={{.HostConfig.ReadonlyRootfs}} SecurityOpt={{json .HostConfig.SecurityOpt}}'
docker exec yamata-app-beta printenv CAMPAIGN_EXECUTION_ENABLED
docker exec yamata-campaign-scheduler-beta printenv CAMPAIGN_EXECUTION_ENABLED
```

Check response headers without assuming configuration fields are wired:

```bash
curl --silent --dump-header - --output /dev/null https://example.com/
curl --silent --dump-header - --output /dev/null \
  https://api.example.com/api/v1/health
```

## Known implementation caveats

- Several `SecurityConfig` fields are parsed and validated but are not passed
  into the current router middleware. Review `app/router/routes.go` and nginx
  files for effective CORS, headers, and rate limits.
- The repository has no production Kubernetes deployment; Kubernetes-specific
  controls are out of scope until manifests and an operating model exist.
- Configuration parsing silently falls back to defaults for malformed numeric,
  Boolean, duration, and optional float values. Verify effective container
  values after deployment.
- Swagger endpoints are disabled when `APP_ENV=production`, but the checked-in
  generated files may still be present inside build contexts or source
  checkouts. Do not expose source directories through the web server.
- Compliance status cannot be inferred from code. Privacy, retention, consent,
  access review, incident response, and regulatory obligations need separate
  evidence and ownership.

Re-review this checklist after changes to `docker-compose.beta.yml`,
`docker/nginx/`, `app/router/routes.go`, `config/production.go`, authentication,
uploads, or the deployment/restore scripts.
