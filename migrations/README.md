# Database migrations

This directory contains the ordered PostgreSQL schema history for Yamata no
Orochi. The current schema head is:

```text
0138_extend_tag_performance_to_execution.sql
```

There are 140 numbered up files and 139 numbered down files. Both aggregate
manifests currently include every matching file exactly once. The difference is
`0050_remove_short_links_indexes.sql`, which has no checked-in down migration.

## Naming and ordering

Most changes use a matching pair:

```text
NNNN_description.sql
NNNN_description_down.sql
```

The history has two duplicate ordinals, so the complete filename—not only the
number—is the migration identity:

- `0024_create_sheba_number_on_customers`
- `0024_create_system_company_and_wallet`
- `0104_create_sent_rubika_messages`
- `0104_create_splus_status_results`

New changes should use the next unused ordinal (`0140` after the current head),
include a down file whenever rollback is safe, and update both aggregate
manifests. Do not edit a migration that may already have been deployed; add a
corrective migration so every environment retains the same append-only history.

## Aggregate manifests

[`run_all_up.sql`](run_all_up.sql) includes all 141 up files in filename order.
[`run_all_down.sql`](run_all_down.sql) includes all 140 available down files
once. Its order is the reverse filename order except for the existing
`0034`/`0035` swap; treat the checked-in manifest order as canonical and review
dependencies before changing it.

Both manifests set psql’s `ON_ERROR_STOP` internally. Supplying
`-v ON_ERROR_STOP=1` on the command line is still recommended for individual
files and makes the intended fail-fast behavior explicit.

Run manifest commands from the repository root because `\i` paths are written
as `migrations/NNNN_description.sql`.

Do not wrap the complete up or down manifest in one outer transaction.
Migration `0126` uses `CREATE/DROP INDEX CONCURRENTLY`, which PostgreSQL forbids
inside a transaction block. Individual migrations that need atomicity declare
their own `BEGIN`/`COMMIT`.

## Choose the correct execution path

### Empty or disposable database

Use the aggregate up manifest to build a new empty schema:

```bash
psql -X \
  -h "$DB_HOST" \
  -p "$DB_PORT" \
  -U "$DB_USER" \
  -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/run_all_up.sql
```

The aggregate is not a database-aware “apply pending” engine. Many historical
migrations are intentionally non-idempotent, and there is no schema migration
table. Do not rerun it against an unknown or partially migrated production
database.

`make migrate` checks PostgreSQL connectivity and invokes the same manifest.
It is appropriate for a known empty/disposable database; the manifest’s
internal `ON_ERROR_STOP` supplies fail-fast behavior.

### Existing beta deployment

The production release path is
[`scripts/deploy-production-beta.sh`](../scripts/deploy-production-beta.sh).
It stops both application writers, invokes
[`scripts/init-beta-database.sh`](../scripts/init-beta-database.sh) to apply only
pending migrations, runs a read-only required-schema verification, restarts the
API and isolated scheduler, and runs topology/schema checks.

`init-beta-database.sh` tracks the last successfully applied filename in the
repository-root `.migration_tracker_beta`. That file is operational state, not
a database table and not independent proof of the database schema. The script
sorts complete filenames, which correctly handles the duplicate ordinals.

Do not delete or fabricate `.migration_tracker_beta`. If it is missing on a
non-empty database, the initializer treats the run as a first migration and
attempts the entire history. Stop and determine the real schema state instead.

### Restored production database

Follow [`../PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md). During that
workflow, use
[`scripts/apply-yamata-required-migrations.sh`](../scripts/apply-yamata-required-migrations.sh)
with `--repair` only at the documented point with both `yamata-app-beta` and
`yamata-campaign-scheduler-beta` stopped.

The required-migrations helper is deliberately not a general migration engine.
Its default `--verify-only` mode performs catalog checks without modifying the
database. Explicit `--repair` mode requires the Bundle schema from `0111`,
refuses to auto-apply destructive `0119`, applies an idempotent subset needed
through `0136`, validates critical schema and data invariants, and then advances
`.migration_tracker_beta` to at least `0136`. It preserves a valid tracker
already pointing to a later available migration, so the helper cannot move
general migration state backward.

## Applying one migration

Before applying an individual file, verify every predecessor and any data
preconditions in the target database. Stop all application processes that can
write affected tables.

```bash
psql -X \
  -h "$DB_HOST" \
  -p "$DB_PORT" \
  -U "$DB_USER" \
  -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/0128_smart_targeting_phase_preparation.sql
```

Never infer state solely from “table exists”: later migrations can replace
columns, constraints, indexes, and data representations while retaining the
same table.

## Recent rollout notes

### `0119`: destructive identifier conversion

`0119_convert_bundle_tag_evaluation_ids_to_bigserial.sql` truncates all
smart-tag evaluation runs, events, attempts, batches, and scores before
replacing UUID identifiers with `BIGSERIAL`. The data cannot be translated by
the migration. Back up and explicitly approve that loss before applying it.
The production required-migrations helper verifies `0119` but will not apply it.

### `0126`: concurrent audience indexes

`0126_optimize_campaign_audience_selection.sql` drops two write-amplifying GIN
indexes and builds the high-volume audience eligibility index with
`CONCURRENTLY`. It must run outside a transaction block. Monitor the concurrent
index build and available disk; a failed build can leave an invalid index that
must be inspected before retrying.

### `0127`: normalized Bundle allocations

Run `0127_normalize_bundle_audience_allocations.sql` in a maintenance window.
It backfills the normalized member ledger from legacy cumulative arrays and
derives each selection count from the completed ledger. Non-monotonic snapshots,
pre-existing normalized rows, and deleted legacy audience profiles are retained;
an audience is attributed to its earliest persisted snapshot unless an existing
ledger row already owns it.

Duplicate processed-campaign and Bale tracking checkpoints are retained for
delivery/status history. The newest row is elected once as `is_current`, and
partial unique indexes prevent new duplicates. Repository and scheduler paths
operate on the elected rows. Reapplying the migration preserves that election,
which is required because the production helper deliberately reapplies required
migrations.

The down migration reconstructs legacy cumulative arrays and drops the
normalized member ledger. It cannot retain the normalized send-order/audit
representation, so treat rollback as data-transforming and test it with a
backup.

### `0128`: ordered Smart Targeting preparation

`0128_smart_targeting_phase_preparation.sql`:

- adds `sample_size_per_tag` and persisted Test-preview intent to `campaigns`;
- backfills zero-based `selection_order` for every existing
  `campaign_selected_tags` row using row ID order;
- backfills zero-based send order for every normalized Bundle selection member;
- adds uniqueness/non-negative constraints for both orders;
- creates immutable campaign/audience/assigned-tag attribution storage.

The backfill updates both selection tables and creates non-concurrent unique
indexes inside a transaction. Stop writers and schedule a maintenance window
proportional to table size. Check for duplicate order or inconsistent legacy
rows before deployment.

The `0128` down migration drops attribution history, Test-preview state,
per-tag sample size, and both explicit ordering columns. It is structurally
reversible but data-destructive.

## Rollback

Roll back only after stopping writers and confirming that no later migration
still depends on the target change. Apply down files from the head backward:

```bash
psql -X \
  -h "$DB_HOST" \
  -p "$DB_PORT" \
  -U "$DB_USER" \
  -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/0128_smart_targeting_phase_preparation_down.sql
```

`run_all_down.sql` attempts to remove the entire application schema. It is a
destructive teardown tool for disposable databases, not a production rollback
strategy. Take and verify a backup first.

Migration `0050_remove_short_links_indexes.sql` has no checked-in rollback.
Restoring those removed indexes requires a deliberate corrective migration or
manual schema repair based on the preceding schema.

Down files can lose data even when they execute successfully. Review the SQL,
application compatibility, restore point, and rollback acceptance criteria for
every production change.

## Migration history by area

| Range | Main changes |
|---|---|
| `0001`–`0013` | Account types, customers, OTP/session/auth foundations, audit logs, UUIDs, UTC timestamps, and validation changes |
| `0014`–`0020` | Initial SMS campaigns, wallet/accounting, agency commission, payment audit actions, and tax wallet |
| `0021`–`0033` | Agency referrals/discounts, balance snapshots, system identities, transaction types/indexes, admins, line numbers, sessions, and bots |
| `0034`–`0054` | Audience profiles/tags, processed/sent SMS, tickets, short links/clicks/scenarios, crypto payments, job categories, and short-link denormalization |
| `0055`–`0076` | Status jobs/results, campaign statistics, pricing, cancellation, audience cache, multimedia, platform settings, Bale/Soroush Plus delivery, and the generic `campaigns` rename |
| `0077`–`0097` | Legacy FK/table cleanup, deposits/invoices, admin audit actions, base/page prices, ACL requests, permissions, expiry, exports, and refund/invoice audit coverage |
| `0098`–`0106` | Platform-neutral status jobs, tracking IDs, Bale/Soroush Plus/Rubika status data, Rubika sends, campaign test-send auditing, and wallet-charge previews |
| `0107`–`0116` | Bundles, campaign phases, Bundle audience selections, audience scores/statistics, normalized scoring, hidden campaigns, and Bundle audit actions |
| `0117`–`0128` | Smart-tag evaluation, platform-scoped jobs, bigint evaluation IDs, ordered campaign tag selections, explicit targeting methods, exact-capacity generations, processed Bundle linkage, source hierarchy, high-volume audience indexes, normalized append-only Bundle allocations/send order, Test-preview intent, and per-audience tag attribution |
| `0129` | Immediate PayamSMS batch response bodies, response headers, HTTP status, retry counts, and terminal errors for campaign diagnostics |
| `0130` | Durable asynchronous Smart Targeting Test sampling calculations and aggregate per-tag results |
| `0131` | PostgreSQL query observability, redundant audience-index removal, and large-table autovacuum/statistics tuning |
| `0132` | Durable, scheduler-driven Smart Targeting Test Campaign tag CTR reports and materialized Bundle/tag summaries |
| `0133` | Independent Smart Targeting Test sampling allocation fingerprints, decoupling preview jobs from exact-capacity generations |
| `0134` | Bundle-scoped audience exclusions for Smart Targeting Test eligibility |
| `0135` | Durable external short-link publication and synchronization state |
| `0136` | Versioned exact-capacity eligibility with platform colors and Test Bundle exclusions |
| `0137` | Auditable SMS-provider selection and send-attempt persistence |
| `0138` | Smart Targeting Execution tag attribution metrics and global delivered-based tag CTR summaries |
| `0139` | Canonical public short-link metadata and explicit campaign test-click exclusion from reporting |
| `0140` | Immutable Smart Targeting Test sample snapshots and releasable pre-execution audience reservations |

## Current schema areas

At head, the schema supports:

- customer, admin, and bot identities, sessions, audit logs, permissions, and
  maker-checker ACL requests;
- Bundles and multi-platform campaigns with Test/Execution phases, explicit
  targeting methods, audience selections, scores, and per-platform
  sent-message/status data;
- Bundle smart-tag runs/events/attempts/batches/scores, ordered campaign tag
  snapshots, asynchronous exact-capacity generations, independently
  fingerprinted Test-preview jobs and intent, normalized Bundle audience
  members/send order, and immutable
  audience/tag attribution;
- wallets, immutable transactions, balance snapshots, fiat payments, deposit
  receipts, invoices, crypto payments, taxes, and agency discounts;
- audience profiles, tags, source hierarchy/statistics, segment factors,
  page/base prices, platform settings, line numbers, short links/clicks,
  multimedia, and tickets.

## Adding a migration

1. Choose the next unused four-digit ordinal.
2. Add the up file and a safe down file when possible.
3. State destructive/data-transforming behavior and operational preconditions
   in SQL comments and this README.
4. Use schema-qualified names where ambiguity is possible.
5. Make a deliberate choice between transactional DDL and concurrent index
   operations; do not mix `CONCURRENTLY` into a transaction.
6. Add the up include to the end of `run_all_up.sql`.
7. Add the down include to the beginning of `run_all_down.sql`, adjusted for
   dependencies when a strict lexical reversal is unsafe.
8. Verify each manifest contains every corresponding file exactly once.
9. Test the up migration and rollback on a disposable copy with
   `ON_ERROR_STOP=1`, including realistic data volume and failure recovery.
10. Run affected application tests and update schema/API/operations docs.
