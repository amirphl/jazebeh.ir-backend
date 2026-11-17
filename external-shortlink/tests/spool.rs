use chrono::Utc;
use external_shortlink::{
    domain::ClickEvent,
    spool::{DurableSpool, SpoolError},
};
use rusqlite::Connection;
use std::time::Duration;
use tempfile::tempdir;
use uuid::Uuid;

const OPERATION_WAIT: Duration = Duration::from_secs(1);

fn event() -> ClickEvent {
    ClickEvent {
        event_id: Uuid::new_v4(),
        short_code: "test".to_owned(),
        link_id: 1,
        long_url: "https://example.com".to_owned(),
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
        clicked_at: Utc::now(),
        client_ip: None,
        user_agent: None,
        referer: None,
    }
}

#[tokio::test]
async fn spool_is_durable_across_reopen() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("click-spool.sqlite3");
    let first = event();
    let spool = DurableSpool::open(path.clone(), 10 * 1024 * 1024, 100, OPERATION_WAIT)
        .await
        .unwrap();
    spool.enqueue(&first).await.unwrap();
    drop(spool);

    let reopened = DurableSpool::open(path, 10 * 1024 * 1024, 100, OPERATION_WAIT)
        .await
        .unwrap();
    let events = reopened.peek(10).await.unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].event_id, first.event_id);
    reopened.remove(&[first.event_id]).await.unwrap();
    assert_eq!(reopened.stats().await.unwrap().events, 0);
}

#[tokio::test]
async fn spool_enforces_the_event_limit() {
    let directory = tempdir().unwrap();
    let spool = DurableSpool::open(
        directory.path().join("click-spool.sqlite3"),
        10 * 1024 * 1024,
        1,
        OPERATION_WAIT,
    )
    .await
    .unwrap();
    spool.enqueue(&event()).await.unwrap();
    assert!(matches!(
        spool.enqueue(&event()).await,
        Err(SpoolError::Full)
    ));
}

#[tokio::test]
async fn spool_capacity_is_reclaimed_after_replay() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("click-spool.sqlite3");
    let first = event();
    let spool = DurableSpool::open(path.clone(), 1024 * 1024, 10, OPERATION_WAIT)
        .await
        .unwrap();
    spool.enqueue(&first).await.unwrap();
    let capacity = spool.stats().await.unwrap().queued_bytes + 1;
    spool.remove(&[first.event_id]).await.unwrap();
    drop(spool);

    // The SQLite database file remains allocated after DELETE. Capacity must
    // still be based on queued payloads, not that historical file size.
    let reopened = DurableSpool::open(path, capacity, 10, OPERATION_WAIT)
        .await
        .unwrap();
    reopened.enqueue(&first).await.unwrap();
}

#[tokio::test]
async fn corrupt_spool_record_is_quarantined_without_blocking_replay() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("click-spool.sqlite3");
    let valid = event();
    let spool = DurableSpool::open(path.clone(), 1024 * 1024, 10, OPERATION_WAIT)
        .await
        .unwrap();
    spool.enqueue(&valid).await.unwrap();
    drop(spool);

    let connection = Connection::open(&path).unwrap();
    connection
        .execute(
            "INSERT INTO click_spool(event_id, payload, payload_bytes, created_unix) VALUES (?1, ?2, ?3, ?4)",
            ["corrupt", "not-json", "8", "0"],
        )
        .unwrap();
    drop(connection);

    let reopened = DurableSpool::open(path, 1024 * 1024, 10, OPERATION_WAIT)
        .await
        .unwrap();
    let events = reopened.peek(10).await.unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].event_id, valid.event_id);
    let stats = reopened.stats().await.unwrap();
    assert_eq!(stats.events, 1);
    assert_eq!(stats.dead_letter_events, 1);
}
