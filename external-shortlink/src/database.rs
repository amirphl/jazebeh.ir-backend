use std::{future::Future, time::Duration};

use chrono::Utc;
use sqlx::{PgPool, Postgres, QueryBuilder, postgres::PgPoolOptions};
use thiserror::Error;

use crate::{
    config::Settings,
    domain::{ClickEvent, ClickRecord, LinkInput, LinkRecord},
};

const LINK_COLUMNS: &str = "
    link_id, code, long_url, short_url, source_link_id, campaign_id, client_id,
    scenario_id, scenario_name, phone_number, is_test, source_created_at, source_updated_at
";

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
        let lock_timeout =
            postgres_timeout(settings.db_command_timeout.min(Duration::from_secs(1)));
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
        self.with_timeout(insert_click_query(event).execute(&self.pool))
            .await?;
        Ok(())
    }

    pub async fn insert_spooled_clicks(&self, events: &[ClickEvent]) -> Result<(), DatabaseError> {
        self.with_database_timeout(self.insert_spooled_clicks_inner(events))
            .await
    }

    async fn insert_spooled_clicks_inner(
        &self,
        events: &[ClickEvent],
    ) -> Result<(), DatabaseError> {
        if events.is_empty() {
            return Ok(());
        }
        let mut transaction = self.pool.begin().await?;
        let mut builder = QueryBuilder::<Postgres>::new(
            "INSERT INTO clicks (\
                event_id, short_code, link_id, long_url, short_url, source_link_id, campaign_id, client_id, \
                scenario_id, scenario_name, phone_number, is_test, link_created_at, link_updated_at, clicked_at, \
                client_ip, user_agent, referer\
            ) ",
        );
        builder.push_values(events, |mut row, event| {
            row.push_bind(event.event_id)
                .push_bind(&event.short_code)
                .push_bind(event.link_id)
                .push_bind(&event.long_url)
                .push_bind(&event.short_url)
                .push_bind(event.source_link_id)
                .push_bind(event.campaign_id)
                .push_bind(event.client_id)
                .push_bind(event.scenario_id)
                .push_bind(&event.scenario_name)
                .push_bind(&event.phone_number)
                .push_bind(event.is_test)
                .push_bind(event.link_created_at)
                .push_bind(event.link_updated_at)
                .push_bind(event.clicked_at)
                .push_bind(&event.client_ip)
                .push_bind(&event.user_agent)
                .push_bind(&event.referer);
        });
        builder.push(" ON CONFLICT (event_id) DO NOTHING");
        builder.build().execute(&mut *transaction).await?;
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
        let rows = self
            .with_timeout(
                sqlx::query_as::<_, ClickRecord>(
                    "
                SELECT
                    click_id, event_id, short_code, link_id, long_url, short_url, source_link_id,
                    campaign_id, client_id, scenario_id, scenario_name, phone_number, is_test,
                    link_created_at, link_updated_at, clicked_at, client_ip, user_agent, referer
                FROM clicks
                WHERE click_id > $1
                ORDER BY click_id ASC
                LIMIT $2
                ",
                )
                .bind(after_id)
                .bind(limit + 1)
                .fetch_all(&self.pool),
            )
            .await?;
        let has_more = rows.len() > limit as usize;
        Ok((rows.into_iter().take(limit as usize).collect(), has_more))
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
            "SELECT EXISTS (SELECT 1 FROM clicks WHERE click_id = $1)",
        )
        .bind(through_click_id)
        .fetch_one(&mut *transaction)
        .await?;
        if !exists {
            return Err(DatabaseError::UnknownAcknowledgement);
        }
        sqlx::query(
            "
            UPDATE clicks
            SET acknowledged_at = COALESCE(acknowledged_at, CURRENT_TIMESTAMP)
            WHERE click_id > $1 AND click_id <= $2
            ",
        )
        .bind(current)
        .bind(through_click_id)
        .execute(&mut *transaction)
        .await?;
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
        let result = self
            .with_timeout(
                sqlx::query("DELETE FROM clicks WHERE acknowledged_at < $1")
                    .bind(cutoff)
                    .execute(&self.pool),
            )
            .await?;
        Ok(result.rows_affected())
    }

    pub async fn database_size(&self) -> Result<i64, DatabaseError> {
        self.with_timeout(
            sqlx::query_scalar("SELECT pg_database_size(current_database())").fetch_one(&self.pool),
        )
        .await
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
) -> sqlx::query::Query<'_, Postgres, sqlx::postgres::PgArguments> {
    sqlx::query(
        "
        INSERT INTO clicks (
            event_id, short_code, link_id, long_url, short_url, source_link_id, campaign_id, client_id,
            scenario_id, scenario_name, phone_number, is_test, link_created_at, link_updated_at, clicked_at,
            client_ip, user_agent, referer
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
        ) ON CONFLICT (event_id) DO NOTHING
        ",
    )
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
