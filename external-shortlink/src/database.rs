use std::{cmp::Ordering, future::Future, time::Duration};

use chrono::Utc;
use sqlx::{PgPool, Postgres, postgres::PgPoolOptions};
use thiserror::Error;
use tracing::warn;

use crate::{
    config::Settings,
    domain::{ClickEvent, ClickRecord, LinkInput, LinkRecord},
};

const LINK_COLUMNS: &str = "
    link_id, code, long_url, short_url, source_link_id, campaign_id, client_id,
    scenario_id, scenario_name, phone_number, is_test, source_created_at, source_updated_at
";

const CLICK_COLUMNS: &str = "
    click_id, event_id, short_code, link_id, long_url, short_url, source_link_id,
    campaign_id, client_id, scenario_id, scenario_name, phone_number, is_test,
    link_created_at, link_updated_at, clicked_at, client_ip, user_agent, referer
";

// Retention must never turn into a long-running, table-wide DELETE.  A batch
// is small enough to keep row locks and WAL pressure bounded, while the loop
// below still lets a scheduled run drain a meaningful backlog.
const PURGE_BATCH_SIZE: i64 = 5_000;
// Legacy rows are no longer written after cutover. Larger bounded draining is
// therefore safe and prevents the temporary dual-read table from consuming the
// seven-day capacity budget for weeks.
const MAX_PURGE_BATCHES_PER_RUN: usize = 200;

#[derive(Clone)]
pub struct Database {
    pool: PgPool,
    command_timeout: Duration,
}

#[derive(Debug, Error)]
pub enum DatabaseError {
    #[error("one or more codes already map to another destination")]
    MappingConflict(Vec<String>),
    #[error("through_click_id is not a persisted click_id")]
    UnknownAcknowledgement,
    #[error(transparent)]
    Sql(#[from] sqlx::Error),
}

impl Database {
    pub fn new(settings: &Settings) -> Result<Self, sqlx::Error> {
        let statement_timeout = postgres_timeout(settings.db_command_timeout);
        let lock_timeout = postgres_timeout(settings.db_lock_timeout);
        let pool = PgPoolOptions::new()
            .min_connections(settings.pool_min_size)
            .max_connections(settings.pool_max_size)
            .acquire_timeout(settings.db_command_timeout)
            .max_lifetime(Some(Duration::from_secs(30 * 60)))
            .idle_timeout(Some(Duration::from_secs(5 * 60)))
            .after_connect(move |connection, _metadata| {
                let statement_timeout = statement_timeout.clone();
                let lock_timeout = lock_timeout.clone();
                Box::pin(async move {
                    sqlx::query("SELECT set_config('statement_timeout', $1, false)")
                        .bind(statement_timeout)
                        .execute(&mut *connection)
                        .await?;
                    sqlx::query("SELECT set_config('lock_timeout', $1, false)")
                        .bind(lock_timeout)
                        .execute(&mut *connection)
                        .await?;
                    Ok(())
                })
            })
            .connect_lazy(&settings.database_url)?;
        Ok(Self {
            pool,
            command_timeout: settings.db_command_timeout,
        })
    }

    pub async fn ping(&self) -> Result<(), DatabaseError> {
        self.with_timeout(sqlx::query_scalar::<_, bool>("SELECT TRUE").fetch_one(&self.pool))
            .await
            .map(|_| ())
    }

    pub async fn lookup_link(&self, code: &str) -> Result<Option<LinkRecord>, DatabaseError> {
        self.with_timeout(
            sqlx::query_as::<_, LinkRecord>(&format!(
                "SELECT {LINK_COLUMNS} FROM links WHERE code = $1"
            ))
            .bind(code)
            .fetch_optional(&self.pool),
        )
        .await
    }

    pub async fn preload_links(&self, limit: i64) -> Result<Vec<LinkRecord>, DatabaseError> {
        if limit == 0 {
            return Ok(Vec::new());
        }
        self.with_timeout(
            sqlx::query_as::<_, LinkRecord>(&format!(
                "SELECT {LINK_COLUMNS} FROM links ORDER BY link_id DESC LIMIT $1"
            ))
            .bind(limit)
            .fetch_all(&self.pool),
        )
        .await
    }

    pub async fn insert_click(&self, event: &ClickEvent) -> Result<(), DatabaseError> {
        let mut transaction = self.pool.begin().await?;
        // A PostgreSQL sequence value is assigned before COMMIT. Without this
        // transaction lock, click 101 can commit and be acknowledged before an
        // earlier transaction holding click 100 commits; the ID cursor would
        // then skip 100 forever. Keep allocation, persistence, and commit
        // ordered. This is deliberately scoped to click writes only; mapping
        // uploads and click reads remain concurrent.
        sqlx::query("SELECT pg_advisory_xact_lock(734_913_001)")
            .execute(&mut *transaction)
            .await?;
        let click_id = sqlx::query_scalar::<_, i64>("SELECT nextval('clicks_click_id_seq')")
            .fetch_one(&mut *transaction)
            .await?;
        let inserted = sqlx::query_scalar::<_, uuid::Uuid>(
            "INSERT INTO click_event_dedup (event_id, click_id, clicked_at) VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING RETURNING event_id",
        )
        .bind(event.event_id)
        .bind(click_id)
        .bind(event.clicked_at)
        .fetch_optional(&mut *transaction)
        .await?;
        if inserted.is_some() {
            insert_click_query(event, click_id)
                .execute(&mut *transaction)
                .await?;
        }
        transaction.commit().await?;
        Ok(())
    }

    pub async fn upload_links(&self, links: &[LinkInput]) -> Result<UploadResult, DatabaseError> {
        self.with_database_timeout(self.upload_links_inner(links))
            .await
    }

    async fn upload_links_inner(&self, links: &[LinkInput]) -> Result<UploadResult, DatabaseError> {
        let codes = links
            .iter()
            .map(|link| link.code.clone())
            .collect::<Vec<_>>();
        let long_urls = links
            .iter()
            .map(|link| link.long_url.clone())
            .collect::<Vec<_>>();
        let short_urls = links
            .iter()
            .map(|link| link.short_url.clone())
            .collect::<Vec<_>>();
        let source_link_ids = links
            .iter()
            .map(|link| link.source_link_id)
            .collect::<Vec<_>>();
        let campaign_ids = links
            .iter()
            .map(|link| link.campaign_id)
            .collect::<Vec<_>>();
        let client_ids = links.iter().map(|link| link.client_id).collect::<Vec<_>>();
        let scenario_ids = links
            .iter()
            .map(|link| link.scenario_id)
            .collect::<Vec<_>>();
        let scenario_names = links
            .iter()
            .map(|link| link.scenario_name.clone())
            .collect::<Vec<_>>();
        let phone_numbers = links
            .iter()
            .map(|link| link.phone_number.clone())
            .collect::<Vec<_>>();
        let is_tests = links.iter().map(|link| link.is_test).collect::<Vec<_>>();
        let source_created_ats = links
            .iter()
            .map(|link| link.source_created_at)
            .collect::<Vec<_>>();
        let source_updated_ats = links
            .iter()
            .map(|link| link.source_updated_at)
            .collect::<Vec<_>>();

        let mut transaction = self.pool.begin().await?;
        let conflicts = sqlx::query_scalar::<_, String>(
            "
            SELECT existing.code
            FROM links AS existing
            JOIN UNNEST($1::varchar[], $2::varchar[]) AS incoming(code, long_url)
                ON existing.code = incoming.code
            WHERE existing.long_url <> incoming.long_url
            ORDER BY existing.code
            LIMIT 100
            ",
        )
        .bind(&codes)
        .bind(&long_urls)
        .fetch_all(&mut *transaction)
        .await?;
        if !conflicts.is_empty() {
            return Err(DatabaseError::MappingConflict(conflicts));
        }

        let inserted = sqlx::query_scalar::<_, String>(&mapping_sql(
            "
            INSERT INTO links (
                code, long_url, short_url, source_link_id, campaign_id, client_id,
                scenario_id, scenario_name, phone_number, is_test, source_created_at, source_updated_at
            )
            SELECT * FROM incoming
            ON CONFLICT (code) DO NOTHING
            RETURNING code
            ",
        ))
        .bind(&codes)
        .bind(&long_urls)
        .bind(&short_urls)
        .bind(&source_link_ids)
        .bind(&campaign_ids)
        .bind(&client_ids)
        .bind(&scenario_ids)
        .bind(&scenario_names)
        .bind(&phone_numbers)
        .bind(&is_tests)
        .bind(&source_created_ats)
        .bind(&source_updated_ats)
        .fetch_all(&mut *transaction)
        .await?;
        // Re-check after INSERT ... ON CONFLICT. PostgreSQL's unique index makes
        // concurrent writers of the same code wait safely; this fresh statement
        // then detects a competing immutable destination without serializing
        // unrelated mapping batches behind a global advisory lock.
        let conflicts = sqlx::query_scalar::<_, String>(
            "
            SELECT existing.code
            FROM links AS existing
            JOIN UNNEST($1::varchar[], $2::varchar[]) AS incoming(code, long_url)
                ON existing.code = incoming.code
            WHERE existing.long_url <> incoming.long_url
            ORDER BY existing.code
            LIMIT 100
            ",
        )
        .bind(&codes)
        .bind(&long_urls)
        .fetch_all(&mut *transaction)
        .await?;
        if !conflicts.is_empty() {
            return Err(DatabaseError::MappingConflict(conflicts));
        }

        sqlx::query(&mapping_sql(
            "
            UPDATE links AS existing
            SET short_url = incoming.short_url,
                source_link_id = incoming.source_link_id,
                campaign_id = incoming.campaign_id,
                client_id = incoming.client_id,
                scenario_id = incoming.scenario_id,
                scenario_name = incoming.scenario_name,
                phone_number = incoming.phone_number,
                is_test = incoming.is_test,
                source_created_at = incoming.source_created_at,
                source_updated_at = incoming.source_updated_at
            FROM incoming
            WHERE existing.code = incoming.code
              AND existing.long_url = incoming.long_url
              -- The synchronizer routinely re-sends existing mappings.  Do
              -- not create a new tuple (and WAL/index churn) unless mutable
              -- metadata actually changed. IS DISTINCT FROM is null-safe.
              AND (
                    existing.short_url IS DISTINCT FROM incoming.short_url
                 OR existing.source_link_id IS DISTINCT FROM incoming.source_link_id
                 OR existing.campaign_id IS DISTINCT FROM incoming.campaign_id
                 OR existing.client_id IS DISTINCT FROM incoming.client_id
                 OR existing.scenario_id IS DISTINCT FROM incoming.scenario_id
                 OR existing.scenario_name IS DISTINCT FROM incoming.scenario_name
                 OR existing.phone_number IS DISTINCT FROM incoming.phone_number
                 OR existing.is_test IS DISTINCT FROM incoming.is_test
                 OR existing.source_created_at IS DISTINCT FROM incoming.source_created_at
                 OR existing.source_updated_at IS DISTINCT FROM incoming.source_updated_at
              )
            ",
        ))
        .bind(&codes)
        .bind(&long_urls)
        .bind(&short_urls)
        .bind(&source_link_ids)
        .bind(&campaign_ids)
        .bind(&client_ids)
        .bind(&scenario_ids)
        .bind(&scenario_names)
        .bind(&phone_numbers)
        .bind(&is_tests)
        .bind(&source_created_ats)
        .bind(&source_updated_ats)
        .execute(&mut *transaction)
        .await?;

        let persisted = sqlx::query_as::<_, LinkRecord>(&format!(
            "SELECT {LINK_COLUMNS} FROM links WHERE code = ANY($1::varchar[]) ORDER BY link_id"
        ))
        .bind(&codes)
        .fetch_all(&mut *transaction)
        .await?;
        transaction.commit().await?;
        Ok(UploadResult {
            created: inserted.len(),
            existing: links.len() - inserted.len(),
            persisted,
        })
    }

    pub async fn fetch_clicks(
        &self,
        after_id: i64,
        limit: i64,
    ) -> Result<(Vec<ClickRecord>, bool), DatabaseError> {
        // Read each table with its own bounded, index-ordered query and merge
        // the two sorted streams here. A `UNION ALL` subquery cannot be served
        // by a MergeAppend, so it forced PostgreSQL to sort every row above the
        // cursor; per-table queries keep the scan index-ordered. It also lets
        // the page promise what the importer's cursor depends on: strictly
        // increasing click IDs, even for an installation that holds the same
        // click_id in both tables after the online cutover.
        let batch = limit + 1;
        let legacy = self
            .with_timeout(
                sqlx::query_as::<_, ClickRecord>(&format!(
                    "SELECT {CLICK_COLUMNS} FROM clicks WHERE click_id > $1 ORDER BY click_id ASC LIMIT $2"
                ))
                .bind(after_id)
                .bind(batch)
                .fetch_all(&self.pool),
            )
            .await?;
        let partitioned = self
            .with_timeout(
                sqlx::query_as::<_, ClickRecord>(&format!(
                    "SELECT {CLICK_COLUMNS} FROM clicks_v2 WHERE click_id > $1 ORDER BY click_id ASC LIMIT $2"
                ))
                .bind(after_id)
                .bind(batch)
                .fetch_all(&self.pool),
            )
            .await?;
        let (page, has_more, shadowed) = merge_click_pages(legacy, partitioned, limit);
        if shadowed > 0 {
            warn!(
                shadowed,
                after_id,
                "click IDs exist in both the legacy and partitioned tables; the partitioned row was served and the shadowed duplicate was not"
            );
        }
        Ok((page, has_more))
    }

    pub async fn acknowledge(&self, through_click_id: i64) -> Result<i64, DatabaseError> {
        self.with_database_timeout(self.acknowledge_inner(through_click_id))
            .await
    }

    async fn acknowledge_inner(&self, through_click_id: i64) -> Result<i64, DatabaseError> {
        let mut transaction = self.pool.begin().await?;
        let current = sqlx::query_scalar::<_, i64>(
            "SELECT through_click_id FROM click_acknowledgements WHERE singleton = TRUE FOR UPDATE",
        )
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(sqlx::Error::RowNotFound)?;
        if through_click_id <= current {
            transaction.commit().await?;
            return Ok(current);
        }
        let exists = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS (SELECT 1 FROM clicks WHERE click_id = $1 UNION ALL SELECT 1 FROM clicks_v2 WHERE click_id = $1)",
        )
        .bind(through_click_id)
        .fetch_one(&mut *transaction)
        .await?;
        if !exists {
            return Err(DatabaseError::UnknownAcknowledgement);
        }
        // The import cursor is the acknowledgement record. Updating every
        // click in a page turns a cheap cursor advance into thousands of
        // indexed writes and can starve concurrent redirect inserts.
        let acknowledged = sqlx::query_scalar::<_, i64>(
            "
            UPDATE click_acknowledgements
            SET through_click_id = $1, acknowledged_at = CURRENT_TIMESTAMP
            WHERE singleton = TRUE
            RETURNING through_click_id
            ",
        )
        .bind(through_click_id)
        .fetch_one(&mut *transaction)
        .await?;
        transaction.commit().await?;
        Ok(acknowledged)
    }

    pub async fn purge_acknowledged(&self, retention_days: i64) -> Result<u64, DatabaseError> {
        let cutoff = Utc::now() - chrono::Duration::days(retention_days);
        let mut purged = 0;

        for _ in 0..MAX_PURGE_BATCHES_PER_RUN {
            // SKIP LOCKED makes retention yield to a concurrent importer or
            // diagnostic query rather than consuming the entire command
            // timeout waiting for it. Each DELETE is its own transaction.
            let result = self
                .with_timeout(
                    sqlx::query(
                        "
                        WITH deletable AS (
                            SELECT click_id
                            FROM clicks
                            WHERE click_id <= (
                                SELECT through_click_id
                                FROM click_acknowledgements
                                WHERE singleton = TRUE
                            )
                              AND clicked_at < $1
                            -- Retention is timestamp-based. Ordering by the
                            -- primary key forced PostgreSQL to visit every
                            -- acknowledged recent click before finding an
                            -- expired one. The composite retention index is
                            -- ordered by these columns.
                            ORDER BY clicked_at, click_id
                            LIMIT $2
                            FOR UPDATE SKIP LOCKED
                        )
                        DELETE FROM clicks AS click
                        USING deletable
                        WHERE click.click_id = deletable.click_id
                        ",
                    )
                    .bind(cutoff)
                    .bind(PURGE_BATCH_SIZE)
                    .execute(&self.pool),
                )
                .await?;
            let deleted = result.rows_affected();
            purged += deleted;
            if deleted < PURGE_BATCH_SIZE as u64 {
                break;
            }
        }

        Ok(purged)
    }

    pub async fn database_size(&self) -> Result<i64, DatabaseError> {
        self.with_timeout(
            sqlx::query_scalar("SELECT pg_database_size(current_database())").fetch_one(&self.pool),
        )
        .await
    }

    /// Reports whether any click has been routed to the default partition.
    ///
    /// Clicks only land there when the maintenance window failed to create the
    /// partition for their day, so this is the alarm for "the window fell
    /// behind". `None` means this installation predates the default partition,
    /// where a missing day partition rejects clicks outright.
    pub async fn default_partition_backlog(&self) -> Result<Option<bool>, DatabaseError> {
        let exists = self
            .with_timeout(
                sqlx::query_scalar::<_, bool>(
                    "SELECT to_regclass('public.clicks_v2_default') IS NOT NULL",
                )
                .fetch_one(&self.pool),
            )
            .await?;
        if !exists {
            return Ok(None);
        }
        let backlog = self
            .with_timeout(
                sqlx::query_scalar::<_, bool>(
                    "SELECT EXISTS (SELECT 1 FROM clicks_v2_default)",
                )
                .fetch_one(&self.pool),
            )
            .await?;
        Ok(Some(backlog))
    }

    async fn with_timeout<T>(
        &self,
        future: impl Future<Output = Result<T, sqlx::Error>>,
    ) -> Result<T, DatabaseError> {
        tokio::time::timeout(self.command_timeout, future)
            .await
            .map_err(|_| sqlx::Error::PoolTimedOut)?
            .map_err(DatabaseError::Sql)
    }

    async fn with_database_timeout<T>(
        &self,
        future: impl Future<Output = Result<T, DatabaseError>>,
    ) -> Result<T, DatabaseError> {
        tokio::time::timeout(self.command_timeout, future)
            .await
            .map_err(|_| DatabaseError::Sql(sqlx::Error::PoolTimedOut))?
    }
}

fn postgres_timeout(duration: Duration) -> String {
    format!("{}ms", duration.as_millis().max(1))
}

#[derive(Debug)]
pub struct UploadResult {
    pub created: usize,
    pub existing: usize,
    pub persisted: Vec<LinkRecord>,
}

fn insert_click_query(
    event: &ClickEvent,
    click_id: i64,
) -> sqlx::query::Query<'_, Postgres, sqlx::postgres::PgArguments> {
    sqlx::query(
        "
        INSERT INTO clicks_v2 (
            click_id, event_id, short_code, link_id, long_url, short_url, source_link_id, campaign_id, client_id,
            scenario_id, scenario_name, phone_number, is_test, link_created_at, link_updated_at, clicked_at,
            client_ip, user_agent, referer
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
        )
        ",
    )
    .bind(click_id)
    .bind(event.event_id)
    .bind(&event.short_code)
    .bind(event.link_id)
    .bind(&event.long_url)
    .bind(&event.short_url)
    .bind(event.source_link_id)
    .bind(event.campaign_id)
    .bind(event.client_id)
    .bind(event.scenario_id)
    .bind(&event.scenario_name)
    .bind(&event.phone_number)
    .bind(event.is_test)
    .bind(event.link_created_at)
    .bind(event.link_updated_at)
    .bind(event.clicked_at)
    .bind(&event.client_ip)
    .bind(&event.user_agent)
    .bind(&event.referer)
}

fn mapping_sql(operation: &str) -> String {
    let prefix = "
        WITH incoming AS (
            SELECT * FROM UNNEST(
                $1::varchar[], $2::varchar[], $3::varchar[], $4::bigint[], $5::bigint[], $6::bigint[],
                $7::bigint[], $8::varchar[], $9::varchar[], $10::boolean[], $11::timestamptz[], $12::timestamptz[]
            ) AS incoming_values(
                code, long_url, short_url, source_link_id, campaign_id, client_id,
                scenario_id, scenario_name, phone_number, is_test, source_created_at, source_updated_at
            )
        ) ";
    format!("{prefix}{operation}")
}

/// Merges the partial pages read from the legacy and partitioned click tables
/// into one strictly increasing page.
///
/// The two tables normally hold disjoint ID ranges, but an installation
/// upgraded across the online click-store cutover can hold the same `click_id`
/// in both. A click ID identifies exactly one click, so such a pair cannot both
/// be imported: the partitioned table is the current store and wins, and the
/// shadowed legacy row is reported to the caller, where it is logged, instead
/// of silently shrinking the page or breaking the importer's cursor contract.
fn merge_click_pages(
    legacy: Vec<ClickRecord>,
    partitioned: Vec<ClickRecord>,
    limit: i64,
) -> (Vec<ClickRecord>, bool, u64) {
    let keep = usize::try_from(limit).unwrap_or(0);
    let mut merged: Vec<ClickRecord> = Vec::with_capacity(keep.saturating_add(1));
    let mut legacy = legacy.into_iter().peekable();
    let mut partitioned = partitioned.into_iter().peekable();
    let mut shadowed = 0_u64;
    // One extra row is enough to answer `has_more` without reading the rest.
    while merged.len() <= keep {
        let next = match (legacy.peek(), partitioned.peek()) {
            (None, None) => break,
            (Some(_), None) => legacy.next(),
            (None, Some(_)) => partitioned.next(),
            (Some(legacy_row), Some(partitioned_row)) => {
                match legacy_row.click_id.cmp(&partitioned_row.click_id) {
                    Ordering::Equal => {
                        shadowed += 1;
                        legacy.next();
                        partitioned.next()
                    }
                    Ordering::Less => legacy.next(),
                    Ordering::Greater => partitioned.next(),
                }
            }
        };
        let Some(record) = next else { break };
        merged.push(record);
    }
    let has_more = merged.len() > keep;
    merged.truncate(keep);
    (merged, has_more, shadowed)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::domain::ClickEvent;
    use chrono::TimeZone;
    use uuid::Uuid;

    fn record(click_id: i64, code: &str) -> ClickRecord {
        ClickRecord {
            click_id,
            event: ClickEvent {
                event_id: Uuid::new_v4(),
                short_code: code.to_owned(),
                link_id: 1,
                long_url: "https://example.com/destination".to_owned(),
                short_url: None,
                source_link_id: None,
                campaign_id: None,
                client_id: None,
                scenario_id: None,
                scenario_name: None,
                phone_number: None,
                is_test: false,
                link_created_at: None,
                link_updated_at: None,
                clicked_at: Utc.timestamp_opt(0, 0).unwrap(),
                client_ip: None,
                user_agent: None,
                referer: None,
            },
        }
    }

    fn ids(page: &[ClickRecord]) -> Vec<i64> {
        page.iter().map(|row| row.click_id).collect()
    }

    #[test]
    fn merged_page_is_ordered_across_both_tables() {
        let (page, has_more, shadowed) = merge_click_pages(
            vec![record(1, "legacy-1"), record(3, "legacy-3")],
            vec![record(2, "v2-2"), record(4, "v2-4")],
            3,
        );
        assert_eq!(ids(&page), vec![1, 2, 3]);
        assert!(has_more);
        assert_eq!(shadowed, 0);
    }

    #[test]
    fn merged_page_truncates_to_the_requested_limit() {
        let (page, has_more, _) = merge_click_pages(
            vec![record(1, "legacy-1")],
            vec![record(2, "v2-2"), record(3, "v2-3")],
            10,
        );
        assert_eq!(ids(&page), vec![1, 2, 3]);
        assert!(!has_more, "an exhausted pair of tables has no more rows");
    }

    #[test]
    fn merged_page_prefers_the_current_store_and_counts_the_shadowed_row() {
        let (page, has_more, shadowed) = merge_click_pages(
            vec![record(7, "legacy-7")],
            vec![record(7, "v2-7")],
            10,
        );
        assert_eq!(ids(&page), vec![7]);
        assert_eq!(page[0].event.short_code, "v2-7");
        assert_eq!(shadowed, 1);
        assert!(!has_more);
    }

    #[test]
    fn merged_page_handles_one_empty_table() {
        let (page, has_more, shadowed) =
            merge_click_pages(Vec::new(), vec![record(9, "v2-9")], 10);
        assert_eq!(ids(&page), vec![9]);
        assert!(!has_more);
        assert_eq!(shadowed, 0);
    }
}
