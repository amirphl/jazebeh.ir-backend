use std::net::Ipv4Addr;

const USER_AGENT_PATTERN: &str = "bot|crawler|spider|facebookexternalhit|facebot|twitterbot|googlemessages|telegrambot|whatsapp|preview|linkpreview|curl|wget|python-requests|okhttp|postman|v2raytun";

// Keep these explicit signals aligned with utils.AutomatedClickUserAgentPattern.
// The reporting-only Linux Chrome heuristic and test-link flag are not used here.
pub fn is_automated_request(user_agent: &str, client_ip: &str) -> bool {
    let ua = user_agent.to_ascii_lowercase();
    if USER_AGENT_PATTERN
        .split('|')
        .any(|token| ua.contains(token))
    {
        return true;
    }
    match client_ip.parse::<Ipv4Addr>() {
        Ok(ip) => matches!(
            ip.octets(),
            [66, 102, _, _] | [66, 249, _, _] | [74, 125, _, _] | [192, 178, _, _]
        ),
        Err(_) => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[derive(serde::Deserialize)]
    struct Case {
        name: String,
        ua: String,
        ip: String,
        bot: bool,
    }
    #[test]
    fn shared_classification_cases() {
        let cases: Vec<Case> =
            serde_json::from_str(include_str!("../tests/fixtures/traffic.json")).unwrap();
        for case in cases {
            assert_eq!(
                is_automated_request(&case.ua, &case.ip),
                case.bot,
                "{}",
                case.name
            );
        }
    }
}
