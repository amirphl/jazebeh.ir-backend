# Production operation scripts

The complete ordered procedure is in
[`PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md).

| Script | Purpose |
|---|---|
| `deploy-beta.sh` | Deploy the Compose API stack; requires API campaign execution to remain disabled |
| `deploy-production-beta.sh` | Canonical release command; deploys API then recreates isolated campaign workers |
| `deploy-campaign-scheduler-beta.sh` | Recreates the private, restartable campaign-worker container from the running API environment |
| `apply-yamata-required-migrations.sh` | Verifies required schema by default; `--repair` explicitly reapplies the restore/repair subset through 0149 |
| `restore-yamata-audience-profiles.sh` | Atomically restores an `audience_profiles`-only PostgreSQL 17 plain dump |
| `restore-yamata-scheduler-runtime-data.sh` | Atomically restores the exact scheduler table set, normalized selection members, audience/tag attribution, audience-spec sources, and sequence counters |
| `run-yamata-data-restore.sh` | Starts either large restore in the background with systemd/journal progress |
| `tune-yamata-restore.sh` | Enables/resets temporary PostgreSQL checkpoint tuning |
| `check-yamata-certificates.sh` | Validates existing certificate/key files; never issues or renews |
| `check-yamata-production.sh` | Read-only topology, environment, network, schema, secret-hash, and certificate checks |
| `install-yamata-operations.sh` | Installs restore/check helpers in `/usr/local/sbin` |
| `backup-yamata-audience-profiles.bat` | Creates the Windows PostgreSQL 17 audience-only plain dump |
| `backup-yamata-scheduler-runtime-data.bat` | Creates the Windows PostgreSQL 17 scheduler-table plain dump |
| `rebuild_audience_spec_cache.py` | Rebuilds the short-lived v4 audience-spec Redis cache from validated exports |
| `verify_audience_spec_tag_activity.py` | Verifies cached tag IDs and capacities against JSON and PostgreSQL |
| `extract_pg_dump_copy.py` | Internal allowlist filter used by both selective restore scripts |

Data and repair utilities:

| Script | Purpose |
|---|---|
| `check_campaign_audience_uid_counts.py` | Compares campaign JSONL UID counts with PostgreSQL |
| `push_campaign_stats.py` | Recalculates and pushes persisted campaign delivery statistics |
| `recover_sms_campaign_statistics.py` | Read-only-DB recovery for PayamSMS statuses and statistics for the fixed campaigns 935, 944, 945, 943, 937, 933, and 942; preview by default |
| `push_campaign_fake_stats.py` | Pushes explicit all-failed statistics with a required message-part count |
| `push_campaign_audience_uids.py` | Resolves and pushes aligned UID/short-code pairs |
| `export_uid_campaign_participation.py` | Creates private, spreadsheet-safe TorobPay participation CSVs |
| `resend_torobpay_sms.py` | Dry-run-by-default, idempotent TorobPay SMS correction/resend tool |
| `count_characters.py` | Mirrors campaign SMS character and part calculations |
| `cert_monitor.py` | Checks configured certificates and alerts without logging credentials |
| `nginx_sentry_forwarder.py` | Redacts and forwards bounded Nginx error events to Sentry |

Install the standalone Python dependencies in an isolated environment:

```bash
python3 -m venv .venv
.venv/bin/python -m pip install -r scripts/requirements.txt
```

The audience-cache tools can instead use the smaller
`scripts/requirements-audience-spec.txt` dependency set.

Deployment scripts parse `.env.beta` as strict dotenv data through
`load_dotenv.py`; they never execute it as shell code. Use one `KEY=VALUE` per
line, quote values containing spaces, and do not use shell expansion, command
substitution, or unresolved placeholders copied from `env.template`.

Key rules:

- Keep `CAMPAIGN_EXECUTION_ENABLED=false` in `.env.beta` and the API container.
- Keep exact-capacity and smart-tag scheduling enabled in the API container;
  the isolated campaign scheduler overrides both to `false`.
- Keep external short-link sync enabled only in the API container; the isolated
  campaign scheduler always overrides `EXTERNAL_SHORTLINK_ENABLED=false`.
- Run `deploy-production-beta.sh` after every image or environment change.
- Stop `yamata-app-beta` and `yamata-campaign-scheduler-beta` during selective imports.
- Restore audience profiles before scheduler runtime data.
- Never run two imports concurrently.
- Do not use `init-beta-database.sh` for the initial restored production database.
- Use `apply-yamata-required-migrations.sh --repair` only for the documented
  restore/repair workflow with both application writers stopped. Routine
  deployments use its read-only `--verify-only` mode.
- Supply passwords and API credentials through the documented environment variables
  or interactive hidden prompts. Secret-bearing command-line options are intentionally
  unsupported because process arguments may be visible to other users.
- PostgreSQL plain dumps are treated as untrusted input: restores emit only allowlisted
  `COPY public.<table> (...) FROM stdin` sections and execute no dump-provided SQL.
