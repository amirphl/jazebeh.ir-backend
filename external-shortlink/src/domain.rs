use std::collections::HashMap;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use url::Url;
use uuid::Uuid;

pub const RESERVED_CODES: [&str; 4] = ["api", "healthz", "readyz", "metrics"];

#[derive(Debug, Clone, Deserialize)]
pub struct LinkUploadRequest {
    pub links: Vec<LinkInput>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct LinkInput {
    pub code: String,
    pub long_url: String,
    #[serde(default)]
    pub short_url: Option<String>,
    #[serde(default)]
    pub source_link_id: Option<i64>,
    #[serde(default)]
    pub campaign_id: Option<i64>,
    #[serde(default)]
    pub client_id: Option<i64>,
    #[serde(default)]
    pub scenario_id: Option<i64>,
    #[serde(default)]
    pub scenario_name: Option<String>,
    #[serde(default)]
    pub phone_number: Option<String>,
    #[serde(default)]
    pub is_test: bool,
    #[serde(default)]
    pub source_created_at: Option<DateTime<Utc>>,
    #[serde(default)]
    pub source_updated_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct LinkRecord {
    pub link_id: i64,
    pub code: String,
    pub long_url: String,
    pub short_url: Option<String>,
    pub source_link_id: Option<i64>,
    pub campaign_id: Option<i64>,
    pub client_id: Option<i64>,
    pub scenario_id: Option<i64>,
    pub scenario_name: Option<String>,
    pub phone_number: Option<String>,
    pub is_test: bool,
    pub source_created_at: Option<DateTime<Utc>>,
    pub source_updated_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct ClickEvent {
    pub event_id: Uuid,
    pub short_code: String,
    pub link_id: i64,
    pub long_url: String,
    pub short_url: Option<String>,
    pub source_link_id: Option<i64>,
    pub campaign_id: Option<i64>,
    pub client_id: Option<i64>,
    pub scenario_id: Option<i64>,
    pub scenario_name: Option<String>,
    pub phone_number: Option<String>,
    pub is_test: bool,
    pub link_created_at: Option<DateTime<Utc>>,
    pub link_updated_at: Option<DateTime<Utc>>,
    pub clicked_at: DateTime<Utc>,
    pub client_ip: Option<String>,
    pub user_agent: Option<String>,
    pub referer: Option<String>,
}

#[derive(Debug, Clone, Serialize, sqlx::FromRow)]
pub struct ClickRecord {
    pub click_id: i64,
    #[sqlx(flatten)]
    #[serde(flatten)]
    pub event: ClickEvent,
}

impl LinkInput {
    pub fn validate_and_normalize(mut self) -> Result<Self, ValidationError> {
        if !is_valid_code(&self.code) {
            return Err(ValidationError::new(
                "code must be 1-64 URL-safe characters and must not be reserved",
            ));
        }
        if self.long_url.len() > 4096 || self.long_url.chars().any(char::is_whitespace) {
            return Err(ValidationError::new("long_url is invalid or too long"));
        }
        self.long_url = normalize_long_url(&self.long_url)?;

        self.short_url = clean_optional(self.short_url, "short_url", 4096)?;
        self.scenario_name = clean_optional(self.scenario_name, "scenario_name", 512)?;
        self.phone_number = clean_optional(self.phone_number, "phone_number", 32)?;
        if self.source_link_id.is_some_and(|id| id <= 0) {
            return Err(ValidationError::new(
                "source_link_id must be a positive 64-bit integer or null",
            ));
        }
        for (name, value) in [
            ("campaign_id", self.campaign_id),
            ("client_id", self.client_id),
            ("scenario_id", self.scenario_id),
        ] {
            if value.is_some_and(|id| id < 0) {
                return Err(ValidationError::new(format!(
                    "{name} must be a non-negative 64-bit integer or null"
                )));
            }
        }
        Ok(self)
    }
}

// The production service keeps the user-supplied destination unchanged. The
// redirect service stores an absolute HTTP(S) URL, adding HTTPS only when the
// supplied value has no scheme, so browsers can follow it correctly.
fn normalize_long_url(long_url: &str) -> Result<String, ValidationError> {
    let parsed = Url::parse(long_url);
    let normalized = match parsed {
        Ok(url) if matches!(url.scheme(), "http" | "https") && url.host_str().is_some() => {
            long_url.to_owned()
        }
        Ok(_) => {
            return Err(ValidationError::new(
                "long_url must be an absolute http:// or https:// URL",
            ));
        }
        Err(url::ParseError::RelativeUrlWithoutBase) if long_url.starts_with("//") => {
            format!("https:{long_url}")
        }
        Err(url::ParseError::RelativeUrlWithoutBase) => format!("https://{long_url}"),
        Err(_) => {
            return Err(ValidationError::new(
                "long_url must be an absolute http:// or https:// URL",
            ));
        }
    };
    if normalized.len() > 4096 {
        return Err(ValidationError::new("long_url is invalid or too long"));
    }
    let url = Url::parse(&normalized).map_err(|_| {
        ValidationError::new("long_url must be an absolute http:// or https:// URL")
    })?;
    if !matches!(url.scheme(), "http" | "https") || url.host_str().is_none() {
        return Err(ValidationError::new(
            "long_url must be an absolute http:// or https:// URL",
        ));
    }
    Ok(normalized)
}

pub fn unique_validated_links(inputs: Vec<LinkInput>) -> Result<Vec<LinkInput>, ValidationError> {
    let mut unique: HashMap<String, LinkInput> = HashMap::with_capacity(inputs.len());
    for input in inputs {
        let input = input.validate_and_normalize()?;
        if let Some(previous) = unique.get(&input.code) {
            if previous.long_url != input.long_url {
                return Err(ValidationError::new(format!(
                    "code {:?} appears with different destinations",
                    input.code
                )));
            }
        }
        unique.insert(input.code.clone(), input);
    }
    Ok(unique.into_values().collect())
}

pub fn is_valid_code(code: &str) -> bool {
    !code.is_empty()
        && code.len() <= 64
        && code
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
        && !RESERVED_CODES
            .iter()
            .any(|reserved| code.eq_ignore_ascii_case(reserved))
}

pub fn click_from_link(
    link: &LinkRecord,
    client_ip: Option<String>,
    user_agent: Option<String>,
    referer: Option<String>,
) -> ClickEvent {
    ClickEvent {
        event_id: Uuid::new_v4(),
        short_code: link.code.clone(),
        link_id: link.link_id,
        long_url: link.long_url.clone(),
        short_url: link.short_url.clone(),
        source_link_id: link.source_link_id,
        campaign_id: link.campaign_id,
        client_id: link.client_id,
        scenario_id: link.scenario_id,
        scenario_name: link.scenario_name.clone(),
        phone_number: link.phone_number.clone(),
        is_test: link.is_test,
        link_created_at: link.source_created_at,
        link_updated_at: link.source_updated_at,
        clicked_at: Utc::now(),
        client_ip,
        user_agent,
        referer,
    }
}

fn clean_optional(
    value: Option<String>,
    name: &str,
    maximum: usize,
) -> Result<Option<String>, ValidationError> {
    let Some(value) = value else {
        return Ok(None);
    };
    let cleaned = value.trim();
    if cleaned.is_empty() {
        return Ok(None);
    }
    if cleaned.len() > maximum || cleaned.chars().any(|character| character.is_control()) {
        return Err(ValidationError::new(format!(
            "{name} is invalid or too long"
        )));
    }
    Ok(Some(cleaned.to_owned()))
}

#[derive(Debug, Clone)]
pub struct ValidationError(String);

impl ValidationError {
    fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }
}

impl std::fmt::Display for ValidationError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        self.0.fmt(formatter)
    }
}

impl std::error::Error for ValidationError {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_codes_exclude_reserved_paths() {
        assert!(is_valid_code("campaign_1-A"));
        assert!(!is_valid_code("healthz"));
        assert!(!is_valid_code("Api"));
        assert!(!is_valid_code("contains/slash"));
    }

    #[test]
    fn duplicate_code_with_different_destination_is_rejected() {
        let links = vec![
            LinkInput {
                code: "same".into(),
                long_url: "https://example.com/a".into(),
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
            },
            LinkInput {
                code: "same".into(),
                long_url: "https://example.com/b".into(),
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
            },
        ];
        assert!(unique_validated_links(links).is_err());
    }

    #[test]
    fn source_link_id_must_be_positive() {
        let link = LinkInput {
            code: "campaign-1".into(),
            long_url: "https://example.com/offer".into(),
            short_url: None,
            source_link_id: Some(0),
            campaign_id: None,
            client_id: None,
            scenario_id: None,
            scenario_name: None,
            phone_number: None,
            is_test: false,
            source_created_at: None,
            source_updated_at: None,
        };
        assert!(link.validate_and_normalize().is_err());
    }

    #[test]
    fn scheme_less_destination_is_stored_as_https() {
        let link = LinkInput {
            code: "campaign-1".into(),
            long_url: "example.com/offer".into(),
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
        let validated = link.validate_and_normalize().unwrap();
        assert_eq!(validated.long_url, "https://example.com/offer");
    }

    #[test]
    fn protocol_relative_destination_uses_https() {
        let link = LinkInput {
            code: "campaign-1".into(),
            long_url: "//example.com/offer".into(),
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
        let validated = link.validate_and_normalize().unwrap();
        assert_eq!(validated.long_url, "https://example.com/offer");
    }

    #[test]
    fn click_events_preserve_test_mapping_status() {
        let link = LinkRecord {
            link_id: 1,
            code: "test1".into(),
            long_url: "https://example.com/offer".into(),
            short_url: Some("https://jzbe.ir/test1".into()),
            source_link_id: None,
            campaign_id: Some(1),
            client_id: None,
            scenario_id: None,
            scenario_name: None,
            phone_number: None,
            is_test: true,
            source_created_at: None,
            source_updated_at: None,
        };
        assert!(click_from_link(&link, None, None, None).is_test);
    }
}
