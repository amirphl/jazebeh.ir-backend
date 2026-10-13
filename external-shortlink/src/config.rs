use std::{env, net::SocketAddr, time::Duration};

use anyhow::{Context, Result, bail};

/// A click is only redirected after it is durably committed, and every click
/// write serializes behind the advisory lock that keeps the import cursor in
/// ID order. A budget below a plausible lock wait plus WAL flush turns ordinary
/// concurrency into `503`s, so the built-in default matches the deployed
/// environment (see `deploy/external-shortlink.env.example` and
/// `scripts/deploy-debian-12.sh`).
pub const CLICK_INSERT_TIMEOUT_SECONDS: f64 = 2.0;
pub const MINIMUM_CLICK_INSERT_TIMEOUT_SECONDS: f64 = CLICK_INSERT_TIMEOUT_SECONDS;

#[derive(Debug, Clone)]
pub struct Settings {
    pub bind_addr: SocketAddr,
    pub database_url: String,
    pub api_token: String,
    pub pool_min_size: u32,
    pub pool_max_size: u32,
    pub db_command_timeout: Duration,
    pub db_lock_timeout: Duration,
    pub click_insert_timeout: Duration,
    pub link_lookup_timeout: Duration,
    pub cache_max_entries: u64,
    pub cache_preload_entries: i64,
    pub admin_batch_max_links: usize,
    pub click_fetch_max_limit: i64,
    pub max_admin_body_bytes: usize,
    pub acknowledged_retention_days: i64,
    pub purge_interval: Duration,
}

impl Settings {
    pub fn from_env() -> Result<Self> {
        let database_url = required("EXTERNAL_SHORTLINK_DATABASE_URL")?;
        let api_token = required("EXTERNAL_SHORTLINK_API_TOKEN")?;
        if !is_url_safe_secret(&api_token) {
            bail!("EXTERNAL_SHORTLINK_API_TOKEN must be a 32+ character URL-safe secret")
        }

        let bind_addr: SocketAddr = value("EXTERNAL_SHORTLINK_BIND_ADDR", "127.0.0.1:8081")
            .parse()
            .context("EXTERNAL_SHORTLINK_BIND_ADDR must be an IP address and port")?;
        if !bind_addr.ip().is_loopback() {
            bail!(
                "EXTERNAL_SHORTLINK_BIND_ADDR must use a loopback address; Nginx is the only supported public entry point"
            )
        }

        let pool_min_size = u32::try_from(unsigned("EXTERNAL_SHORTLINK_POOL_MIN_SIZE", 1, 0, 16)?)?;
        let pool_max_size =
            u32::try_from(unsigned("EXTERNAL_SHORTLINK_POOL_MAX_SIZE", 32, 1, 32)?)?;
        if pool_min_size > pool_max_size {
            bail!("EXTERNAL_SHORTLINK_POOL_MIN_SIZE cannot exceed EXTERNAL_SHORTLINK_POOL_MAX_SIZE")
        }
        let cache_max_entries =
            unsigned("EXTERNAL_SHORTLINK_CACHE_MAX_ENTRIES", 50_000, 1, 100_000)?;
        let cache_preload_entries = unsigned(
            "EXTERNAL_SHORTLINK_CACHE_PRELOAD_ENTRIES",
            25_000,
            0,
            100_000,
        )?;
        if cache_preload_entries > cache_max_entries {
            bail!(
                "EXTERNAL_SHORTLINK_CACHE_PRELOAD_ENTRIES cannot exceed EXTERNAL_SHORTLINK_CACHE_MAX_ENTRIES"
            )
        }

        Ok(Self {
            bind_addr,
            database_url,
            api_token,
            pool_min_size,
            pool_max_size,
            db_command_timeout: seconds(
                "EXTERNAL_SHORTLINK_DB_COMMAND_TIMEOUT_SECONDS",
                15.0,
                0.01,
            )?,
            db_lock_timeout: seconds("EXTERNAL_SHORTLINK_DB_LOCK_TIMEOUT_SECONDS", 10.0, 0.01)?,
            click_insert_timeout: seconds(
                "EXTERNAL_SHORTLINK_CLICK_INSERT_TIMEOUT_SECONDS",
                CLICK_INSERT_TIMEOUT_SECONDS,
                MINIMUM_CLICK_INSERT_TIMEOUT_SECONDS,
            )?,
            link_lookup_timeout: seconds(
                "EXTERNAL_SHORTLINK_LINK_LOOKUP_TIMEOUT_SECONDS",
                5.0,
                0.001,
            )?,
            cache_max_entries,
            cache_preload_entries: i64::try_from(cache_preload_entries)?,
            admin_batch_max_links: usize::try_from(unsigned(
                "EXTERNAL_SHORTLINK_ADMIN_BATCH_MAX_LINKS",
                500,
                1,
                1_000,
            )?)?,
            click_fetch_max_limit: i64::try_from(unsigned(
                "EXTERNAL_SHORTLINK_CLICK_FETCH_MAX_LIMIT",
                2_000,
                1,
                5_000,
            )?)?,
            max_admin_body_bytes: usize::try_from(unsigned(
                "EXTERNAL_SHORTLINK_MAX_ADMIN_BODY_BYTES",
                16 * 1024 * 1024,
                1,
                16 * 1024 * 1024,
            )?)?,
            acknowledged_retention_days: i64::try_from(unsigned(
                "EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS",
                7,
                1,
                365,
            )?)?,
            purge_interval: seconds("EXTERNAL_SHORTLINK_PURGE_INTERVAL_SECONDS", 3600.0, 1.0)?,
        })
    }
}

fn is_url_safe_secret(value: &str) -> bool {
    value.len() >= 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'~' | b'-'))
}

fn value(name: &str, default: &str) -> String {
    env::var(name)
        .unwrap_or_else(|_| default.to_owned())
        .trim()
        .to_owned()
}

fn required(name: &str) -> Result<String> {
    let value = value(name, "");
    if value.is_empty() {
        bail!("{name} is required")
    }
    Ok(value)
}

fn unsigned(name: &str, default: u64, minimum: u64, maximum: u64) -> Result<u64> {
    let raw = value(name, &default.to_string());
    let parsed = raw
        .parse::<u64>()
        .with_context(|| format!("{name} must be an unsigned integer"))?;
    if parsed < minimum {
        bail!("{name} must be at least {minimum}")
    }
    if parsed > maximum {
        bail!("{name} must not exceed {maximum}")
    }
    Ok(parsed)
}

fn seconds(name: &str, default: f64, minimum: f64) -> Result<Duration> {
    let raw = value(name, &default.to_string());
    let parsed = raw
        .parse::<f64>()
        .with_context(|| format!("{name} must be numeric"))?;
    if !parsed.is_finite() || parsed < minimum {
        bail!("{name} must be at least {minimum}")
    }
    Ok(Duration::from_secs_f64(parsed))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn seconds_rejects_non_finite_values() {
        assert!(seconds("EXTERNAL_SHORTLINK_TEST_SECONDS", f64::NAN, 0.0).is_err());
    }

    #[test]
    fn unsigned_enforces_upper_bound() {
        assert!(unsigned("EXTERNAL_SHORTLINK_TEST_UNSIGNED", 10, 1, 5).is_err());
    }

    #[test]
    fn url_safe_secret_rejects_environment_file_syntax() {
        assert!(is_url_safe_secret(&"x".repeat(32)));
        assert!(!is_url_safe_secret(&("x".repeat(31) + "!")));
    }

    #[test]
    fn click_insert_timeout_default_matches_deployed_configuration() {
        let deployed = format!(
            "EXTERNAL_SHORTLINK_CLICK_INSERT_TIMEOUT_SECONDS={CLICK_INSERT_TIMEOUT_SECONDS}"
        );
        assert!(
            include_str!("../deploy/external-shortlink.env.example").contains(&deployed),
            "the example environment file must not drift from the built-in click timeout default"
        );
        assert!(
            include_str!("../scripts/deploy-debian-12.sh").contains(&format!("'{deployed}'")),
            "the deployment script must not drift from the built-in click timeout default"
        );
    }

    #[test]
    fn click_insert_timeout_rejects_an_unsafe_override() {
        assert!(
            seconds(
                "EXTERNAL_SHORTLINK_TEST_UNSET_CLICK_INSERT_TIMEOUT_SECONDS",
                0.100,
                MINIMUM_CLICK_INSERT_TIMEOUT_SECONDS,
            )
            .is_err()
        );
    }
}
