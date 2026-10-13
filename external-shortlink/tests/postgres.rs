use std::{env, time::Duration};

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
    let xmin_before: String = sqlx::query_scalar("SELECT xmin::text FROM links WHERE code = $1")
        .bind(&code)
        .fetch_one(&setup_pool)
        .await
        .unwrap();
    let existing = database
        .upload_links(std::slice::from_ref(&mapping))
        .await
        .unwrap();
    assert_eq!((existing.created, existing.existing), (0, 1));
    let xmin_after: String = sqlx::query_scalar("SELECT xmin::text FROM links WHERE code = $1")
        .bind(&code)
        .fetch_one(&setup_pool)
        .await
        .unwrap();
    assert_eq!(
        xmin_after, xmin_before,
        "unchanged mappings must not be rewritten"
    );

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
    database.insert_click(&second).await.unwrap();

    let own_click_id: i64 =
        sqlx::query_scalar("SELECT click_id FROM clicks_v2 WHERE event_id = $1")
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

    // Exercise the retention path with an acknowledged, expired row. The
    // production index is verified by the deployment schema; this confirms
    // that timestamp ordering preserves cursor-based deletion semantics.
    sqlx::query(
        "UPDATE clicks_v2 SET clicked_at = CURRENT_TIMESTAMP - INTERVAL '8 days' WHERE event_id = $1",
    )
    .bind(event.event_id)
    .execute(&setup_pool)
    .await
    .unwrap();
    // Partition retention is performed by the privileged host maintenance
    // timer; the application purge loop drains only legacy rows during the
    // online migration.
    assert_eq!(database.purge_acknowledged(7).await.unwrap(), 0);
    let retained: i64 = sqlx::query_scalar("SELECT count(*) FROM clicks_v2 WHERE event_id = $1")
        .bind(event.event_id)
        .fetch_one(&setup_pool)
        .await
        .unwrap();
    assert_eq!(retained, 1);
}

// Exercise real mapping lookup and persisted clicks alongside a local destination.
#[tokio::test]
#[ignore = "requires EXTERNAL_SHORTLINK_TEST_DATABASE_URL"]
async fn automated_visits_track_without_reaching_destination() {
    use axum::{
        Router,
        body::{Body, to_bytes},
        http::{Request, StatusCode, header},
        routing::get,
    };
    use external_shortlink::server::{AppState, router};
    use std::sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    };
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tower::ServiceExt;

    let database_url = env::var("EXTERNAL_SHORTLINK_TEST_DATABASE_URL").unwrap();
    let pool = PgPool::connect(&database_url).await.unwrap();
    let hits = Arc::new(AtomicU64::new(0));
    let count = hits.clone();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let destination = listener.local_addr().unwrap();
    let destination_app = Router::new().route(
        "/landing",
        get(move || {
            let count = count.clone();
            async move {
                count.fetch_add(1, Ordering::Relaxed);
                "destination"
            }
        }),
    );
    let server = tokio::spawn(async move { axum::serve(listener, destination_app).await.unwrap() });
    let code = format!("preview-{}", Uuid::new_v4().simple());
    let mut mapping = link(code.clone());
    mapping.long_url = format!("http://{destination}/landing");
    mapping.is_test = true; // Test links must still redirect human visitors.
    Database::new(&settings(database_url.clone()))
        .unwrap()
        .upload_links(&[mapping.clone()])
        .await
        .unwrap();
    let mut config = settings(database_url);
    config.click_insert_timeout = Duration::from_secs(5);
    config.link_lookup_timeout = Duration::from_secs(5);
    let state = AppState::initialize(config).await.unwrap();
    for (index, ua) in [
        "GoogleMessages",
        "Mozilla/5.0 Chrome/120",
        "Twitterbot",
        "Mozilla/5.0 Safari/604",
    ]
    .iter()
    .enumerate()
    {
        let response = router(state.clone())
            .oneshot(
                Request::builder()
                    .uri(format!("/{code}"))
                    .header(header::USER_AGENT, *ua)
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
        // Follow redirects exactly when the response advertises one.
        if index % 2 == 1 {
            assert_eq!(response.status(), StatusCode::FOUND);
            let location = response.headers().get(header::LOCATION).unwrap();
            assert_eq!(location.to_str().unwrap(), mapping.long_url);
            let mut stream = tokio::net::TcpStream::connect(destination).await.unwrap();
            stream
                .write_all(
                    format!(
                        "GET /landing HTTP/1.1\r\nHost: {destination}\r\nConnection: close\r\n\r\n"
                    )
                    .as_bytes(),
                )
                .await
                .unwrap();
            let mut body = Vec::new();
            stream.read_to_end(&mut body).await.unwrap();
            assert!(String::from_utf8_lossy(&body).contains("destination"));
        } else {
            assert_eq!(response.status(), StatusCode::OK);
            assert!(!response.headers().contains_key(header::LOCATION));
            assert!(
                to_bytes(response.into_body(), 1024)
                    .await
                    .unwrap()
                    .is_empty()
            );
        }
        assert_eq!(hits.load(Ordering::Relaxed), index.div_ceil(2) as u64);
    }
    let clicks: i64 = sqlx::query_scalar("SELECT count(*) FROM clicks_v2 WHERE short_code = $1")
        .bind(&code)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(clicks, 4);
    let missing = router(state)
        .oneshot(
            Request::builder()
                .uri("/unknown-preview-code")
                .header(header::USER_AGENT, "Googlebot")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(missing.status(), StatusCode::NOT_FOUND);
    assert_eq!(missing.headers()[header::CACHE_CONTROL], "no-store");
    server.abort();
}
