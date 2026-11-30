#!/usr/bin/env python3
"""Resend only SMS provider batches which were definitively rejected with HTTP 400.

This is an operational recovery tool, not a normal campaign dispatcher.  It
opens PostgreSQL read-only and never changes campaign, sent_sms, or status-job
records.  Its default mode is a dry run.  --execute needs both campaign and
attempt-count confirmation because a successful provider request cannot be
rolled back by this script.

For PayamSMS the old tracking IDs are sent again as customerId values.  For
Candoo the old numeric provider_customer_id values are reused; Candoo does not
accept Yamata's hexadecimal tracking ID as customerId.  Reusing those IDs also
means the normal status report can look up the replacement result later.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import logging
import os
import sys
import urllib.parse
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

from load_dotenv import DotenvError, parse_dotenv
from script_common import read_secret, validate_database_port, validate_https_origin, validate_positive_ids

try:
    import psycopg
    from psycopg.rows import dict_row
    import requests
except ImportError as exc:  # pragma: no cover
    raise SystemExit("install scripts requirements first: python3 -m pip install -r scripts/requirements.txt") from exc


LOGGER = logging.getLogger("yamata.resend_sms_http_400")
DEFAULT_ENV_FILE = Path(__file__).resolve().parents[1] / ".env.beta"


class RecoveryError(RuntimeError):
    pass


@dataclass(frozen=True)
class Recipient:
    tracking_id: str
    phone: str
    body: str
    customer_id: int | None


@dataclass(frozen=True)
class FailedAttempt:
    id: int
    provider: str
    tracking_ids: tuple[str, ...]
    recipients: tuple[Recipient, ...]


def body(spec: dict[str, Any], code: str, uid: str) -> str:
    content = str(spec.get("content") or "")
    adlink = str(spec.get("adlink") or "")
    if adlink:
        domain = str(spec.get("short_link_domain") or "").strip()
        link = domain.rstrip("/") + "/" + code if domain else adlink.replace("{uid}", uid)
        return content.replace("{YOUR_LINK}", link) + "\nلغو۱۱"
    return content.replace("{YOUR_LINK}", "") + "\nلغو۱۱"


def load_env(path: Path) -> None:
    try:
        values = parse_dotenv(path)
    except (DotenvError, UnicodeError) as exc:
        raise RecoveryError(f"cannot load env file {path}: {exc}") from exc
    for key, value in values:
        os.environ.setdefault(key, value)


def nonempty(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        raise RecoveryError(f"{name} is required for --execute")
    return value


def integer_env(name: str, default: int, maximum: int) -> int:
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise RecoveryError(f"{name} must be an integer") from exc
    if not 0 <= value <= maximum:
        raise RecoveryError(f"{name} must be between 0 and {maximum}")
    return value


def candoo_recipient(value: str) -> str:
    """Mirror normalizeCandooNumber in the scheduler before a send request."""
    number = value.strip().replace(" ", "").replace("-", "")
    if number.startswith("+98"):
        number = number[1:]
    elif number.startswith("0098"):
        number = "98" + number[4:]
    elif number.startswith("0"):
        number = "98" + number[1:]
    if not number.startswith("98") or len(number) < 11 or not number.isascii() or not number.isdigit():
        raise RecoveryError("Candoo recipient is not a valid Iranian 98-format number")
    return number


def https_url(name: str, value: str) -> str:
    """Accept an HTTPS endpoint (unlike an origin, it may contain a path)."""
    parsed = urllib.parse.urlparse(value.strip())
    if (
        parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password
        or parsed.query or parsed.fragment or any(ord(char) < 32 for char in value)
    ):
        raise RecoveryError(f"{name} must be a credential-free HTTPS URL without query or fragment")
    return value.strip()


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("campaign_id", type=int, help="campaigns.id to recover")
    parser.add_argument("--env-file", type=Path, default=DEFAULT_ENV_FILE)
    # dotenv is loaded after parsing --env-file, so these intentionally begin
    # empty and are filled from the environment in main().
    parser.add_argument("--db-host")
    parser.add_argument("--db-port", type=int)
    parser.add_argument("--db-name")
    parser.add_argument("--db-user")
    parser.add_argument("--db-sslmode")
    parser.add_argument("--attempt-id", type=int, action="append", help="only this failed send-attempt ID (repeatable)")
    parser.add_argument("--execute", action="store_true", help="send provider requests; default is dry-run")
    parser.add_argument("--confirm-campaign-id", type=int)
    parser.add_argument("--confirm-attempt-count", type=int, help="must equal the dry-run selected attempt count")
    parser.add_argument("--audit-file", type=Path, help="new local JSONL audit path; required implicitly in execute mode")
    parser.add_argument("--timeout", type=float, default=60.0)
    args = parser.parse_args(argv)
    validate_positive_ids(parser, "campaign_id", [args.campaign_id])
    if args.db_port is not None:
        validate_database_port(parser, args.db_port)
    if args.attempt_id:
        validate_positive_ids(parser, "attempt-id", args.attempt_id)
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.execute and (args.confirm_campaign_id != args.campaign_id or args.confirm_attempt_count is None):
        parser.error("--execute requires --confirm-campaign-id and --confirm-attempt-count")
    if args.audit_file is None:
        args.audit_file = Path(f"resend-sms-http-400-campaign-{args.campaign_id}.jsonl")
    return args


def connect(args: argparse.Namespace):
    if not args.db_name.strip() or not args.db_user.strip():
        raise RecoveryError("--db-name/DB_NAME and --db-user/DB_USER are required")
    return psycopg.connect(
        host=args.db_host, port=args.db_port, dbname=args.db_name, user=args.db_user,
        password=read_secret("DB_PASSWORD", "Database password: "), sslmode=args.db_sslmode,
        connect_timeout=10, autocommit=False, row_factory=dict_row,
        options="-c default_transaction_read_only=on -c statement_timeout=60000",
    )


def private_new_file(path: Path):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    descriptor = os.open(path, flags, 0o600)
    os.fchmod(descriptor, 0o600)
    return os.fdopen(descriptor, "w", encoding="utf-8")


def digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:16]


def load_attempts(conn, campaign_id: int, attempt_ids: list[int] | None) -> tuple[str, list[FailedAttempt]]:
    where = "a.http_status_code = 400"
    if attempt_ids:
        where += " AND a.id = ANY(%s)"
    campaign = conn.execute("""
        SELECT pc.id, pc.campaign_json, pc.audience_ids, pc.audience_codes
        FROM processed_campaigns pc
        WHERE pc.campaign_id=%s AND pc.is_current
    """, (campaign_id,)).fetchone()
    if not campaign:
        raise RecoveryError("current processed campaign was not found")
    # sms_provider_send_attempts references processed_campaigns, not campaigns.
    parameters: list[Any] = [campaign["id"]]
    if attempt_ids:
        parameters.append(attempt_ids)
    spec = campaign["campaign_json"] if isinstance(campaign["campaign_json"], dict) else json.loads(campaign["campaign_json"])
    sender = str(spec.get("line_number") or "").strip()
    if str(spec.get("platform") or "").lower() != "sms" or not sender:
        raise RecoveryError("stored campaign JSON is not a valid SMS campaign")
    audience_ids, codes = list(campaign["audience_ids"] or []), list(campaign["audience_codes"] or [])
    if len(audience_ids) != len(codes):
        raise RecoveryError("stored audience IDs/codes have different lengths")
    sent = conn.execute("""
        SELECT tracking_id, phone_number, provider, provider_customer_id
        FROM sent_sms WHERE processed_campaign_id=%s AND NULLIF(BTRIM(phone_number), '') IS NOT NULL
        ORDER BY id
    """, (campaign["id"],)).fetchall()
    if len(sent) != len(audience_ids):
        raise RecoveryError("cannot prove sent_sms order matches stored audience order")
    profiles = conn.execute("SELECT id, phone_number, uid FROM audience_profiles WHERE id = ANY(%s)", (audience_ids,)).fetchall()
    profile_by_id = {row["id"]: row for row in profiles}
    by_tracking: dict[str, Recipient] = {}
    for index, message in enumerate(sent):
        profile = profile_by_id.get(audience_ids[index])
        if not profile or not profile["uid"] or not profile["phone_number"]:
            raise RecoveryError(f"audience {audience_ids[index]} is no longer usable")
        if str(profile["phone_number"]).strip() != str(message["phone_number"]).strip():
            raise RecoveryError("audience/ sent_sms phone ordering no longer agrees")
        tracking = str(message["tracking_id"] or "").strip()
        if not tracking or tracking in by_tracking:
            raise RecoveryError("sent_sms tracking IDs are missing or duplicated")
        by_tracking[tracking] = Recipient(tracking, str(message["phone_number"]), body(spec, str(codes[index]), str(profile["uid"])), message["provider_customer_id"])
    rows = conn.execute(f"""
        SELECT a.id, LOWER(BTRIM(a.provider)) AS provider, a.tracking_ids
        FROM sms_provider_send_attempts a
        WHERE a.processed_campaign_id=%s AND {where}
        ORDER BY a.id
    """, parameters).fetchall()
    if attempt_ids and len(rows) != len(set(attempt_ids)):
        raise RecoveryError("one or more --attempt-id values are not HTTP-400 attempts for this campaign")
    selected: set[str] = set()
    attempts: list[FailedAttempt] = []
    for row in rows:
        provider = str(row["provider"] or "").lower()
        tracking_ids = tuple(str(x).strip() for x in row["tracking_ids"] or [])
        limit = 200 if provider == "payamsms" else 100 if provider == "candoo" else 0
        if not limit or not tracking_ids or len(tracking_ids) > limit or len(set(tracking_ids)) != len(tracking_ids):
            raise RecoveryError(f"attempt {row['id']} has unsafe provider/tracking IDs")
        if selected.intersection(tracking_ids):
            raise RecoveryError("selected HTTP-400 attempts overlap; select one attempt explicitly")
        recipients = tuple(by_tracking.get(tracking) for tracking in tracking_ids)
        if any(item is None for item in recipients):
            raise RecoveryError(f"attempt {row['id']} has no matching send record")
        if any((provider == "candoo" and (item.customer_id is None or item.customer_id <= 0)) for item in recipients):
            raise RecoveryError(f"attempt {row['id']} has no usable Candoo customer ID")
        selected.update(tracking_ids)
        attempts.append(FailedAttempt(int(row["id"]), provider, tracking_ids, recipients))
    if not attempts:
        raise RecoveryError("no HTTP-400 provider attempts matched")
    return sender, attempts


def provider_config(provider: str) -> dict[str, Any]:
    if provider == "candoo":
        if os.getenv("CANDOO_SMS_ENABLED", "").strip().lower() not in {"1", "true", "yes"}:
            raise RecoveryError("CANDOO_SMS_ENABLED must be true for --execute")
        key = nonempty("CANDOO_SMS_API_KEY")
        origin = validate_https_origin(argparse.ArgumentParser(add_help=False), "CANDOO_SMS_BASE_URL", os.getenv("CANDOO_SMS_BASE_URL", "https://api.candoosms.com"))
        return {"url": origin + "/api/v3.0.1/send", "headers": {"x-api-key": key}, "type": integer_env("CANDOO_SMS_MESSAGE_TYPE", 0, 4), "retry": integer_env("CANDOO_SMS_RETRY_COUNT", 0, 10), "validity": integer_env("CANDOO_SMS_VALIDITY_PERIOD", 0, 172800)}
    if provider == "payamsms":
        token_url = https_url("PAYAM_SMS_TOKEN_URL", os.getenv("PAYAM_SMS_TOKEN_URL", "https://www.payamsms.com/auth/oauth/token"))
        send_url = https_url("PAYAM_SMS_SEND_URL", os.getenv("PAYAM_SMS_SEND_URL", "https://www.payamsms.com/panel/webservice/sendMultipleWithSrc"))
        return {"token_url": token_url, "send_url": send_url, "username": nonempty("PAYAM_SMS_USERNAME"), "password": read_secret("PAYAM_SMS_PASSWORD", "PayamSMS password: "), "system": os.getenv("PAYAM_SMS_SYSTEM_NAME", "").strip(), "scope": os.getenv("PAYAM_SMS_SCOPE", "webservice").strip() or "webservice", "grant": os.getenv("PAYAM_SMS_GRANT_TYPE", "password").strip() or "password", "root": nonempty("PAYAM_SMS_ROOT_ACCESS_TOKEN")}
    raise RecoveryError(f"unsupported provider {provider}")


def validate_send_response(provider: str, response, attempt: FailedAttempt) -> None:
    """Reject a 2xx response that cannot be correlated to every submitted row."""
    if not 200 <= response.status_code < 300:
        return
    try:
        items = response.json()
    except ValueError as exc:
        raise RecoveryError(f"{provider} returned 2xx with invalid JSON; result is uncertain") from exc
    if not isinstance(items, list):
        raise RecoveryError(f"{provider} returned 2xx with a non-list response; result is uncertain")
    if provider == "candoo":
        expected = {str(item.customer_id) for item in attempt.recipients}
        received: set[str] = set()
        for item in items:
            if not isinstance(item, dict):
                raise RecoveryError("Candoo returned a non-object item; result is uncertain")
            customer_id = str(item.get("customerId") or "")
            status = str(item.get("status") or "").upper()
            if customer_id in received or customer_id not in expected:
                raise RecoveryError("Candoo returned unexpected customer IDs; result is uncertain")
            # Match the scheduler: ACCEPTED needs a provider message ID;
            # REJECTED is a definitive non-send outcome.
            if status == "ACCEPTED" and int(item.get("messageId") or 0) > 0:
                received.add(customer_id)
            elif status == "REJECTED":
                received.add(customer_id)
            else:
                raise RecoveryError("Candoo returned an unknown item status; result is uncertain")
    else:
        expected = set(attempt.tracking_ids)
        received = set()
        for item in items:
            if not isinstance(item, dict):
                raise RecoveryError("PayamSMS returned a non-object item; result is uncertain")
            tracking_id = str(item.get("customerId") or item.get("trackingId") or "").strip()
            if not tracking_id or tracking_id in received or tracking_id not in expected:
                raise RecoveryError("PayamSMS returned unexpected tracking IDs; result is uncertain")
            received.add(tracking_id)
    if received != expected:
        raise RecoveryError(f"{provider} response did not acknowledge every submitted row; result is uncertain")


def send_attempt(session, attempt: FailedAttempt, sender: str, timeout: float) -> tuple[int, str]:
    cfg = provider_config(attempt.provider)
    if attempt.provider == "candoo":
        payload = [{"srcNum": sender, "recipient": candoo_recipient(item.phone), "body": item.body, "customerId": item.customer_id, "type": cfg["type"], "retryCount": cfg["retry"], "validityPeriod": cfg["validity"]} for item in attempt.recipients]
        response = session.post(cfg["url"], json=payload, headers=cfg["headers"], timeout=timeout)
    else:
        if not cfg["system"]:
            raise RecoveryError("PAYAM_SMS_SYSTEM_NAME is required for --execute")
        token = session.post(cfg["token_url"], params={"systemName": cfg["system"], "username": cfg["username"], "password": cfg["password"], "scope": cfg["scope"], "grant_type": cfg["grant"]}, headers={"Authorization": "Basic " + cfg["root"]}, timeout=timeout)
        token.raise_for_status()
        access = token.json().get("access_token")
        if not isinstance(access, str) or not access:
            raise RecoveryError("PayamSMS login returned no access token")
        payload = {"sender": sender, "smsItems": [{"recipient": item.phone, "body": item.body, "customerId": item.tracking_id} for item in attempt.recipients]}
        response = session.post(cfg["send_url"], json=payload, headers={"Authorization": "Bearer " + access}, timeout=timeout)
    validate_send_response(attempt.provider, response, attempt)
    return response.status_code, hashlib.sha256(response.content).hexdigest()


def describe(attempts: Iterable[FailedAttempt]) -> list[dict[str, Any]]:
    return [{"attempt_id": a.id, "provider": a.provider, "recipient_count": len(a.recipients), "tracking_id_digests": [digest(x) for x in a.tracking_ids]} for a in attempts]


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        load_env(args.env_file)
        # Arguments win, but dotenv is the normal operations source.  argparse
        # has already run when the file is loaded, so fill its empty DB fields.
        args.db_host = args.db_host or os.getenv("DB_HOST", "172.29.0.2")
        args.db_port = args.db_port or int(os.getenv("DB_PORT", "5432"))
        args.db_name = args.db_name or os.getenv("DB_NAME", "")
        args.db_user = args.db_user or os.getenv("DB_USER", "")
        args.db_sslmode = args.db_sslmode or os.getenv("DB_SSL_MODE", "require")
        validate_database_port(argparse.ArgumentParser(add_help=False), args.db_port)
        with connect(args) as conn:
            sender, attempts = load_attempts(conn, args.campaign_id, args.attempt_id)
        plan = describe(attempts)
        print(json.dumps({"campaign_id": args.campaign_id, "database": "read-only", "execute": args.execute, "selected_attempt_count": len(attempts), "selected_recipient_count": sum(len(a.recipients) for a in attempts), "attempts": plan}, indent=2))
        if not args.execute:
            return 0
        if args.confirm_attempt_count != len(attempts):
            raise RecoveryError(f"--confirm-attempt-count={args.confirm_attempt_count} does not match selected count {len(attempts)}")
        # A brand-new audit file makes accidental repeat execution fail closed.
        with private_new_file(args.audit_file) as audit, requests.Session() as session:
            for attempt in attempts:
                event = {"campaign_id": args.campaign_id, "attempt_id": attempt.id, "provider": attempt.provider, "recipient_count": len(attempt.recipients), "tracking_id_digests": [digest(x) for x in attempt.tracking_ids]}
                try:
                    status, response_sha256 = send_attempt(session, attempt, sender, args.timeout)
                    event.update({"http_status_code": status, "response_sha256": response_sha256})
                    audit.write(json.dumps(event, separators=(",", ":")) + "\n"); audit.flush()
                    if not 200 <= status < 300:
                        raise RecoveryError(f"attempt {attempt.id} returned HTTP {status}; stopped without sending later attempts")
                except Exception as exc:
                    if "http_status_code" in event:
                        raise
                    event.update({"error": str(exc)[:512]})
                    audit.write(json.dumps(event, separators=(",", ":")) + "\n"); audit.flush()
                    raise
        print(f"sent {len(attempts)} HTTP-400 recovery batches; DB unchanged; audit={args.audit_file}")
        return 0
    except (RecoveryError, psycopg.Error, requests.RequestException, OSError, ValueError) as exc:
        LOGGER.error("failed: %s", exc)
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
