use anyhow::Result;
use external_shortlink::{
    config::Settings,
    logging,
    server::{AppState, serve},
};

#[tokio::main]
async fn main() -> Result<()> {
    logging::init();
    let settings = Settings::from_env().map_err(|error| {
        tracing::error!(component = logging::COMPONENT, event = "configuration_invalid", error = %error, "service configuration is invalid");
        error
    })?;
    tracing::info!(
        component = logging::COMPONENT,
        event = "service_starting",
        bind_address = %settings.bind_addr,
        pool_max_size = settings.pool_max_size,
        cache_max_entries = settings.cache_max_entries,
        spool_max_events = settings.spool_max_events,
        spool_max_bytes = settings.spool_max_bytes,
        "starting external short-link service"
    );
    let state = AppState::initialize(settings).await.map_err(|error| {
        tracing::error!(component = logging::COMPONENT, event = "initialization_failed", error = %error, "service initialization failed");
        error
    })?;
    serve(state).await.map_err(|error| {
        tracing::error!(component = logging::COMPONENT, event = "service_stopped_unexpectedly", error = %error, "external short-link service stopped unexpectedly");
        error
    })
}
