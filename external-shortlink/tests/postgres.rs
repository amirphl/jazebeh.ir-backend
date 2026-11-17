use std::{env, path::PathBuf, time::Duration};

use chrono::Utc;
use external_shortlink::{
    config::Settings,
    database::{Database, DatabaseError},
    domain::{LinkInput, click_from_link},
};
use sqlx::PgPool;
use uuid::Uuid;

fn settings(database_url: String) -> Settings {
    Settings {
        bind_addr: "127.0.0.1:0".parse().unwrap(),
        database_url,
        api_token: "x".repeat(32),
        spool_path: PathBuf::from("/tmp/not-used.sqlite3"),
        pool_min_size: 0,
        pool_max_size: 2,
        db_command_timeout: Duration::from_secs(5),
        db_lock_timeout: Duration::from_secs(5),
        click_insert_timeout: Duration::from_millis(100),
        link_lookup_timeout: Duration::from_millis(100),
        cache_max_entries: 100,
        cache_preload_entries: 0,
        admin_batch_max_links: 100,
        click_fetch_max_limit: 100,
        max_admin_body_bytes: 1024 * 1024,
        spool_max_bytes: 1024 * 1024,
        spool_max_events: 100,
        spool_replay_batch_size: 10,
        spool_replay_interval: Duration::from_secs(1),
        spool_operation_timeout: Duration::from_secs(1),
        acknowledged_retention_days: 7,
        purge_interval: Duration::from_secs(60),
    }
}

fn link(code: String) -> LinkInput {
    LinkInput {
        code,
        long_url: "https://example.com/destination".to_owned(),
        short_url: Some("https://links.example.com/test".to_owned()),
        source_link_id: Some(42),
        campaign_id: Some(9),
        client_id: None,
        scenario_id: None,
        scenario_name: None,
        phone_number: None,
        is_test: false,
        source_created_at: Some(Utc::now()),
        source_updated_at: Some(Utc::now()),
    }
}

#[tokio::test]
#[ignore = "requires EXTERNAL_SHORTLINK_TEST_DATABASE_URL"]
async fn postgres_mapping_click_cursor_and_idempotency() {
    let database_url = env::var("EXTERNAL_SHORTLINK_TEST_DATABASE_URL")
        .expect("EXTERNAL_SHORTLINK_TEST_DATABASE_URL must be set");

    let setup_pool = PgPool::connect(&database_url).await.unwrap();
    let database = Database::new(&settings(database_url)).unwrap();
    let code = format!("test-{}", Uuid::new_v4().simple());
    let mapping = link(code.clone()).validate_and_normalize().unwrap();

    let created = database
        .upload_links(std::slice::from_ref(&mapping))
        .await
        .unwrap();
    assert_eq!(
        (created.created, created.existing, created.persisted.len()),
        (1, 0, 1)
    );
    let existing = database
        .upload_links(std::slice::from_ref(&mapping))
        .await
        .unwrap();
    assert_eq!((existing.created, existing.existing), (0, 1));

    let cleared_metadata = LinkInput {
        code: code.clone(),
        long_url: mapping.long_url.clone(),
        short_url: None,
        source_link_id: None,
        campaign_id: None,
        client_id: None,
        scenario_id: None,
        scenario_name: None,
        phone_number: None,
        is_test: false,
        source_created_at: None,
        source_updated_at: None,
    };
    database.upload_links(&[cleared_metadata]).await.unwrap();
    let cleared = database.lookup_link(&code).await.unwrap().unwrap();
    assert!(cleared.short_url.is_none());
    assert!(cleared.source_link_id.is_none());
    assert!(cleared.campaign_id.is_none());
    assert!(cleared.source_created_at.is_none());
    assert!(matches!(
        database
            .upload_links(&[LinkInput {
                long_url: "https://example.com/other".to_owned(),
                ..mapping.clone()
            }])
            .await,
        Err(DatabaseError::MappingConflict(_))
    ));

    let concurrent_code = format!("race-{}", Uuid::new_v4().simple());
    let first_mapping = link(concurrent_code.clone())
        .validate_and_normalize()
        .unwrap();
    let second_mapping = LinkInput {
        long_url: "https://example.com/racing-destination".to_owned(),
        ..first_mapping.clone()
    }
    .validate_and_normalize()
    .unwrap();
    let first_database = database.clone();
    let second_database = database.clone();
    let first_batch = [first_mapping];
    let second_batch = [second_mapping];
    let (first_result, second_result) = tokio::join!(
        first_database.upload_links(&first_batch),
        second_database.upload_links(&second_batch),
    );
    assert!(
        matches!(
            (&first_result, &second_result),
            (Ok(_), Err(DatabaseError::MappingConflict(_)))
                | (Err(DatabaseError::MappingConflict(_)), Ok(_))
        ),
        "concurrent mapping results were {first_result:?} and {second_result:?}"
    );

    let stored_link = database.lookup_link(&code).await.unwrap().unwrap();
    let event = click_from_link(&stored_link, None, None, None);
    database.insert_click(&event).await.unwrap();
    database.insert_click(&event).await.unwrap();
    let second = click_from_link(&stored_link, None, None, None);
    database.insert_spooled_clicks(&[second]).await.unwrap();

    let own_click_id: i64 = sqlx::query_scalar("SELECT click_id FROM clicks WHERE event_id = $1")
        .bind(event.event_id)
        .fetch_one(&setup_pool)
        .await
        .unwrap();
    let (page, _) = database.fetch_clicks(own_click_id - 1, 10).await.unwrap();
    assert_eq!(page.first().unwrap().event.event_id, event.event_id);
    let final_id = page.last().unwrap().click_id;
    let acknowledged = database.acknowledge(final_id).await.unwrap();
    assert!(acknowledged >= final_id);
    assert_eq!(database.acknowledge(final_id).await.unwrap(), acknowledged);
}
