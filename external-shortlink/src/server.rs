use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicI64, Ordering},
    },
    time::{Instant, SystemTime, UNIX_EPOCH},
};

use anyhow::{Context, Result};
use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use moka::sync::Cache;
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use subtle::ConstantTimeEq;
use tokio::{
    sync::watch,
    task::JoinHandle,
    time::{self, MissedTickBehavior},
};
use tower_http::trace::TraceLayer;
use tracing::{error, info, warn};

use crate::{
    config::Settings,
    database::{Database, DatabaseError},
    domain::{
        ClickRecord, LinkRecord, LinkUploadRequest, click_from_link, is_valid_code,
        unique_validated_links,
    },
    metrics::Metrics,
    spool::{DurableSpool, SpoolError},
};

const DATABASE_RECONNECT_INTERVAL: std::time::Duration = std::time::Duration::from_secs(5);
const DATABASE_SIZE_REFRESH_INTERVAL: std::time::Duration = std::time::Duration::from_secs(60);
const SPOOL_FAILURE_LOG_INTERVAL_SECONDS: i64 = 60;

#[derive(Clone)]
pub struct AppState {
    settings: Arc<Settings>,
    database: Database,
    spool: DurableSpool,
    cache: Cache<String, Arc<LinkRecord>>,
    metrics: Arc<Metrics>,
    database_ready: Arc<AtomicBool>,
    last_spool_failure_log: Arc<AtomicI64>,
}

impl AppState {
    pub async fn initialize(settings: Settings) -> Result<Self> {
        let database = Database::new(&settings).context("initialize PostgreSQL pool")?;
        let spool = DurableSpool::open(
            settings.spool_path.clone(),
            settings.spool_max_bytes,
            settings.spool_max_events,
            settings.spool_operation_timeout,
        )
        .await
        .context("open durable click spool")?;
        let state = Self {
            cache: Cache::builder()
                .max_capacity(settings.cache_max_entries)
                .build(),
            settings: Arc::new(settings),
            database,
            spool,
            metrics: Arc::new(Metrics::default()),
            database_ready: Arc::new(AtomicBool::new(false)),
            last_spool_failure_log: Arc::new(AtomicI64::new(0)),
        };
        state.refresh_cache_if_available().await;
        if let Some(stats) = state.refresh_spool_metrics().await {
            if stats.events > 0 || stats.dead_letter_events > 0 {
                warn!(
                    pending_events = stats.events,
                    queued_bytes = stats.queued_bytes,
                    dead_letter_events = stats.dead_letter_events,
                    "durable click spool recovered pending state at startup"
                );
            } else {
                info!("durable click spool initialized empty");
            }
        }
        Ok(state)
    }

    async fn refresh_cache_if_available(&self) {
        match self
            .database
            .preload_links(self.settings.cache_preload_entries)
            .await
        {
            Ok(links) => {
                for link in links {
                    self.cache.insert(link.code.clone(), Arc::new(link));
                }
                self.database_ready.store(true, Ordering::Release);
                info!(
                    cached_links = self.cache.entry_count(),
                    "link cache preload completed"
                );
            }
            Err(error) => {
                self.database_ready.store(false, Ordering::Release);
                self.metrics.postgres_error("preload");
                warn!(error = %error, "initial link cache preload deferred because PostgreSQL is unavailable");
            }
        }
    }

    async fn refresh_spool_metrics(&self) -> Option<crate::spool::SpoolStats> {
        match self.spool.stats().await {
            Ok(stats) => {
                self.metrics.spool_stats(
                    stats.events,
                    stats.queued_bytes,
                    stats.disk_bytes,
                    stats.oldest_age_seconds,
                    stats.dead_letter_events,
                );
                Some(stats)
            }
            Err(SpoolError::Busy) => {
                tracing::debug!("durable click spool metrics deferred because the spool is busy");
                None
            }
            Err(error) => {
                error!(error = %error, "could not read durable click spool metrics");
                None
            }
        }
    }
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics))
        .route("/api/v1/links/batch", post(upload_links))
        .route("/api/v1/clicks", get(fetch_clicks))
        .route("/api/v1/clicks/ack", post(acknowledge_clicks))
        .route("/{code}", get(redirect))
        .with_state(state)
        .layer(TraceLayer::new_for_http())
}

pub async fn serve(state: AppState) -> Result<()> {
    let listener = tokio::net::TcpListener::bind(state.settings.bind_addr)
        .await
        .with_context(|| {
            format!(
                "bind external short-link server to {}",
                state.settings.bind_addr
            )
        })?;
    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    let workers = start_background_workers(state.clone(), shutdown_rx);
    info!(
        address = %state.settings.bind_addr,
        database_pool_max = state.settings.pool_max_size,
        cache_capacity = state.settings.cache_max_entries,
        spool_max_events = state.settings.spool_max_events,
        spool_max_bytes = state.settings.spool_max_bytes,
        "external short-link service is listening"
    );

    let shutdown = async move {
        wait_for_shutdown_signal().await;
        let _ = shutdown_tx.send(true);
    };
    axum::serve(listener, router(state))
        .with_graceful_shutdown(shutdown)
        .await
        .context("run HTTP server")?;
    for worker in workers {
        if let Err(error) = worker.await {
            error!(error = %error, "background worker did not stop cleanly");
        }
    }
    Ok(())
}

fn start_background_workers(
    state: AppState,
    shutdown: watch::Receiver<bool>,
) -> Vec<JoinHandle<()>> {
    vec![
        tokio::spawn(database_reconnect_loop(state.clone(), shutdown.clone())),
        tokio::spawn(spool_replay_loop(state.clone(), shutdown.clone())),
        tokio::spawn(purge_loop(state, shutdown)),
    ]
}

async fn database_reconnect_loop(state: AppState, mut shutdown: watch::Receiver<bool>) {
    let mut interval = time::interval(DATABASE_RECONNECT_INTERVAL);
    interval.set_missed_tick_behavior(MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            _ = shutdown.changed() => return,
            _ = interval.tick() => {
                match state.database.ping().await {
                    Ok(()) if !state.database_ready.swap(true, Ordering::AcqRel) => {
                        info!("PostgreSQL recovered; preloading link cache");
                        state.refresh_cache_if_available().await;
                    }
                    Ok(()) => {}
                    Err(error) => {
                        if state.database_ready.swap(false, Ordering::AcqRel) {
                            warn!(error = %error, "PostgreSQL became unavailable; cached redirects remain available");
                        }
                    }
                }
            }
        }
    }
}

async fn spool_replay_loop(state: AppState, mut shutdown: watch::Receiver<bool>) {
    let mut interval = time::interval(state.settings.spool_replay_interval);
    interval.set_missed_tick_behavior(MissedTickBehavior::Skip);
    let mut database_size_interval = time::interval(DATABASE_SIZE_REFRESH_INTERVAL);
    database_size_interval.set_missed_tick_behavior(MissedTickBehavior::Skip);
    let mut spool_backlog_active = false;
    let mut replayed_since_drain = 0_u64;
    let mut replay_failure_reported = false;
    loop {
        tokio::select! {
            _ = shutdown.changed() => return,
            _ = interval.tick() => {
                match state.spool.peek(state.settings.spool_replay_batch_size).await {
                    Ok(events) if !events.is_empty() => {
                        spool_backlog_active = true;
                        match state.database.insert_spooled_clicks(&events).await {
                            Ok(()) => {
                                let ids = events.iter().map(|event| event.event_id).collect::<Vec<_>>();
                                if let Err(error) = state.spool.remove(&ids).await {
                                    state.metrics.spool_dropped("remove_error");
                                    if !replay_failure_reported {
                                        error!(
                                            error = %error,
                                            batch_events = ids.len(),
                                            "clicks were stored in PostgreSQL but could not be removed from spool"
                                        );
                                        replay_failure_reported = true;
                                    }
                                } else {
                                    replayed_since_drain += u64::try_from(ids.len()).unwrap_or(u64::MAX);
                                    replay_failure_reported = false;
                                    tracing::debug!(batch_events = ids.len(), "replayed durable click batch");
                                }
                            }
                            Err(error) => {
                                state.metrics.postgres_error("spool_replay");
                                if !replay_failure_reported {
                                    warn!(
                                        error = %error,
                                        batch_events = events.len(),
                                        "durable click replay deferred because PostgreSQL is unavailable"
                                    );
                                    replay_failure_reported = true;
                                }
                            }
                        }
                    }
                    Ok(_) => {
                        if spool_backlog_active {
                            info!(
                                replayed_events = replayed_since_drain,
                                "durable click spool drained"
                            );
                            spool_backlog_active = false;
                            replayed_since_drain = 0;
                        }
                        replay_failure_reported = false;
                    }
                    Err(SpoolError::Busy) => {
                        tracing::debug!("durable click spool replay deferred because the spool is busy");
                    }
                    Err(error) => {
                        if !replay_failure_reported {
                            error!(error = %error, "could not read durable click spool");
                            replay_failure_reported = true;
                        }
                    }
                }
                state.refresh_spool_metrics().await;
            }
            _ = database_size_interval.tick() => {
                if let Ok(bytes) = state.database.database_size().await {
                    state.metrics.database_size(bytes);
                }
            }
        }
    }
}

async fn purge_loop(state: AppState, mut shutdown: watch::Receiver<bool>) {
    let mut interval = time::interval(state.settings.purge_interval);
    interval.set_missed_tick_behavior(MissedTickBehavior::Skip);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = shutdown.changed() => return,
            _ = interval.tick() => match state.database.purge_acknowledged(state.settings.acknowledged_retention_days).await {
                Ok(count) if count > 0 => info!(count, "purged acknowledged click records"),
                Ok(_) => {},
                Err(error) => {
                    state.metrics.postgres_error("purge");
                    error!(error = %error, "could not purge acknowledged clicks");
                }
            }
        }
    }
}

async fn wait_for_shutdown_signal() {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("install SIGTERM signal handler");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {},
            _ = terminate.recv() => {},
        }
    }
    #[cfg(not(unix))]
    tokio::signal::ctrl_c()
        .await
        .expect("install Ctrl-C signal handler");
}

async fn healthz() -> impl IntoResponse {
    json_response(StatusCode::OK, serde_json::json!({"status": "ok"}))
}

async fn readyz(State(state): State<AppState>) -> Response {
    match state.database.ping().await {
        Ok(()) => json_response(
            StatusCode::OK,
            serde_json::json!({"status": "ready", "postgres": true}),
        ),
        Err(_) => json_response(
            StatusCode::SERVICE_UNAVAILABLE,
            serde_json::json!({"status": "not_ready", "postgres": false}),
        ),
    }
}

async fn metrics(State(state): State<AppState>) -> Response {
    (
        [(
            header::CONTENT_TYPE,
            "text/plain; version=0.0.4; charset=utf-8",
        )],
        state.metrics.render(),
    )
        .into_response()
}

async fn redirect(
    State(state): State<AppState>,
    Path(code): Path<String>,
    headers: HeaderMap,
) -> Response {
    let started = Instant::now();
    let response = redirect_inner(&state, code, &headers).await;
    state
        .metrics
        .redirect(response.status().as_u16(), started.elapsed().as_secs_f64());
    response
}

async fn redirect_inner(state: &AppState, code: String, headers: &HeaderMap) -> Response {
    if !is_valid_code(&code) {
        state.metrics.unknown_code();
        return text_response(StatusCode::NOT_FOUND, "not found");
    }
    let link = if let Some(link) = state.cache.get(&code) {
        state.metrics.cache_lookup("hit");
        link
    } else {
        state.metrics.cache_lookup("miss");
        match tokio::time::timeout(
            state.settings.link_lookup_timeout,
            state.database.lookup_link(&code),
        )
        .await
        {
            Ok(Ok(Some(link))) => {
                let link = Arc::new(link);
                state.cache.insert(code.clone(), Arc::clone(&link));
                link
            }
            Ok(Ok(None)) => {
                state.metrics.unknown_code();
                return text_response(StatusCode::NOT_FOUND, "not found");
            }
            Ok(Err(_)) => {
                state.metrics.postgres_error("lookup_link");
                return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
            }
            Err(_) => {
                state.metrics.pool_timeout();
                return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
            }
        }
    };
    let event = click_from_link(
        &link,
        client_ip(headers),
        header_string(headers, header::USER_AGENT, 1024),
        header_string(headers, header::REFERER, 2048),
    );
    match tokio::time::timeout(
        state.settings.click_insert_timeout,
        state.database.insert_click(&event),
    )
    .await
    {
        Ok(Ok(())) => {}
        Ok(Err(_)) => {
            state.metrics.postgres_error("insert_click");
            spool_click(state, &event).await;
        }
        Err(_) => {
            state.metrics.pool_timeout();
            spool_click(state, &event).await;
        }
    }
    redirect_response(&link.long_url)
}

async fn spool_click(state: &AppState, event: &crate::domain::ClickEvent) {
    match state.spool.enqueue(event).await {
        Ok(()) => {}
        Err(SpoolError::Full) => {
            state.metrics.spool_dropped("capacity");
            if should_log_spool_failure(state) {
                error!("click processor discarded events because spool capacity is exhausted");
            }
        }
        Err(SpoolError::Busy) => {
            state.metrics.spool_dropped("backpressure");
            if should_log_spool_failure(state) {
                error!("click processor discarded events because the spool is saturated");
            }
        }
        Err(error) => {
            state.metrics.spool_dropped("write_error");
            if should_log_spool_failure(state) {
                error!(error = %error, "click processor could not write to durable spool");
            }
        }
    }
}

fn should_log_spool_failure(state: &AppState) -> bool {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
        .try_into()
        .unwrap_or(i64::MAX);
    let previous = state.last_spool_failure_log.load(Ordering::Relaxed);
    if now.saturating_sub(previous) < SPOOL_FAILURE_LOG_INTERVAL_SECONDS {
        return false;
    }
    state
        .last_spool_failure_log
        .compare_exchange(previous, now, Ordering::AcqRel, Ordering::Relaxed)
        .is_ok()
}

async fn upload_links(State(state): State<AppState>, request: Request) -> Response {
    let (parts, body) = request.into_parts();
    let parsed: LinkUploadRequest = match decode_admin_json(&state, &parts.headers, body).await {
        Ok(value) => value,
        Err(response) => {
            state.metrics.mapping_upload("auth_or_validation_error");
            return response;
        }
    };
    if parsed.links.is_empty() || parsed.links.len() > state.settings.admin_batch_max_links {
        state.metrics.mapping_upload("validation_error");
        return json_response(
            StatusCode::BAD_REQUEST,
            serde_json::json!({"error": format!("links must contain 1-{} items", state.settings.admin_batch_max_links)}),
        );
    }
    let links = match unique_validated_links(parsed.links) {
        Ok(links) => links,
        Err(error) => {
            state.metrics.mapping_upload("validation_error");
            return json_response(
                StatusCode::BAD_REQUEST,
                serde_json::json!({"error": error.to_string()}),
            );
        }
    };
    match state.database.upload_links(&links).await {
        Ok(result) => {
            for link in &result.persisted {
                state
                    .cache
                    .insert(link.code.clone(), Arc::new(link.clone()));
            }
            state.metrics.mapping_upload("success");
            state.metrics.mapping_upload_succeeded();
            info!(
                persisted = result.persisted.len(),
                created = result.created,
                existing = result.existing,
                "mapping batch persisted"
            );
            json_response(
                StatusCode::OK,
                serde_json::json!({"persisted": result.persisted.len(), "created": result.created, "existing": result.existing}),
            )
        }
        Err(DatabaseError::MappingConflict(codes)) => {
            state.metrics.mapping_upload("conflict");
            warn!(
                conflicting_codes = codes.len(),
                "mapping batch rejected due to destination conflict"
            );
            json_response(
                StatusCode::CONFLICT,
                serde_json::json!({"error": "one or more codes already map to another destination", "conflicting_codes": codes}),
            )
        }
        Err(error) => {
            state.metrics.postgres_error("upload_links");
            state.metrics.mapping_upload("database_error");
            error!(error = %error, "mapping upload failed");
            json_response(
                StatusCode::SERVICE_UNAVAILABLE,
                serde_json::json!({"error": "mapping persistence failed"}),
            )
        }
    }
}

#[derive(Debug, Deserialize)]
struct ClickQuery {
    #[serde(default)]
    after_id: i64,
    #[serde(default)]
    limit: Option<i64>,
}

const DEFAULT_CLICK_FETCH_LIMIT: i64 = 1_000;

fn effective_click_limit(requested_limit: Option<i64>, maximum_limit: i64) -> i64 {
    requested_limit.unwrap_or_else(|| DEFAULT_CLICK_FETCH_LIMIT.min(maximum_limit))
}

#[derive(Serialize)]
struct ClickPage {
    clicks: Vec<ClickRecord>,
    next_after_id: i64,
    has_more: bool,
}

async fn fetch_clicks(
    State(state): State<AppState>,
    headers: HeaderMap,
    query: Result<Query<ClickQuery>, axum::extract::rejection::QueryRejection>,
) -> Response {
    if !authorized(&headers, &state.settings.api_token) {
        return unauthorized();
    }
    let Query(query) = match query {
        Ok(query) => query,
        Err(_) => {
            return json_response(
                StatusCode::BAD_REQUEST,
                serde_json::json!({"error": "after_id and limit must be integers"}),
            );
        }
    };
    let limit = effective_click_limit(query.limit, state.settings.click_fetch_max_limit);
    if query.after_id < 0 || limit < 1 || limit > state.settings.click_fetch_max_limit {
        return json_response(
            StatusCode::BAD_REQUEST,
            serde_json::json!({"error": format!("after_id must be non-negative and limit must be 1-{}", state.settings.click_fetch_max_limit)}),
        );
    }
    match state.database.fetch_clicks(query.after_id, limit).await {
        Ok((clicks, has_more)) => {
            let next_after_id = clicks.last().map_or(query.after_id, |click| click.click_id);
            json_response(
                StatusCode::OK,
                ClickPage {
                    clicks,
                    next_after_id,
                    has_more,
                },
            )
        }
        Err(error) => {
            state.metrics.postgres_error("fetch_clicks");
            error!(error = %error, "click fetch failed");
            json_response(
                StatusCode::SERVICE_UNAVAILABLE,
                serde_json::json!({"error": "click fetch failed"}),
            )
        }
    }
}

#[derive(Deserialize)]
struct AcknowledgeRequest {
    through_click_id: i64,
}

async fn acknowledge_clicks(State(state): State<AppState>, request: Request) -> Response {
    let (parts, body) = request.into_parts();
    let payload: AcknowledgeRequest = match decode_admin_json(&state, &parts.headers, body).await {
        Ok(payload) => payload,
        Err(response) => return response,
    };
    if payload.through_click_id < 0 {
        return json_response(
            StatusCode::BAD_REQUEST,
            serde_json::json!({"error": "through_click_id must be a non-negative integer"}),
        );
    }
    match state.database.acknowledge(payload.through_click_id).await {
        Ok(acknowledged) => {
            state.metrics.acknowledged_through(acknowledged);
            info!(
                through_click_id = acknowledged,
                "click import cursor acknowledged"
            );
            json_response(
                StatusCode::OK,
                serde_json::json!({"through_click_id": acknowledged}),
            )
        }
        Err(DatabaseError::UnknownAcknowledgement) => json_response(
            StatusCode::CONFLICT,
            serde_json::json!({"error": "through_click_id is not a persisted click_id"}),
        ),
        Err(error) => {
            state.metrics.postgres_error("acknowledge");
            error!(error = %error, "click acknowledgement failed");
            json_response(
                StatusCode::SERVICE_UNAVAILABLE,
                serde_json::json!({"error": "acknowledgement failed"}),
            )
        }
    }
}

async fn decode_admin_json<T: DeserializeOwned>(
    state: &AppState,
    headers: &HeaderMap,
    body: Body,
) -> Result<T, Response> {
    if !authorized(headers, &state.settings.api_token) {
        return Err(unauthorized());
    }
    if let Some(length) = headers.get(header::CONTENT_LENGTH) {
        let length = length
            .to_str()
            .ok()
            .and_then(|value| value.parse::<usize>().ok())
            .ok_or_else(|| {
                json_response(
                    StatusCode::BAD_REQUEST,
                    serde_json::json!({"error": "invalid content-length"}),
                )
            })?;
        if length > state.settings.max_admin_body_bytes {
            return Err(json_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                serde_json::json!({"error": "request body too large"}),
            ));
        }
    }
    let bytes = to_bytes(body, state.settings.max_admin_body_bytes)
        .await
        .map_err(|_| {
            json_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                serde_json::json!({"error": "request body too large"}),
            )
        })?;
    serde_json::from_slice(&bytes).map_err(|_| {
        json_response(
            StatusCode::BAD_REQUEST,
            serde_json::json!({"error": "invalid JSON"}),
        )
    })
}

fn authorized(headers: &HeaderMap, expected: &str) -> bool {
    let Some(value) = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
    else {
        return false;
    };
    let Some((scheme, supplied)) = value.split_once(' ') else {
        return false;
    };
    scheme.eq_ignore_ascii_case("bearer") && supplied.as_bytes().ct_eq(expected.as_bytes()).into()
}

fn unauthorized() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        [(header::WWW_AUTHENTICATE, HeaderValue::from_static("Bearer"))],
        axum::Json(serde_json::json!({"error": "unauthorized"})),
    )
        .into_response()
}

fn client_ip(headers: &HeaderMap) -> Option<String> {
    headers
        .get("x-forwarded-for")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(|value| truncate(value, 64))
}

fn header_string(headers: &HeaderMap, name: header::HeaderName, maximum: usize) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.is_empty())
        .map(|value| truncate(value, maximum))
}

fn truncate(value: &str, maximum_bytes: usize) -> String {
    if value.len() <= maximum_bytes {
        return value.to_owned();
    }
    let mut end = maximum_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_owned()
}

fn redirect_response(destination: &str) -> Response {
    let Ok(location) = HeaderValue::from_str(destination) else {
        return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
    };
    (StatusCode::FOUND, [(header::LOCATION, location)]).into_response()
}

fn text_response(status: StatusCode, message: &'static str) -> Response {
    (status, message).into_response()
}

fn json_response(status: StatusCode, value: impl Serialize) -> Response {
    (status, axum::Json(value)).into_response()
}

#[cfg(test)]
mod tests {
    use std::{net::SocketAddr, path::PathBuf, time::Duration};

    use axum::{
        body::Body,
        http::{Request as HttpRequest, StatusCode},
    };
    use tower::ServiceExt;

    use super::*;

    fn settings(path: PathBuf) -> Settings {
        Settings {
            bind_addr: "127.0.0.1:0".parse::<SocketAddr>().unwrap(),
            database_url: "postgresql://postgres@127.0.0.1:1/external_shortlink".to_owned(),
            api_token: "x".repeat(32),
            spool_path: path,
            pool_min_size: 0,
            pool_max_size: 1,
            db_command_timeout: Duration::from_millis(50),
            db_lock_timeout: Duration::from_millis(50),
            click_insert_timeout: Duration::from_millis(5),
            link_lookup_timeout: Duration::from_millis(5),
            cache_max_entries: 100,
            cache_preload_entries: 0,
            admin_batch_max_links: 100,
            click_fetch_max_limit: 100,
            max_admin_body_bytes: 1024 * 1024,
            spool_max_bytes: 10 * 1024 * 1024,
            spool_max_events: 100,
            spool_replay_batch_size: 10,
            spool_replay_interval: Duration::from_secs(60),
            spool_operation_timeout: Duration::from_secs(1),
            acknowledged_retention_days: 7,
            purge_interval: Duration::from_secs(60),
        }
    }

    fn link() -> LinkRecord {
        LinkRecord {
            link_id: 7,
            code: "abc1".to_owned(),
            long_url: "https://example.com/destination".to_owned(),
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
        }
    }

    #[tokio::test]
    async fn cached_mapping_redirects_and_spools_click_when_database_is_down() {
        let directory = tempfile::tempdir().unwrap();
        let state = AppState::initialize(settings(directory.path().join("spool.sqlite3")))
            .await
            .unwrap();
        state.cache.insert("abc1".to_owned(), Arc::new(link()));
        let response = router(state.clone())
            .oneshot(
                HttpRequest::builder()
                    .uri("/abc1")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::FOUND);
        assert_eq!(
            response.headers()[header::LOCATION],
            "https://example.com/destination"
        );
        assert_eq!(state.spool.stats().await.unwrap().events, 1);
    }

    #[tokio::test]
    async fn admin_endpoints_require_a_bearer_token() {
        let directory = tempfile::tempdir().unwrap();
        let state = AppState::initialize(settings(directory.path().join("spool.sqlite3")))
            .await
            .unwrap();
        let response = router(state)
            .oneshot(
                HttpRequest::builder()
                    .method("POST")
                    .uri("/api/v1/links/batch")
                    .body(Body::from(r#"{"links": []}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }

    #[test]
    fn omitted_click_limit_never_exceeds_the_configured_maximum() {
        assert_eq!(effective_click_limit(None, 100), 100);
        assert_eq!(effective_click_limit(None, 2_000), 1_000);
        assert_eq!(effective_click_limit(Some(500), 100), 500);
    }
}
