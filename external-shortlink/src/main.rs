use anyhow::Result;
use tracing_subscriber::EnvFilter;

use external_shortlink::{
    config::Settings,
    server::{AppState, serve},
};

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .with_target(false)
        .compact()
        .init();
    if let Err(error) = run().await {
        tracing::error!(error = %error, "external short-link service terminated unexpectedly");
        return Err(error);
    }
    Ok(())
}

async fn run() -> Result<()> {
    let settings = Settings::from_env()?;
    let state = AppState::initialize(settings).await?;
    serve(state).await
}
