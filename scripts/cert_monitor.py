"""
Daily SSL certificate watcher for beta environment.
- Parses nginx config(s) to find certificate paths (ssl_certificate directives).
- Supports multiple conf files and includes; expands ${VAR} using environment.
- Checks expiration dates; if expired or within threshold days, sends SMS alert.

Environment variables:
  CERT_ALERT_PHONE              (required) target phone number, e.g. 98912xxxxxxx
  CERT_ALERT_THRESHOLD_DAYS     (default: 7)
  CERT_ALERT_CONF_PATH          (default: /etc/nginx/sites-available/yamata-beta.conf)
  CERT_ALERT_CONF_GLOBS         (default: "/etc/nginx/sites-enabled/*.conf,/etc/nginx/conf.d/*.conf")
  CERT_ALERT_CERT_PATHS         optional, comma-separated extra cert paths
  CERT_ALERT_LOG_LEVEL          (default: INFO)
  SMS_PROVIDER_DOMAIN           (required) provider selector ("payamsms") or
                                generic SMS provider host (no scheme)
  SMS_API_KEY                   required for generic SMS providers
  SMS_SOURCE_NUMBER             (required) sender number
  SMS_RETRY_COUNT               (default: 3)
  SMS_VALIDITY_PERIOD           (default: 300) seconds
  SMS_TIMEOUT                   (default: 30s) simple duration (30s/5m/2h) or seconds
  PAYAM_SMS_TOKEN_URL           PayamSMS OAuth endpoint
  PAYAM_SMS_SEND_URL            PayamSMS send endpoint
  PAYAM_SMS_SYSTEM_NAME         required when SMS_PROVIDER_DOMAIN=payamsms
  PAYAM_SMS_USERNAME            required when SMS_PROVIDER_DOMAIN=payamsms
  PAYAM_SMS_PASSWORD            required when SMS_PROVIDER_DOMAIN=payamsms
  PAYAM_SMS_SCOPE               (default: webservice)
  PAYAM_SMS_GRANT_TYPE          (default: password)
  PAYAM_SMS_ROOT_ACCESS_TOKEN   optional Basic token for the OAuth request
  CERT_ALERT_RETRY_INTERVAL     (default: 5m) retry delay after transient failures
  DOMAIN / API_DOMAIN / ...     used for ${VAR} expansion inside the nginx conf paths

Run forever: performs a check on start, then every 24h.
"""

from __future__ import annotations

import glob
import logging
import os
import re
import time
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlparse

import requests
from cryptography import x509
from cryptography.hazmat.backends import default_backend

# ---------- Helpers ----------


def _parse_timedelta(s: str) -> float:
    """Parse simple duration strings like '30s', '5m', '2h'. Return seconds."""
    if not s:
        return 30.0
    m = re.match(r"^(\d+)([smhd])$", s.strip(), re.IGNORECASE)
    if not m:
        # fallback: try float seconds
        try:
            seconds = float(s)
        except ValueError as exc:
            raise ValueError(f"invalid duration: {s!r}") from exc
        if seconds <= 0:
            raise ValueError("duration must be greater than zero")
        return seconds
    value, unit = int(m.group(1)), m.group(2).lower()
    mult = {"s": 1, "m": 60, "h": 3600, "d": 86400}[unit]
    seconds = float(value * mult)
    if seconds <= 0:
        raise ValueError("duration must be greater than zero")
    return seconds


def load_cert_expiry(p: Path) -> datetime:
    data = p.read_bytes()
    # Take first certificate in the bundle
    pem_blocks = re.split(b"(?=-----BEGIN CERTIFICATE-----)", data)
    for block in pem_blocks:
        block = block.strip()
        if not block:
            continue
        cert = x509.load_pem_x509_certificate(block, default_backend())
        # Prefer timezone-aware API if available to avoid deprecation warnings
        if hasattr(cert, "not_valid_after_utc"):
            return cert.not_valid_after_utc
        return cert.not_valid_after.replace(tzinfo=timezone.utc)
    raise ValueError(f"No certificate found in {p}")


def find_cert_paths(
    conf_path: Path,
    env_map: dict[str, str],
    _depth: int = 0,
    _seen: set[Path] | None = None,
) -> list[Path]:
    if _depth > 10:
        return []
    if _seen is None:
        _seen = set()
    canonical = conf_path.resolve() if conf_path.exists() else conf_path
    if canonical in _seen:
        return []
    _seen.add(canonical)
    paths: list[Path] = []
    if conf_path.exists():
        text = conf_path.read_text()
        for match in re.finditer(r"ssl_certificate\s+([^;]+);", text):
            raw = match.group(1).strip()
            expanded = os.path.expandvars(_expand_env(raw, env_map))
            paths.append(Path(expanded))
        for match in re.finditer(r"include\s+([^;]+);", text):
            inc_raw = match.group(1).strip()
            inc_expanded = os.path.expandvars(_expand_env(inc_raw, env_map))
            for inc_path in glob.glob(inc_expanded):
                paths.extend(
                    find_cert_paths(Path(inc_path), env_map, _depth + 1, _seen)
                )
    return paths


def _expand_env(value: str, env_map: dict[str, str]) -> str:
    out = value
    for k, v in env_map.items():
        out = out.replace(f"${{{k}}}", v)
    return out


def unique_paths(paths: list[Path]) -> list[Path]:
    seen = set()
    out: list[Path] = []
    for p in paths:
        q = p.resolve() if p.exists() else p
        if q in seen:
            continue
        seen.add(q)
        out.append(p)
    return out


# ---------- SMS ----------


PAYAM_SMS_TOKEN_URL = "https://www.payamsms.com/auth/oauth/token"
PAYAM_SMS_SEND_URL = "https://www.payamsms.com/panel/webservice/sendMultipleWithSrc"


def _validate_https_endpoint(value: str, name: str) -> None:
    parsed = urlparse(value)
    try:
        port = parsed.port
    except ValueError as exc:
        raise ValueError(f"{name} has an invalid port") from exc
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.params
        or parsed.query
        or parsed.fragment
        or (port is not None and not 1 <= port <= 65535)
    ):
        raise ValueError(
            f"{name} must be an absolute HTTPS URL without credentials or query"
        )


def _payamsms_token(cfg: dict[str, str], timeout_s: float) -> str:
    token_url = cfg.get("PAYAM_SMS_TOKEN_URL", "").strip() or PAYAM_SMS_TOKEN_URL
    headers = {"Content-Type": "application/x-www-form-urlencoded"}
    root_access_token = cfg.get("PAYAM_SMS_ROOT_ACCESS_TOKEN", "").strip()
    if root_access_token:
        headers["Authorization"] = f"Basic {root_access_token}"
    resp = requests.post(
        token_url,
        data={
            "systemName": cfg["PAYAM_SMS_SYSTEM_NAME"],
            "username": cfg["PAYAM_SMS_USERNAME"],
            "password": cfg["PAYAM_SMS_PASSWORD"],
            "scope": cfg.get("PAYAM_SMS_SCOPE", "").strip() or "webservice",
            "grant_type": cfg.get("PAYAM_SMS_GRANT_TYPE", "").strip() or "password",
        },
        headers=headers,
        timeout=timeout_s,
    )
    resp.raise_for_status()
    result = resp.json()
    token = result.get("access_token", "") if isinstance(result, dict) else ""
    if not isinstance(token, str) or not token.strip():
        raise RuntimeError("PayamSMS token response did not contain access_token")
    return token.strip()


def _send_payamsms(
    recipient: str, body: str, cfg: dict[str, str], timeout_s: float
) -> None:
    token = _payamsms_token(cfg, timeout_s)
    send_url = cfg.get("PAYAM_SMS_SEND_URL", "").strip() or PAYAM_SMS_SEND_URL
    payload = {
        "sender": cfg["SMS_SOURCE_NUMBER"],
        "smsItems": [
            {
                "recipient": recipient,
                "body": body,
                "customerId": f"cert-alert-{time.time_ns()}",
            }
        ],
    }
    resp = requests.post(
        send_url,
        json=payload,
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json; charset=utf-8",
        },
        timeout=timeout_s,
    )
    resp.raise_for_status()
    results = resp.json()
    if not isinstance(results, list) or not results:
        raise RuntimeError("PayamSMS returned an invalid or empty result")
    for result in results:
        if not isinstance(result, dict):
            raise RuntimeError("PayamSMS returned a non-object result")
        if str(result.get("errorCode") or "").strip():
            raise RuntimeError("PayamSMS rejected the certificate alert")


def send_sms(recipient: str, body: str, cfg: dict[str, str], timeout_s: float) -> None:
    if cfg.get("EXTERNAL_SERVICES_MODE", "").strip().lower() == "mock":
        logging.info(
            "mock provider=certificate-alert action=send recipient=***%s",
            recipient[-4:],
        )
        return
    if cfg["SMS_PROVIDER_DOMAIN"].strip().lower() == "payamsms":
        _send_payamsms(recipient, body, cfg, timeout_s)
        return

    url = f"https://{cfg['SMS_PROVIDER_DOMAIN']}/api/v3.0.1/send"
    payload = [
        {
            "srcNum": cfg["SMS_SOURCE_NUMBER"],
            "recipient": recipient,
            "body": body,
            "customerId": None,
            "retryCount": int(cfg.get("SMS_RETRY_COUNT", 3)),
            "type": 1,
            "validityPeriod": int(cfg.get("SMS_VALIDITY_PERIOD", 300)),
        }
    ]
    resp = requests.post(
        url,
        json=payload,
        headers={"Content-Type": "application/json", "x-api-key": cfg["SMS_API_KEY"]},
        timeout=timeout_s,
    )
    resp.raise_for_status()
    results = resp.json()
    if not isinstance(results, list) or not results:
        raise RuntimeError("SMS provider returned an invalid or empty result")
    # basic validation like Go service
    for r in results:
        if not isinstance(r, dict):
            raise RuntimeError("SMS provider returned a non-object result")
        if r.get("statusCode") != 200 or r.get("status") != "ACCEPTED":
            raise RuntimeError("SMS provider rejected the certificate alert")


def _validate_hostname_with_optional_port(value: str) -> None:
    if any(ord(character) < 33 for character in value) or any(
        character in value for character in "/\\@?#"
    ):
        raise ValueError("SMS_PROVIDER_DOMAIN must be a hostname with optional port")
    host, separator, raw_port = value.rpartition(":")
    if not separator:
        host = value
    elif not host or not raw_port.isdigit() or not 1 <= int(raw_port) <= 65535:
        raise ValueError("SMS_PROVIDER_DOMAIN has an invalid port")
    labels = host.split(".")
    if any(
        not label
        or len(label) > 63
        or not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?", label)
        for label in labels
    ):
        raise ValueError("SMS_PROVIDER_DOMAIN must be a valid hostname")


def _validate_sms_config(cfg: dict[str, str]) -> None:
    if cfg.get("EXTERNAL_SERVICES_MODE", "").strip().lower() == "mock":
        return
    provider = cfg.get("SMS_PROVIDER_DOMAIN", "").strip()
    if not provider:
        raise SystemExit("Missing SMS config env: SMS_PROVIDER_DOMAIN")

    source_number = cfg.get("SMS_SOURCE_NUMBER", "").strip()
    if not source_number:
        raise SystemExit("Missing SMS config env: SMS_SOURCE_NUMBER")
    if not re.fullmatch(r"[0-9]{3,20}", source_number):
        raise ValueError("SMS_SOURCE_NUMBER must contain 3 to 20 digits")

    if provider.lower() == "payamsms":
        required = [
            "PAYAM_SMS_SYSTEM_NAME",
            "PAYAM_SMS_USERNAME",
            "PAYAM_SMS_PASSWORD",
        ]
        missing = [key for key in required if not cfg.get(key, "").strip()]
        if missing:
            raise SystemExit(f"Missing PayamSMS config envs: {', '.join(missing)}")
        _validate_https_endpoint(
            cfg.get("PAYAM_SMS_TOKEN_URL", "").strip() or PAYAM_SMS_TOKEN_URL,
            "PAYAM_SMS_TOKEN_URL",
        )
        _validate_https_endpoint(
            cfg.get("PAYAM_SMS_SEND_URL", "").strip() or PAYAM_SMS_SEND_URL,
            "PAYAM_SMS_SEND_URL",
        )
        return

    _validate_hostname_with_optional_port(provider)
    if not cfg.get("SMS_API_KEY", "").strip():
        raise SystemExit("Missing SMS config env: SMS_API_KEY")
    retry_count = int(cfg.get("SMS_RETRY_COUNT", "").strip() or "3")
    validity_period = int(cfg.get("SMS_VALIDITY_PERIOD", "").strip() or "300")
    if retry_count < 0:
        raise ValueError("SMS_RETRY_COUNT must be non-negative")
    if validity_period <= 0:
        raise ValueError("SMS_VALIDITY_PERIOD must be greater than zero")


# ---------- Main logic ----------


def check_and_notify() -> None:
    log = logging.getLogger("cert_monitor")
    env_map = {k: v for k, v in os.environ.items()}
    conf_path = Path(
        os.getenv("CERT_ALERT_CONF_PATH", "/etc/nginx/sites-available/yamata-beta.conf")
    )
    threshold_days = int(os.getenv("CERT_ALERT_THRESHOLD_DAYS", "7"))
    if threshold_days < 0:
        raise ValueError("CERT_ALERT_THRESHOLD_DAYS must be non-negative")
    recipient = os.getenv("CERT_ALERT_PHONE")
    if not recipient:
        raise SystemExit("CERT_ALERT_PHONE is required")
    if not re.fullmatch(r"\+?[0-9]{8,15}", recipient):
        raise ValueError("CERT_ALERT_PHONE must contain 8 to 15 digits")

    timeout_s = _parse_timedelta(os.getenv("SMS_TIMEOUT", "30s"))
    _validate_sms_config(env_map)

    conf_globs = os.getenv(
        "CERT_ALERT_CONF_GLOBS",
        "/etc/nginx/sites-enabled/*.conf,/etc/nginx/conf.d/*.conf",
    )
    conf_files: list[Path] = [conf_path]
    for g in conf_globs.split(","):
        g = g.strip()
        if not g:
            continue
        for p in glob.glob(g):
            conf_files.append(Path(p))
    conf_files = unique_paths(conf_files)

    log.info(
        "Scanning configs: %s",
        ", ".join(str(p) for p in conf_files if Path(p).exists()),
    )

    paths: list[Path] = []
    for cf in conf_files:
        paths.extend(find_cert_paths(cf, env_map))

    extra = os.getenv("CERT_ALERT_CERT_PATHS", "")
    if extra:
        paths.extend(Path(p.strip()) for p in extra.split(",") if p.strip())

    # If nothing found, try default letsencrypt path using DOMAIN
    default_domain = env_map.get("DOMAIN")
    if default_domain:
        paths.append(Path(f"/etc/letsencrypt/live/{default_domain}/fullchain.pem"))

    paths = unique_paths(paths)
    if not paths:
        raise SystemExit("No certificate paths found to check")

    now = datetime.now(timezone.utc)
    alerts: list[tuple[Path, datetime, int]] = []
    failures: list[tuple[Path, str]] = []
    for p in paths:
        if not p.exists():
            reason = "certificate file is missing"
            log.error("%s: %s", reason, p)
            failures.append((p, reason))
            continue
        try:
            expiry = load_cert_expiry(p)
        except Exception as e:
            reason = "certificate file is unreadable or invalid"
            log.error("%s: %s (%s)", reason, p, e)
            failures.append((p, reason))
            continue
        days_left = int((expiry - now).total_seconds() // 86400)
        log.info("checked %s -> expires %s (%sd)", p, expiry.date(), days_left)
        if days_left <= threshold_days:
            alerts.append((p, expiry, days_left))

    if not alerts and not failures:
        log.info("All certificates healthy. Checked %d paths.", len(paths))
        return

    for p, reason in failures:
        send_sms(
            recipient,
            f"[Yamata Beta] SSL cert alert: {reason}. Path: {p}",
            env_map,
            timeout_s,
        )
        log.warning("Alert sent for %s: %s", p, reason)

    for p, exp, days_left in alerts:
        status = "expired" if days_left < 0 else f"{days_left}d left"
        msg = (
            f"[Yamata Beta] SSL cert alert: {p.name} expires on {exp.date()} UTC ({status}). "
            f"Path: {p}"
        )
        send_sms(recipient, msg, env_map, timeout_s)
        log.warning("Alert sent for %s: %s", p, status)


def main() -> None:
    log_level = os.getenv("CERT_ALERT_LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, log_level, logging.INFO),
        format="%(asctime)s [%(levelname)s] %(name)s %(message)s",
    )
    retry_interval = _parse_timedelta(os.getenv("CERT_ALERT_RETRY_INTERVAL", "5m"))
    # one run on start, then every 24h; transient failures retry sooner
    while True:
        sleep_seconds = 24 * 3600
        try:
            check_and_notify()
            Path("/tmp/cert-monitor-last-success").touch(mode=0o600)
        except Exception as e:
            logging.getLogger("cert_monitor").exception(
                "Certificate check failed: %s", e
            )
            sleep_seconds = retry_interval
        time.sleep(sleep_seconds)


if __name__ == "__main__":
    main()
