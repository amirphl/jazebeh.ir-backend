use std::{
    collections::BTreeMap,
    fmt::Write,
    sync::{
        Mutex,
        atomic::{AtomicI64, AtomicU64, Ordering},
    },
};

const LATENCY_BUCKETS: [f64; 10] = [0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0];

#[derive(Default)]
struct LabelledCounter {
    values: Mutex<BTreeMap<String, u64>>,
}

impl LabelledCounter {
    fn increment(&self, label: impl Into<String>) {
        let mut values = self.values.lock().expect("metrics lock is not poisoned");
        *values.entry(label.into()).or_default() += 1;
    }

    fn render(&self, output: &mut String, metric: &str, label_name: &str) {
        let values = self.values.lock().expect("metrics lock is not poisoned");
        for (label, value) in values.iter() {
            let _ = writeln!(
                output,
                r#"{metric}{{{label_name}="{}"}} {value}"#,
                escape_label(label)
            );
        }
    }
}

#[derive(Default)]
struct Histogram {
    buckets: Vec<AtomicU64>,
    count: AtomicU64,
    sum_nanoseconds: AtomicU64,
}

impl Histogram {
    fn new() -> Self {
        Self {
            buckets: (0..LATENCY_BUCKETS.len())
                .map(|_| AtomicU64::new(0))
                .collect(),
            count: AtomicU64::new(0),
            sum_nanoseconds: AtomicU64::new(0),
        }
    }

    fn observe(&self, seconds: f64) {
        self.count.fetch_add(1, Ordering::Relaxed);
        self.sum_nanoseconds.fetch_add(
            (seconds.max(0.0) * 1_000_000_000.0).min(u64::MAX as f64) as u64,
            Ordering::Relaxed,
        );
        for (index, upper_bound) in LATENCY_BUCKETS.iter().enumerate() {
            if seconds <= *upper_bound {
                self.buckets[index].fetch_add(1, Ordering::Relaxed);
            }
        }
    }

    fn render(&self, output: &mut String, metric: &str) {
        for (index, upper_bound) in LATENCY_BUCKETS.iter().enumerate() {
            let count = self.buckets[index].load(Ordering::Relaxed);
            let _ = writeln!(output, r#"{metric}_bucket{{le="{upper_bound}"}} {count}"#);
        }
        let count = self.count.load(Ordering::Relaxed);
        let _ = writeln!(output, r#"{metric}_bucket{{le="+Inf"}} {count}"#);
        let _ = writeln!(
            output,
            "{metric}_sum {}",
            self.sum_nanoseconds.load(Ordering::Relaxed) as f64 / 1e9
        );
        let _ = writeln!(output, "{metric}_count {count}");
    }
}

pub struct Metrics {
    redirects: LabelledCounter,
    redirect_latency: Histogram,
    unknown_codes: AtomicU64,
    suppressed_bot_redirects: AtomicU64,
    postgres_errors: LabelledCounter,
    pool_timeouts: AtomicU64,
    spool_events: AtomicI64,
    spool_bytes: AtomicI64,
    spool_disk_bytes: AtomicI64,
    spool_oldest_age: AtomicI64,
    spool_dead_letter_events: AtomicI64,
    spool_dropped: LabelledCounter,
    mapping_uploads: LabelledCounter,
    last_mapping_upload: AtomicI64,
    last_ack_id: AtomicI64,
    database_bytes: AtomicI64,
    cache_lookups: LabelledCounter,
}

impl Default for Metrics {
    fn default() -> Self {
        Self {
            redirects: LabelledCounter::default(),
            redirect_latency: Histogram::new(),
            unknown_codes: AtomicU64::new(0),
            suppressed_bot_redirects: AtomicU64::new(0),
            postgres_errors: LabelledCounter::default(),
            pool_timeouts: AtomicU64::new(0),
            spool_events: AtomicI64::new(0),
            spool_bytes: AtomicI64::new(0),
            spool_disk_bytes: AtomicI64::new(0),
            spool_oldest_age: AtomicI64::new(0),
            spool_dead_letter_events: AtomicI64::new(0),
            spool_dropped: LabelledCounter::default(),
            mapping_uploads: LabelledCounter::default(),
            last_mapping_upload: AtomicI64::new(0),
            last_ack_id: AtomicI64::new(0),
            database_bytes: AtomicI64::new(0),
            cache_lookups: LabelledCounter::default(),
        }
    }
}

impl Metrics {
    pub fn redirect(&self, status: u16, elapsed_seconds: f64) {
        self.redirects.increment(status.to_string());
        self.redirect_latency.observe(elapsed_seconds);
    }

    pub fn suppressed_bot_redirect(&self) {
        self.suppressed_bot_redirects
            .fetch_add(1, Ordering::Relaxed);
    }

    pub fn unknown_code(&self) {
        self.unknown_codes.fetch_add(1, Ordering::Relaxed);
    }

    pub fn postgres_error(&self, operation: &str) {
        self.postgres_errors.increment(operation);
    }

    pub fn pool_timeout(&self) {
        self.pool_timeouts.fetch_add(1, Ordering::Relaxed);
    }

    pub fn spool_dropped(&self, reason: &str) {
        self.spool_dropped.increment(reason);
    }

    pub fn mapping_upload(&self, result: &str) {
        self.mapping_uploads.increment(result);
    }

    pub fn mapping_upload_succeeded(&self) {
        self.last_mapping_upload
            .store(chrono::Utc::now().timestamp(), Ordering::Relaxed);
    }

    pub fn acknowledged_through(&self, click_id: i64) {
        self.last_ack_id.store(click_id, Ordering::Relaxed);
    }

    pub fn cache_lookup(&self, result: &str) {
        self.cache_lookups.increment(result);
    }

    pub fn spool_stats(
        &self,
        events: u64,
        queued_bytes: u64,
        disk_bytes: u64,
        oldest_age_seconds: f64,
        dead_letter_events: u64,
    ) {
        self.spool_events
            .store(events.min(i64::MAX as u64) as i64, Ordering::Relaxed);
        self.spool_bytes
            .store(queued_bytes.min(i64::MAX as u64) as i64, Ordering::Relaxed);
        self.spool_disk_bytes
            .store(disk_bytes.min(i64::MAX as u64) as i64, Ordering::Relaxed);
        self.spool_oldest_age.store(
            oldest_age_seconds.max(0.0).min(i64::MAX as f64) as i64,
            Ordering::Relaxed,
        );
        self.spool_dead_letter_events.store(
            dead_letter_events.min(i64::MAX as u64) as i64,
            Ordering::Relaxed,
        );
    }

    pub fn database_size(&self, bytes: i64) {
        self.database_bytes.store(bytes.max(0), Ordering::Relaxed);
    }

    pub fn render(&self) -> String {
        let mut output = String::with_capacity(4096);
        emit_atomic_counter(
            &mut output,
            "external_shortlink_suppressed_bot_redirects_total",
            "Tracked automated visits returned without redirecting",
            &self.suppressed_bot_redirects,
        );
        emit_help_type(
            &mut output,
            "external_shortlink_redirect_requests_total",
            "Redirect requests",
            "counter",
        );
        self.redirects.render(
            &mut output,
            "external_shortlink_redirect_requests_total",
            "status",
        );
        emit_help_type(
            &mut output,
            "external_shortlink_redirect_latency_seconds",
            "End-to-end redirect handler latency",
            "histogram",
        );
        self.redirect_latency
            .render(&mut output, "external_shortlink_redirect_latency_seconds");
        emit_atomic_counter(
            &mut output,
            "external_shortlink_unknown_codes_total",
            "Unknown short-code requests",
            &self.unknown_codes,
        );
        emit_help_type(
            &mut output,
            "external_shortlink_postgres_errors_total",
            "PostgreSQL operation failures",
            "counter",
        );
        self.postgres_errors.render(
            &mut output,
            "external_shortlink_postgres_errors_total",
            "operation",
        );
        emit_atomic_counter(
            &mut output,
            "external_shortlink_postgres_pool_timeouts_total",
            "PostgreSQL pool or operation timeouts",
            &self.pool_timeouts,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_spool_events",
            "Events awaiting PostgreSQL replay",
            &self.spool_events,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_spool_bytes",
            "Logical bytes occupied by click events awaiting PostgreSQL replay",
            &self.spool_bytes,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_spool_disk_bytes",
            "Physical bytes used by the durable click spool and its SQLite sidecar files",
            &self.spool_disk_bytes,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_spool_oldest_event_age_seconds",
            "Age of the oldest spooled click",
            &self.spool_oldest_age,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_spool_dead_letter_events",
            "Corrupt spool records quarantined to preserve replay progress",
            &self.spool_dead_letter_events,
        );
        emit_help_type(
            &mut output,
            "external_shortlink_spool_rejected_total",
            "Clicks that could not be durably spooled",
            "counter",
        );
        self.spool_dropped.render(
            &mut output,
            "external_shortlink_spool_rejected_total",
            "reason",
        );
        emit_help_type(
            &mut output,
            "external_shortlink_mapping_upload_total",
            "Mapping upload requests",
            "counter",
        );
        self.mapping_uploads.render(
            &mut output,
            "external_shortlink_mapping_upload_total",
            "result",
        );
        emit_gauge(
            &mut output,
            "external_shortlink_last_successful_mapping_upload_timestamp_seconds",
            "Unix time of the last successful mapping upload",
            &self.last_mapping_upload,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_last_acknowledged_click_id",
            "Highest production-acknowledged click ID",
            &self.last_ack_id,
        );
        emit_gauge(
            &mut output,
            "external_shortlink_database_size_bytes",
            "Current PostgreSQL database size",
            &self.database_bytes,
        );
        emit_help_type(
            &mut output,
            "external_shortlink_cache_lookups_total",
            "Link-cache lookups",
            "counter",
        );
        self.cache_lookups.render(
            &mut output,
            "external_shortlink_cache_lookups_total",
            "result",
        );
        output
    }
}

fn emit_help_type(output: &mut String, metric: &str, help: &str, metric_type: &str) {
    let _ = writeln!(output, "# HELP {metric} {help}");
    let _ = writeln!(output, "# TYPE {metric} {metric_type}");
}

fn emit_atomic_counter(output: &mut String, metric: &str, help: &str, value: &AtomicU64) {
    emit_help_type(output, metric, help, "counter");
    let _ = writeln!(output, "{metric} {}", value.load(Ordering::Relaxed));
}

fn emit_gauge(output: &mut String, metric: &str, help: &str, value: &AtomicI64) {
    emit_help_type(output, metric, help, "gauge");
    let _ = writeln!(output, "{metric} {}", value.load(Ordering::Relaxed));
}

fn escape_label(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\n', "\\n")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn metrics_are_prometheus_text() {
        let metrics = Metrics::default();
        metrics.redirect(302, 0.004);
        let text = metrics.render();
        assert!(text.contains("external_shortlink_redirect_requests_total{status=\"302\"} 1"));
        assert!(
            text.contains("external_shortlink_redirect_latency_seconds_bucket{le=\"0.005\"} 1")
        );
    }
}
