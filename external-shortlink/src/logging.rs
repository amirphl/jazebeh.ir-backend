//! Service-wide structured logging.
//!
//! Logs deliberately contain operational identifiers and counts, but never
//! bearer tokens, destinations, client IP addresses, user agents, referers,
//! phone numbers, or click payloads.  Nginx uses the same request ID and JSON
//! field names so a failed request can be followed across the proxy and app.
use tracing_subscriber::EnvFilter;

pub const COMPONENT: &str = "external_shortlink";

pub fn init() {
    tracing_subscriber::fmt()
        .json()
        .flatten_event(true)
        .with_current_span(false)
        .with_span_list(false)
        .with_target(false)
        .with_ansi(false)
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .init();
}
