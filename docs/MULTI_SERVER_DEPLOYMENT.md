# Multi-server deployment

The repository supports one self-contained beta Compose stack per host. It does
not currently include an environment generator, Kubernetes manifests,
multi-region orchestration, or the `deployments/environments/*` scripts that
older versions of this guide described.

Use this document to keep several independent hosts—such as staging and
production—consistent. Use
[`../PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md) when moving one live
production stack to a replacement host.

## Isolation model

Each host should have its own:

- repository checkout and protected `.env.beta`;
- PostgreSQL, Redis, GlitchTip, monitoring, uploads, logs, and backup volumes;
- domain family and pre-provisioned TLS certificates;
- JWT secret, database/Redis passwords, provider keys, bot credentials, and
  Grafana/GlitchTip credentials;
- backup retention and restore test;
- isolated campaign scheduler.

Do not share PostgreSQL or Redis between staging and production. Do not reuse
JWT secrets or provider credentials unless that sharing is an explicit,
reviewed operational decision.

## One stack per host

The checked-in Compose file uses fixed container names, fixed volume names, and
the fixed subnet `172.30.0.0/24`. Running two unmodified copies on the same
Docker host will collide even if different Compose project names are used.

The supported simple layout is therefore:

```text
production host  -> /srv/yamata-production -> production domain and data
staging host     -> /srv/yamata-staging    -> staging domain and data
```

Co-locating multiple stacks requires a maintained Compose override that changes
all conflicting container names, network/subnet, volume names, published
ports, generated nginx paths, and operational-script assumptions. That variant
is not supplied or validated by this repository.

## Per-host preparation

On each host:

```bash
git clone REPOSITORY_URL /srv/yamata
cd /srv/yamata
cp env.template .env.beta
chmod 600 .env.beta
```

Replace every template placeholder and set environment-specific values. Follow
[`PRODUCTION_CONFIGURATION.md`](PRODUCTION_CONFIGURATION.md), particularly the
startup requirements and scheduler ownership table.

Provision DNS and certificates for:

```text
example.com
www.example.com
api.example.com
monitoring.example.com
sentry.example.com
landing.example.com
```

The nginx template determines the exact names used by a deployment. The deploy
script validates existing certificates; it does not obtain or renew them.

## Deploy each host

Run the same immutable revision on every intended host, but supply that host’s
domain and `.env.beta`:

```bash
cd /srv/yamata
git checkout RELEASE_COMMIT_OR_TAG
./scripts/deploy-production-beta.sh --domain example.com
```

Deploy and validate staging first. Promote the exact tested commit or image to
production; do not rebuild from an unpinned branch after approval.

For a new empty database, the deployment script applies migrations through the
current head. For a restored or transferred database, follow the production
migration runbook and keep writers stopped until all imports and validations
finish.

## DNS and cutover

Before a production move:

1. reduce DNS TTL early enough for the old value to expire;
2. leave the source active while the destination is prepared;
3. take the final backup with source writers stopped;
4. restore, import, and validate the destination;
5. test nginx without changing public DNS;
6. update DNS only after health and relationship checks pass;
7. keep the old host stopped but recoverable through the acceptance window.

Test the destination directly:

```bash
curl --resolve 'example.com:443:DESTINATION_IP' \
  'https://example.com/api/v1/health'
curl --resolve 'api.example.com:443:DESTINATION_IP' \
  'https://api.example.com/api/v1/health'
```

## Verification on every host

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml ps
./scripts/check-yamata-production.sh /srv/yamata
docker exec yamata-app-beta printenv APP_ENV
docker exec yamata-app-beta printenv CAMPAIGN_EXECUTION_ENABLED
docker exec yamata-campaign-scheduler-beta printenv CAMPAIGN_EXECUTION_ENABLED
curl --fail https://example.com/api/v1/health
```

Also verify that:

- only ports 80/443 are published by the stack;
- PostgreSQL, Redis, metrics, and Grafana are not exposed directly;
- the API owns exact-capacity, Test-sampling, smart-tag, and refund-reconciliation jobs;
- the isolated scheduler alone owns campaign execution;
- certificate monitoring works and an authorized renewal mechanism exists;
- alerts, backups, retention, and restore drills identify the correct
  environment.

## CI/CD guidance

The repository does not ship a remote deployment workflow. If one is added,
keep these constraints:

- use protected environment-specific secrets, never generated `.env.beta`
  artifacts in source control;
- require explicit environment and release revision inputs;
- serialize migrations and deployments per environment;
- use `deploy-production-beta.sh`, not raw `docker compose up`, so the isolated
  scheduler is recreated and checked;
- block production promotion when the staging health check, migration check, or
  security review fails;
- avoid `rsync --delete` against a live checkout containing `.env.beta`,
  certificates, backups, or generated operational state.

## Scaling limitations

`docker compose up --scale app-beta=N` is not supported by the checked-in stack
because `app-beta` has a fixed container name/IP and also owns background job
schedulers. Horizontal API scaling would first require external load
balancing, removal of fixed addressing/names, and explicit singleton or
distributed coordination for every background worker. Treat that as an
architecture change, not an operational flag.
