use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
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
use tracing::{debug, error, info, warn};

use crate::{
    config::Settings,
    database::{Database, DatabaseError},
    domain::{
        ClickRecord, LinkRecord, LinkUploadRequest, click_from_link, is_valid_code,
        unique_validated_links,
    },
    metrics::Metrics,
};

const DATABASE_RECONNECT_INTERVAL: std::time::Duration = std::time::Duration::from_secs(5);
const DATABASE_SIZE_REFRESH_INTERVAL: std::time::Duration = std::time::Duration::from_secs(60);
const CLICK_WRITE_ATTEMPTS: usize = 3;
const CLICK_RETRY_DELAY: Duration = Duration::from_millis(20);

#[derive(Clone)]
pub struct AppState {
    settings: Arc<Settings>,
    database: Database,
    cache: Cache<String, Arc<LinkRecord>>,
    metrics: Arc<Metrics>,
    database_ready: Arc<AtomicBool>,
}

impl AppState {
    pub async fn initialize(settings: Settings) -> Result<Self> {
        let database = Database::new(&settings).context("initialize PostgreSQL pool")?;
        let state = Self {
            cache: Cache::builder()
                .max_capacity(settings.cache_max_entries)
                .build(),
            settings: Arc::new(settings),
            database,
            metrics: Arc::new(Metrics::default()),
            database_ready: Arc::new(AtomicBool::new(false)),
        };
        state.refresh_cache_if_available().await;
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
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics))
        .route("/api/v1/links/batch", post(upload_links))
        .route("/api/v1/clicks", get(fetch_clicks))
        .route("/api/v1/clicks/ack", post(acknowledge_clicks))
        // HEAD is registered explicitly: axum routes HEAD to `get` by default,
        // which would record a click for every unfurler, prefetcher and probe.
        .route("/{code}", get(redirect).head(head_redirect))
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
        "external short-link service is listening"
    );

    let shutdown = async move {
        wait_for_shutdown_signal().await;
        info!("shutdown signal received; stopping external short-link service");
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
        tokio::spawn(database_metrics_loop(state.clone(), shutdown.clone())),
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
                            warn!(error = %error, "PostgreSQL became unavailable; redirects are withheld until it recovers");
                        }
                    }
                }
            }
        }
    }
}

async fn database_metrics_loop(state: AppState, mut shutdown: watch::Receiver<bool>) {
    let mut interval = time::interval(DATABASE_SIZE_REFRESH_INTERVAL);
    interval.set_missed_tick_behavior(MissedTickBehavior::Skip);
    let mut last_default_partition_backlog = None;
    let mut reported_missing_default_partition = false;
    loop {
        tokio::select! {
            _ = shutdown.changed() => return,
            _ = interval.tick() => {
                match state.database.database_size().await {
                    Ok(bytes) => state.metrics.database_size(bytes),
                    Err(error) => {
                        state.metrics.postgres_error("database_size");
                        warn!(error = %error, "could not refresh PostgreSQL size metric");
                    }
                }
                // A click must never be rejected because its day partition is
                // missing; the default partition makes that impossible, and rows
                // landing in it are the one signal that the window fell behind.
                match state.database.default_partition_backlog().await {
                    Ok(Some(backlog)) => {
                        state.metrics.default_partition_backlog(backlog);
                        if last_default_partition_backlog != Some(backlog) {
                            if backlog {
                                error!(
                                    "clicks are landing in the clicks_v2 default partition; the partition maintenance window is behind and retention cannot drop those clicks"
                                );
                            } else {
                                info!("clicks_v2 default partition is empty again; the partition window has caught up");
                            }
                            last_default_partition_backlog = Some(backlog);
                        }
                    }
                    Ok(None) => {
                        if !reported_missing_default_partition {
                            warn!(
                                "clicks_v2 has no default partition; apply the deployment schema so a missing day partition cannot reject every click"
                            );
                            reported_missing_default_partition = true;
                        }
                    }
                    Err(error) => {
                        state.metrics.postgres_error("default_partition_backlog");
                        warn!(error = %error, "could not check the clicks_v2 default partition");
                    }
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
        Err(error) => {
            warn!(error = %error, "readiness check failed because PostgreSQL is unavailable");
            json_response(
                StatusCode::SERVICE_UNAVAILABLE,
                serde_json::json!({"status": "not_ready", "postgres": false}),
            )
        }
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
    handle_redirect(&state, code, &headers, true).await
}

/// HEAD resolves the same destination but never records a click: probes, link
/// unfurlers and prefetchers send HEAD, and none of them is a click. It also
/// performs no durable write, so a resolved mapping is still answered while
/// PostgreSQL is unavailable: the fail-closed rule protects the click record,
/// not a metadata probe.
async fn head_redirect(
    State(state): State<AppState>,
    Path(code): Path<String>,
    headers: HeaderMap,
) -> Response {
    handle_redirect(&state, code, &headers, false).await
}

async fn handle_redirect(
    state: &AppState,
    code: String,
    headers: &HeaderMap,
    record_click: bool,
) -> Response {
    let started = Instant::now();
    let mut response = redirect_inner(state, code.clone(), headers, record_click).await;
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    state
        .metrics
        .redirect(response.status().as_u16(), started.elapsed().as_secs_f64());
    if !matches!(response.status(), StatusCode::FOUND | StatusCode::OK) {
        warn!(
            short_code = %code,
            status = response.status().as_u16(),
            elapsed_ms = started.elapsed().as_millis(),
            "redirect request did not complete successfully"
        );
    }
    response
}

async fn redirect_inner(
    state: &AppState,
    code: String,
    headers: &HeaderMap,
    record_click: bool,
) -> Response {
    if !is_valid_code(&code) {
        state.metrics.unknown_code();
        warn!(
            code_length = code.len(),
            "redirect rejected because short code format is invalid"
        );
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
                warn!(short_code = %code, "redirect target was not found");
                return text_response(StatusCode::NOT_FOUND, "not found");
            }
            Ok(Err(error)) => {
                state.metrics.postgres_error("lookup_link");
                error!(short_code = %code, error = %error, "redirect lookup failed; redirect withheld");
                return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
            }
            Err(_) => {
                state.metrics.pool_timeout();
                error!(short_code = %code, timeout_ms = state.settings.link_lookup_timeout.as_millis(), "redirect lookup timed out; redirect withheld");
                return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
            }
        }
    };
    if record_click {
        let event = click_from_link(
            &link,
            client_ip(headers),
            header_string(headers, header::USER_AGENT, 1024),
            header_string(headers, header::REFERER, 2048),
        );
        if let Err(error) = persist_click(state, &event).await {
            state.metrics.postgres_error("insert_click");
            if let Some(fault) = click_storage_fault(&error) {
                state.metrics.click_store_fault(fault.reason);
                error!(
                    event_id = %event.event_id,
                    short_code = %event.short_code,
                    fault = fault.reason,
                    action = fault.action,
                    "click rejected because the click store refused it"
                );
            } else {
                error!(event_id = %event.event_id, short_code = %event.short_code, error = %error, "click persistence failed; redirect withheld");
            }
            return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
        }
        debug!(event_id = %event.event_id, short_code = %event.short_code, "click persisted; issuing redirect");
    } else {
        debug!(short_code = %link.code, "HEAD request resolved without recording a click");
    }
    if HeaderValue::from_str(&link.long_url).is_err() {
        error!(short_code = %link.code, "destination cannot be redirected because it is not a valid HTTP Location header");
        return text_response(StatusCode::SERVICE_UNAVAILABLE, "temporarily unavailable");
    }
    // Preserve click tracking, but prevent automated clients from following the destination.
    if crate::traffic::is_automated_request(
        headers
            .get(header::USER_AGENT)
            .and_then(|value| value.to_str().ok())
            .unwrap_or(""),
        client_ip(headers).as_deref().unwrap_or(""),
    ) {
        state.metrics.suppressed_bot_redirect();
        return StatusCode::OK.into_response();
    }
    redirect_response(&link.long_url)
}

async fn persist_click(
    state: &AppState,
    event: &crate::domain::ClickEvent,
) -> Result<(), DatabaseError> {
    let mut last_error = None;
    for attempt in 0..CLICK_WRITE_ATTEMPTS {
        match tokio::time::timeout(
            state.settings.click_insert_timeout,
            state.database.insert_click(event),
        )
        .await
        {
            Ok(Ok(())) => return Ok(()),
            Ok(Err(error)) => {
                warn!(event_id = %event.event_id, short_code = %event.short_code, attempt = attempt + 1, max_attempts = CLICK_WRITE_ATTEMPTS, error = %error, "click persistence attempt failed");
                last_error = Some(error);
            }
            Err(_) => {
                warn!(event_id = %event.event_id, short_code = %event.short_code, attempt = attempt + 1, max_attempts = CLICK_WRITE_ATTEMPTS, timeout_ms = state.settings.click_insert_timeout.as_millis(), "click persistence attempt timed out");
                last_error = Some(DatabaseError::Sql(sqlx::Error::PoolTimedOut));
            }
        }
        if attempt + 1 < CLICK_WRITE_ATTEMPTS {
            time::sleep(CLICK_RETRY_DELAY * u32::try_from(attempt + 1).unwrap_or(1)).await;
        }
    }
    Err(last_error.unwrap_or(DatabaseError::Sql(sqlx::Error::PoolTimedOut)))
}

async fn upload_links(State(state): State<AppState>, request: Request) -> Response {
    let (parts, body) = request.into_parts();
    let parsed: LinkUploadRequest = match decode_admin_json(&state, &parts.headers, body).await {
        Ok(value) => value,
        Err(response) => {
            state.metrics.mapping_upload("auth_or_validation_error");
            warn!("mapping upload rejected during authentication or JSON/body validation");
            return response;
        }
    };
    if parsed.links.is_empty() || parsed.links.len() > state.settings.admin_batch_max_links {
        state.metrics.mapping_upload("validation_error");
        warn!(
            link_count = parsed.links.len(),
            maximum = state.settings.admin_batch_max_links,
            "mapping upload rejected due to invalid batch size"
        );
        return json_response(
            StatusCode::BAD_REQUEST,
            serde_json::json!({"error": format!("links must contain 1-{} items", state.settings.admin_batch_max_links)}),
        );
    }
    let links = match unique_validated_links(parsed.links) {
        Ok(links) => links,
        Err(error) => {
            state.metrics.mapping_upload("validation_error");
            warn!(error = %error, "mapping upload rejected due to invalid mapping data");
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
        warn!("click fetch rejected because authorization failed");
        return unauthorized();
    }
    let Query(query) = match query {
        Ok(query) => query,
        Err(error) => {
            warn!(error = %error, "click fetch rejected because query parameters are malformed");
            return json_response(
                StatusCode::BAD_REQUEST,
                serde_json::json!({"error": "after_id and limit must be integers"}),
            );
        }
    };
    let limit = effective_click_limit(query.limit, state.settings.click_fetch_max_limit);
    if query.after_id < 0 || limit < 1 || limit > state.settings.click_fetch_max_limit {
        warn!(
            after_id = query.after_id,
            limit,
            maximum = state.settings.click_fetch_max_limit,
            "click fetch rejected because cursor or limit is invalid"
        );
        return json_response(
            StatusCode::BAD_REQUEST,
            serde_json::json!({"error": format!("after_id must be non-negative and limit must be 1-{}", state.settings.click_fetch_max_limit)}),
        );
    }
    match state.database.fetch_clicks(query.after_id, limit).await {
        Ok((clicks, has_more)) => {
            let next_after_id = clicks.last().map_or(query.after_id, |click| click.click_id);
            debug!(
                after_id = query.after_id,
                returned = clicks.len(),
                next_after_id,
                has_more,
                "click page served"
            );
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
            if let Some(fault) = click_storage_fault(&error) {
                state.metrics.click_store_fault(fault.reason);
                error!(
                    fault = fault.reason,
                    action = fault.action,
                    "click fetch rejected by the click store"
                );
            } else {
                error!(error = %error, "click fetch failed");
            }
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
        warn!(
            through_click_id = payload.through_click_id,
            "click acknowledgement rejected because cursor is negative"
        );
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
        Err(DatabaseError::UnknownAcknowledgement) => {
            warn!(
                through_click_id = payload.through_click_id,
                "click acknowledgement rejected because click ID is unknown"
            );
            json_response(
                StatusCode::CONFLICT,
                serde_json::json!({"error": "through_click_id is not a persisted click_id"}),
            )
        }
        Err(error) => {
            state.metrics.postgres_error("acknowledge");
            if let Some(fault) = click_storage_fault(&error) {
                state.metrics.click_store_fault(fault.reason);
                error!(
                    fault = fault.reason,
                    action = fault.action,
                    "click acknowledgement rejected by the click store"
                );
            } else {
                error!(error = %error, "click acknowledgement failed");
            }
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
        warn!("admin API request rejected because authorization failed");
        return Err(unauthorized());
    }
    if let Some(length) = headers.get(header::CONTENT_LENGTH) {
        let length = length
            .to_str()
            .ok()
            .and_then(|value| value.parse::<usize>().ok())
            .ok_or_else(|| {
                warn!("admin API request rejected because content-length is invalid");
                json_response(
                    StatusCode::BAD_REQUEST,
                    serde_json::json!({"error": "invalid content-length"}),
                )
            })?;
        if length > state.settings.max_admin_body_bytes {
            warn!(
                length,
                maximum = state.settings.max_admin_body_bytes,
                "admin API request rejected because body is too large"
            );
            return Err(json_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                serde_json::json!({"error": "request body too large"}),
            ));
        }
    }
    let bytes = to_bytes(body, state.settings.max_admin_body_bytes)
        .await
        .map_err(|error| {
            warn!(error = %error, maximum = state.settings.max_admin_body_bytes, "admin API request body could not be read");
            json_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                serde_json::json!({"error": "request body too large"}),
            )
        })?;
    serde_json::from_slice(&bytes).map_err(|error| {
        warn!(error = %error, body_bytes = bytes.len(), "admin API request rejected because JSON is invalid");
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

/// PostgreSQL reports "no partition of relation ... found for row" as a
/// check-violation SQLSTATE, so the message decides whether this is the one
/// check violation the click path treats as a partition-schema problem.
const MISSING_PARTITION_SQLSTATE: &str = "23514";
/// `insufficient_privilege`: the runtime role cannot read or write an object it
/// needs, which is a grant problem rather than a transient database problem.
const INSUFFICIENT_PRIVILEGE_SQLSTATE: &str = "42501";

/// A click-store failure that points at the deployment instead of at a transient
/// database problem. Naming these explicitly is what keeps a day-boundary
/// failure from looking like a random outage.
struct StorageFault {
    reason: &'static str,
    action: &'static str,
}

fn click_storage_fault(error: &DatabaseError) -> Option<StorageFault> {
    let DatabaseError::Sql(sqlx::Error::Database(database_error)) = error else {
        return None;
    };
    match database_error.code().as_deref() {
        // The service schema installs a default partition precisely so this
        // cannot happen; a match means the schema is stale.
        Some(MISSING_PARTITION_SQLSTATE)
            if database_error
                .message()
                .contains("no partition of relation") =>
        {
            Some(StorageFault {
                reason: "missing_partition",
                action: "clicks_v2 has no partition for this click day and no default partition; apply the deployment schema",
            })
        }
        // Queries name the partitioned parent, so this is about the parent's
        // grants or another click-store object, not about a partition's own ACL.
        Some(INSUFFICIENT_PRIVILEGE_SQLSTATE) => Some(StorageFault {
            reason: "permission_denied",
            action: "the runtime role lacks privileges on the click store; run the partition maintenance unit or deploy to reconcile grants",
        }),
        _ => None,
    }
}

fn redirect_response(destination: &str) -> Response {
    let Ok(location) = HeaderValue::from_str(destination) else {
        error!(
            "redirect response construction failed because destination is not a valid HTTP Location header"
        );
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
    use std::{net::SocketAddr, time::Duration};

    use axum::{
        body::Body,
        http::{Request as HttpRequest, StatusCode},
    };
    use tower::ServiceExt;

    use super::*;

    fn settings() -> Settings {
        Settings {
            bind_addr: "127.0.0.1:0".parse::<SocketAddr>().unwrap(),
            database_url: "postgresql://postgres@127.0.0.1:1/external_shortlink".to_owned(),
            api_token: "x".repeat(32),
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
    async fn cached_mapping_withholds_redirect_when_database_is_down() {
        let state = AppState::initialize(settings()).await.unwrap();
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
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    }

    #[tokio::test]
    async fn automated_head_requests_are_suppressed_without_recording_clicks() {
        let state = AppState::initialize(settings()).await.unwrap();
        state.cache.insert("abc1".to_owned(), Arc::new(link()));
        for (ua, ip) in [
            ("GoogleMessages", "192.0.2.1"),
            ("curl/8.0", "192.0.2.1"),
            ("Mozilla/5.0", "66.249.1.2"),
            ("Mozilla/5.0", "74.125.1.2"),
        ] {
            let response = router(state.clone())
                .oneshot(
                    HttpRequest::builder()
                        .method("HEAD")
                        .uri("/abc1")
                        .header(header::USER_AGENT, ua)
                        .header("x-forwarded-for", ip)
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
            assert!(!response.headers().contains_key(header::LOCATION));
            assert!(!response.headers().contains_key("refresh"));
            assert!(
                to_bytes(response.into_body(), 1024)
                    .await
                    .unwrap()
                    .is_empty()
            );
        }
        assert!(
            state
                .metrics
                .render()
                .contains("external_shortlink_suppressed_bot_redirects_total 4")
        );
    }

    #[tokio::test]
    async fn bot_requests_preserve_errors_when_database_is_down() {
        let state = AppState::initialize(settings()).await.unwrap();
        state.cache.insert("abc1".to_owned(), Arc::new(link()));
        for (path, status) in [
            ("/abc1", StatusCode::SERVICE_UNAVAILABLE),
            ("/invalid!", StatusCode::NOT_FOUND),
            ("/missing", StatusCode::SERVICE_UNAVAILABLE),
        ] {
            let response = router(state.clone())
                .oneshot(
                    HttpRequest::builder()
                        .uri(path)
                        .header(header::USER_AGENT, "Googlebot")
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), status);
            assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
            assert!(!response.headers().contains_key(header::LOCATION));
        }
        assert!(
            state
                .metrics
                .render()
                .contains("external_shortlink_suppressed_bot_redirects_total 0")
        );
    }

    #[tokio::test]
    async fn admin_endpoints_require_a_bearer_token() {
        let state = AppState::initialize(settings()).await.unwrap();
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

    #[tokio::test]
    async fn cached_mapping_answers_head_without_recording_a_click() {
        let state = AppState::initialize(settings()).await.unwrap();
        state.cache.insert("abc1".to_owned(), Arc::new(link()));
        let response = router(state)
            .oneshot(
                HttpRequest::builder()
                    .method("HEAD")
                    .uri("/abc1")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        // PostgreSQL is unreachable in this test, so answering 302 proves HEAD
        // did not attempt the durable click write that GET performs.
        assert_eq!(response.status(), StatusCode::FOUND);
        assert_eq!(
            response.headers()[header::LOCATION],
            "https://example.com/destination"
        );
    }

    #[test]
    fn omitted_click_limit_never_exceeds_the_configured_maximum() {
        assert_eq!(effective_click_limit(None, 100), 100);
        assert_eq!(effective_click_limit(None, 2_000), 1_000);
        assert_eq!(effective_click_limit(Some(500), 100), 500);
    }

    #[derive(Debug)]
    struct StubDatabaseError {
        code: &'static str,
        message: String,
    }

    impl std::fmt::Display for StubDatabaseError {
        fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
            formatter.write_str(&self.message)
        }
    }

    impl std::error::Error for StubDatabaseError {}

    impl sqlx::error::DatabaseError for StubDatabaseError {
        fn kind(&self) -> sqlx::error::ErrorKind {
            sqlx::error::ErrorKind::CheckViolation
        }

        fn message(&self) -> &str {
            &self.message
        }

        fn code(&self) -> Option<std::borrow::Cow<'_, str>> {
            Some(std::borrow::Cow::Borrowed(self.code))
        }

        fn as_error(&self) -> &(dyn std::error::Error + Send + Sync + 'static) {
            self
        }

        fn as_error_mut(&mut self) -> &mut (dyn std::error::Error + Send + Sync + 'static) {
            self
        }

        fn into_error(self: Box<Self>) -> Box<dyn std::error::Error + Send + Sync + 'static> {
            self
        }
    }

    fn stub_database_error(code: &'static str, message: &str) -> DatabaseError {
        DatabaseError::Sql(sqlx::Error::Database(Box::new(StubDatabaseError {
            code,
            message: message.to_owned(),
        })))
    }

    #[test]
    fn click_store_faults_are_named_and_actionable() {
        let missing_partition = click_storage_fault(&stub_database_error(
            MISSING_PARTITION_SQLSTATE,
            r#"no partition of relation "clicks_v2" found for row"#,
        ))
        .expect("a missing partition must be a named fault");
        assert_eq!(missing_partition.reason, "missing_partition");
        assert!(missing_partition.action.contains("deployment schema"));

        let denied = click_storage_fault(&stub_database_error(
            INSUFFICIENT_PRIVILEGE_SQLSTATE,
            r#"permission denied for table clicks_v2"#,
        ))
        .expect("a privilege failure must be a named fault");
        assert_eq!(denied.reason, "permission_denied");
        assert!(denied.action.contains("grants"));

        // Every other database problem stays a generic persistence failure.
        assert!(
            click_storage_fault(&stub_database_error(
                MISSING_PARTITION_SQLSTATE,
                r#"new row for relation "clicks_v2" violates check constraint "clicks_http_destination""#
            ))
            .is_none()
        );
        assert!(
            click_storage_fault(&stub_database_error(
                "57014",
                "canceling statement due to statement timeout"
            ))
            .is_none()
        );
        assert!(click_storage_fault(&DatabaseError::UnknownAcknowledgement).is_none());
        assert!(click_storage_fault(&DatabaseError::Sql(sqlx::Error::PoolTimedOut)).is_none());
    }
}
