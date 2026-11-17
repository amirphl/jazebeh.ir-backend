use std::{
    fs,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{SystemTime, UNIX_EPOCH},
};

use rusqlite::{Connection, OptionalExtension, params};
use thiserror::Error;
use tokio::{sync::Semaphore, task, time};
use tracing::{info, warn};
use uuid::Uuid;

use crate::domain::ClickEvent;

const COMPACT_AFTER_DRAIN_BYTES: u64 = 64 * 1024 * 1024;
#[derive(Clone)]
pub struct DurableSpool {
    inner: Arc<Mutex<Connection>>,
    operation_gate: Arc<Semaphore>,
    path: Arc<PathBuf>,
    max_bytes: u64,
    max_events: u64,
    operation_wait: std::time::Duration,
}

#[derive(Debug, Error)]
pub enum SpoolError {
    #[error("click spool capacity exhausted")]
    Full,
    #[error("click spool is busy")]
    Busy,
    #[error("durable click spool error: {0}")]
    Storage(String),
}

impl DurableSpool {
    pub async fn open(
        path: PathBuf,
        max_bytes: u64,
        max_events: u64,
        operation_wait: std::time::Duration,
    ) -> Result<Self, SpoolError> {
        let connection_path = path.clone();
        let connection = task::spawn_blocking(move || open_connection(&connection_path))
            .await
            .map_err(|error| SpoolError::Storage(error.to_string()))??;
        Ok(Self {
            inner: Arc::new(Mutex::new(connection)),
            operation_gate: Arc::new(Semaphore::new(1)),
            path: Arc::new(path),
            max_bytes,
            max_events,
            operation_wait,
        })
    }

    pub async fn enqueue(&self, event: &ClickEvent) -> Result<(), SpoolError> {
        let event_id = event.event_id;
        let payload =
            serde_json::to_string(event).map_err(|error| SpoolError::Storage(error.to_string()))?;
        let max_bytes = self.max_bytes;
        let max_events = self.max_events;
        self.run(move |connection| {
            let event_id = event_id.to_string();
            let transaction = connection.transaction().map_err(storage)?;
            let existing: Option<i64> = transaction
                .query_row("SELECT 1 FROM click_spool WHERE event_id = ?1", [&event_id], |row| {
                    row.get(0)
                })
                .optional()
                .map_err(storage)?;
            if existing.is_some() {
                return Ok(());
            }
            let (event_count, queued_bytes): (u64, u64) = transaction
                .query_row(
                    "SELECT event_count, queued_bytes FROM click_spool_state WHERE id = 1",
                    [],
                    |row| Ok((row.get(0)?, row.get(1)?)),
                )
                .map_err(storage)?;
            if event_count >= max_events {
                return Err(SpoolError::Full);
            }
            let payload_bytes = u64::try_from(payload.len())
                .map_err(|_| SpoolError::Storage("payload length overflow".to_owned()))?;
            if queued_bytes.saturating_add(payload_bytes) > max_bytes {
                return Err(SpoolError::Full);
            }
            transaction
                .execute(
                    "INSERT INTO click_spool(event_id, payload, payload_bytes, created_unix) VALUES (?1, ?2, ?3, ?4)",
                    params![event_id, payload, payload_bytes, unix_time()],
                )
                .map_err(storage)?;
            transaction
                .execute(
                    "UPDATE click_spool_state SET event_count = event_count + 1, queued_bytes = queued_bytes + ?1 WHERE id = 1",
                    [payload_bytes],
                )
                .map_err(storage)?;
            transaction.commit().map_err(storage)?;
            Ok(())
        })
        .await
    }

    pub async fn peek(&self, limit: usize) -> Result<Vec<ClickEvent>, SpoolError> {
        self.run(move |connection| {
            let rows = {
                let mut statement = connection
                    .prepare(
                        "SELECT event_id, payload FROM click_spool ORDER BY created_unix, event_id LIMIT ?1",
                    )
                    .map_err(storage)?;
                let rows = statement
                    .query_map([limit as i64], |row| {
                        Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
                    })
                    .map_err(storage)?;
                rows.collect::<Result<Vec<_>, _>>().map_err(storage)?
            };

            let mut events = Vec::with_capacity(rows.len());
            let mut corrupt = Vec::new();
            for (event_id, payload) in rows {
                match serde_json::from_str::<ClickEvent>(&payload) {
                    Ok(event) if event.event_id.to_string() == event_id => events.push(event),
                    Ok(_) => corrupt.push((event_id, payload, "event ID does not match spool key")),
                    Err(_) => corrupt.push((event_id, payload, "click payload cannot be decoded")),
                }
            }
            if !corrupt.is_empty() {
                let quarantined_events = corrupt.len();
                let transaction = connection.transaction().map_err(storage)?;
                for (event_id, payload, reason) in corrupt {
                    transaction
                        .execute(
                            "INSERT OR REPLACE INTO click_spool_dead_letters(event_id, payload, reason, quarantined_unix) VALUES (?1, ?2, ?3, ?4)",
                            params![&event_id, &payload, reason, unix_time()],
                        )
                        .map_err(storage)?;
                    transaction
                        .execute("DELETE FROM click_spool WHERE event_id = ?1", [&event_id])
                        .map_err(storage)?;
                }
                transaction.commit().map_err(storage)?;
                refresh_capacity_state(connection)?;
                warn!(
                    quarantined_events,
                    "quarantined corrupt durable click spool records"
                );
            }
            Ok(events)
        })
        .await
    }

    pub async fn remove(&self, event_ids: &[Uuid]) -> Result<(), SpoolError> {
        if event_ids.is_empty() {
            return Ok(());
        }
        let ids: Vec<String> = event_ids.iter().map(Uuid::to_string).collect();
        let event_count = ids.len();
        let path = Arc::clone(&self.path);
        self.run(move |connection| {
            let transaction = connection.transaction().map_err(storage)?;
            let mut removed_bytes = 0_u64;
            {
                let mut statement = transaction
                    .prepare("SELECT payload_bytes FROM click_spool WHERE event_id = ?1")
                    .map_err(storage)?;
                for id in &ids {
                    let payload_bytes: Option<u64> = statement
                        .query_row([id], |row| row.get(0))
                        .optional()
                        .map_err(storage)?;
                    removed_bytes = removed_bytes.saturating_add(payload_bytes.unwrap_or(0));
                }
            }
            {
                let mut statement = transaction
                    .prepare("DELETE FROM click_spool WHERE event_id = ?1")
                    .map_err(storage)?;
                for id in ids {
                    statement.execute([id]).map_err(storage)?;
                }
            }
            transaction
                .execute(
                    "UPDATE click_spool_state SET event_count = MAX(event_count - ?1, 0), queued_bytes = MAX(queued_bytes - ?2, 0) WHERE id = 1",
                    params![event_count, removed_bytes],
                )
                .map_err(storage)?;
            transaction.commit().map_err(storage)?;
            let remaining: u64 = connection
                .query_row("SELECT event_count FROM click_spool_state WHERE id = 1", [], |row| row.get(0))
                .map_err(storage)?;
            let disk_bytes = database_size(&path);
            if remaining == 0 && disk_bytes >= COMPACT_AFTER_DRAIN_BYTES {
                connection
                    .execute_batch(
                        "PRAGMA wal_checkpoint(TRUNCATE); VACUUM; PRAGMA wal_checkpoint(TRUNCATE);",
                    )
                    .map_err(storage)?;
                info!(disk_bytes, "compacted empty durable click spool");
            }
            Ok(())
        })
        .await
    }

    pub async fn stats(&self) -> Result<SpoolStats, SpoolError> {
        let path = Arc::clone(&self.path);
        self.run(move |connection| {
            let (events, queued_bytes): (u64, u64) = connection
                .query_row(
                    "SELECT event_count, queued_bytes FROM click_spool_state WHERE id = 1",
                    [],
                    |row| Ok((row.get(0)?, row.get(1)?)),
                )
                .map_err(storage)?;
            let oldest: Option<f64> = connection
                .query_row("SELECT MIN(created_unix) FROM click_spool", [], |row| {
                    row.get(0)
                })
                .map_err(storage)?;
            let dead_letter_events: u64 = connection
                .query_row("SELECT COUNT(*) FROM click_spool_dead_letters", [], |row| {
                    row.get(0)
                })
                .map_err(storage)?;
            let oldest_age_seconds = oldest.map_or(0.0, |created| (unix_time() - created).max(0.0));
            Ok(SpoolStats {
                events,
                queued_bytes,
                disk_bytes: database_size(&path),
                oldest_age_seconds,
                dead_letter_events,
            })
        })
        .await
    }

    async fn run<T, F>(&self, operation: F) -> Result<T, SpoolError>
    where
        T: Send + 'static,
        F: FnOnce(&mut Connection) -> Result<T, SpoolError> + Send + 'static,
    {
        let permit = time::timeout(
            self.operation_wait,
            Arc::clone(&self.operation_gate).acquire_owned(),
        )
        .await
        .map_err(|_| SpoolError::Busy)?
        .map_err(|error| SpoolError::Storage(error.to_string()))?;
        let inner = Arc::clone(&self.inner);
        task::spawn_blocking(move || {
            let _permit = permit;
            let mut connection = inner
                .lock()
                .map_err(|_| SpoolError::Storage("spool lock poisoned".to_owned()))?;
            operation(&mut connection)
        })
        .await
        .map_err(|error| SpoolError::Storage(error.to_string()))?
    }
}

#[derive(Debug, Clone, Copy)]
pub struct SpoolStats {
    pub events: u64,
    pub queued_bytes: u64,
    pub disk_bytes: u64,
    pub oldest_age_seconds: f64,
    pub dead_letter_events: u64,
}

fn open_connection(path: &Path) -> Result<Connection, SpoolError> {
    let parent = path
        .parent()
        .ok_or_else(|| SpoolError::Storage("spool path has no parent directory".to_owned()))?;
    fs::create_dir_all(parent).map_err(storage)?;
    let connection = Connection::open(path).map_err(storage)?;
    connection
        .pragma_update(None, "journal_mode", "WAL")
        .map_err(storage)?;
    connection
        .pragma_update(None, "synchronous", "FULL")
        .map_err(storage)?;
    connection
        .busy_timeout(std::time::Duration::from_secs(2))
        .map_err(storage)?;
    connection
        .execute_batch(
            "
            CREATE TABLE IF NOT EXISTS click_spool (
                event_id TEXT PRIMARY KEY,
                payload TEXT NOT NULL,
                payload_bytes INTEGER NOT NULL,
                created_unix REAL NOT NULL
            );
            CREATE INDEX IF NOT EXISTS idx_click_spool_created ON click_spool(created_unix);
            CREATE TABLE IF NOT EXISTS click_spool_state (
                id INTEGER PRIMARY KEY CHECK (id = 1),
                event_count INTEGER NOT NULL,
                queued_bytes INTEGER NOT NULL
            );
            CREATE TABLE IF NOT EXISTS click_spool_dead_letters (
                event_id TEXT PRIMARY KEY,
                payload TEXT NOT NULL,
                reason TEXT NOT NULL,
                quarantined_unix REAL NOT NULL
            );
            ",
        )
        .map_err(storage)?;
    if !has_column(&connection, "click_spool", "payload_bytes")? {
        connection
            .execute(
                "ALTER TABLE click_spool ADD COLUMN payload_bytes INTEGER NOT NULL DEFAULT 0",
                [],
            )
            .map_err(storage)?;
        connection
            .execute(
                "UPDATE click_spool SET payload_bytes = length(CAST(payload AS BLOB)) WHERE payload_bytes = 0",
                [],
            )
            .map_err(storage)?;
    }
    refresh_capacity_state(&connection)?;
    Ok(connection)
}

fn refresh_capacity_state(connection: &Connection) -> Result<(), SpoolError> {
    connection
        .execute(
            "INSERT INTO click_spool_state(id, event_count, queued_bytes) \
             SELECT 1, COUNT(*), COALESCE(SUM(payload_bytes), 0) FROM click_spool WHERE true \
             ON CONFLICT(id) DO UPDATE SET event_count = excluded.event_count, queued_bytes = excluded.queued_bytes",
            [],
        )
        .map_err(storage)?;
    Ok(())
}

fn has_column(connection: &Connection, table: &str, column: &str) -> Result<bool, SpoolError> {
    let mut statement = connection
        .prepare(&format!("PRAGMA table_info({table})"))
        .map_err(storage)?;
    let columns = statement
        .query_map([], |row| row.get::<_, String>(1))
        .map_err(storage)?;
    for candidate in columns {
        if candidate.map_err(storage)? == column {
            return Ok(true);
        }
    }
    Ok(false)
}

fn database_size(path: &Path) -> u64 {
    [
        path.to_path_buf(),
        path.with_extension(format!(
            "{}-wal",
            path.extension()
                .and_then(|value| value.to_str())
                .unwrap_or_default()
        )),
        path.with_extension(format!(
            "{}-shm",
            path.extension()
                .and_then(|value| value.to_str())
                .unwrap_or_default()
        )),
    ]
    .iter()
    .filter_map(|candidate| fs::metadata(candidate).ok())
    .map(|metadata| metadata.len())
    .sum()
}

fn unix_time() -> f64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs_f64()
}

fn storage(error: impl std::fmt::Display) -> SpoolError {
    SpoolError::Storage(error.to_string())
}
