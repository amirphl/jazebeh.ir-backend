#!/usr/bin/env python3
"""Export fresh, read-only SMS delivery statuses for one campaign.

The normal scheduler creates ``campaign_status_jobs`` and writes its delivery
results back to the database.  This diagnostic tool deliberately does neither:
it reads the jobs and corresponding ``sent_sms`` rows from PostgreSQL, asks the
original provider for the latest delivery state, and writes a local CSV or
JSONL report.  It never updates database rows or sends messages.

If no status jobs were created, --include-unjobbed-sent-sms (the default) also
looks at the campaign's sent messages.  This is useful for diagnosing exactly
the failure mode where a campaign was submitted but job creation never ran.

Required environment variables:
  DB_NAME, DB_USER, DB_PASSWORD
  PAYAM_SMS_SYSTEM_NAME, PAYAM_SMS_USERNAME, PAYAM_SMS_PASSWORD (when needed)
  CANDOO_SMS_API_KEY (when needed)

The remaining DB/provider values use the same environment names and defaults
as the application.  Install dependencies with:
  python3 -m pip install -r scripts/requirements.txt
"""

from __future__ import annotations

import argparse
import csv
import json
import os
import sys
import time
import urllib.parse
from collections import defaultdict
from collections.abc import Iterable, Iterator, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

from load_dotenv import DotenvError, parse_dotenv
from script_common import read_secret, validate_database_port, validate_positive_ids

PAYAM_BATCH_SIZE = 200
CANDOO_BATCH_SIZE = 100
RETRYABLE_HTTP_STATUSES = frozenset((429, 500, 502, 503, 504))
MAX_ATTEMPTS = 5
DEFAULT_ENV_FILE = Path(__file__).resolve().parents[1] / ".env.beta"


@dataclass
class Message:
    processed_campaign_id: int
    tracking_id: str
    provider: str
    lookup_id: str | None
    job_ids: set[int] = field(default_factory=set)
    job_executed_at: list[str] = field(default_factory=list)
    job_errors: list[str] = field(default_factory=list)
    source: str = "campaign_status_job"


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("campaign_id", type=int, help="campaigns.id to inspect")
    parser.add_argument("--db-host", default="172.30.0.10")
    parser.add_argument("--db-port", type=int, default=int(os.getenv("DB_PORT", "5432")))
    parser.add_argument("--db-name", default=os.getenv("DB_NAME", ""))
    parser.add_argument("--db-user", default=os.getenv("DB_USER", ""))
    parser.add_argument("--db-sslmode", default=os.getenv("DB_SSL_MODE", "require"))
    parser.add_argument("--output", type=Path, help="Report path (default: ./campaign-<id>-provider-statuses.csv)")
    parser.add_argument("--format", choices=("csv", "jsonl"), default="csv")
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument("--request-delay", type=float, default=1.0, help="seconds between provider requests")
    parser.add_argument("--include-unjobbed-sent-sms", action=argparse.BooleanOptionalAction, default=True)
    args = parser.parse_args(argv)
    validate_positive_ids(parser, "campaign_id", [args.campaign_id])
    validate_database_port(parser, args.db_port)
    if not args.db_name.strip() or not args.db_user.strip():
        parser.error("--db-name/DB_NAME and --db-user/DB_USER are required")
    if args.timeout <= 0 or args.request_delay < 0:
        parser.error("--timeout must be positive and --request-delay must be non-negative")
    if args.output is None:
        args.output = Path(f"campaign-{args.campaign_id}-provider-statuses.{args.format}")
    if args.output.exists():
        parser.error(f"refusing to overwrite existing output: {args.output}")
    return args


def load_repository_env(path: Path = DEFAULT_ENV_FILE) -> None:
    """Load the repository dotenv file as data, preserving shell overrides."""
    if not path.is_file():
        return
    try:
        values = parse_dotenv(path)
    except (DotenvError, UnicodeError) as exc:
        raise RuntimeError(f"invalid dotenv file {path}: {exc}") from exc
    for key, value in values:
        # A value deliberately exported by the invoking shell is authoritative.
        os.environ.setdefault(key, value)


def require_https_url(env_name: str, default: str) -> str:
    value = os.getenv(env_name, default).strip()
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise RuntimeError(f"{env_name} must be an HTTPS URL without credentials, query, or fragment")
    return value


def connect_read_only(args: argparse.Namespace):
    try:
        import psycopg2
    except ImportError as exc:
        raise RuntimeError("psycopg2 is required; install scripts/requirements.txt") from exc
    connection = psycopg2.connect(
        host=args.db_host, port=args.db_port, dbname=args.db_name, user=args.db_user,
        password=read_secret("DB_PASSWORD", "Database password: "), sslmode=args.db_sslmode,
        connect_timeout=10, application_name="export_sms_campaign_provider_statuses_readonly",
        options="-c statement_timeout=60000 -c default_transaction_read_only=on",
    )
    connection.set_session(readonly=True, autocommit=True)
    return connection


def fetch_messages(cur, campaign_id: int, include_unjobbed: bool) -> list[Message]:
    # PayamSMS is queried with byCustomer=true, so its lookup ID is the
    # application's original customer/tracking ID. Candoo instead requires the
    # numeric provider_customer_id stored on sent_sms.
    cur.execute(
        """
        SELECT j.id, j.processed_campaign_id, COALESCE(j.provider, 'payamsms'),
               j.executed_at, j.error, BTRIM(job_tracking_id),
               BTRIM(s.tracking_id), s.provider, s.provider_customer_id
        FROM campaign_status_jobs AS j
        JOIN processed_campaigns AS pc ON pc.id = j.processed_campaign_id
        CROSS JOIN LATERAL unnest(j.tracking_ids) AS job_tracking_id
        LEFT JOIN sent_sms AS s
          ON s.processed_campaign_id = j.processed_campaign_id
         AND s.tracking_id = job_tracking_id
        WHERE pc.campaign_id = %s AND j.platform = 'sms'
        ORDER BY j.id, job_tracking_id
        """,
        (campaign_id,),
    )
    messages: dict[tuple[int, str], Message] = {}
    for job_id, processed_id, job_provider, executed_at, job_error, job_tracking, tracking, sent_provider, customer_id in cur.fetchall():
        tracking_id = str(tracking or job_tracking or "").strip()
        if not tracking_id:
            continue
        provider = str(sent_provider or job_provider or "payamsms").strip().lower()
        key = (int(processed_id), tracking_id)
        message = messages.setdefault(key, Message(int(processed_id), tracking_id, provider, None))
        message.job_ids.add(int(job_id))
        if executed_at:
            message.job_executed_at.append(executed_at.isoformat())
        if job_error:
            message.job_errors.append(str(job_error))
        if provider == "payamsms":
            message.lookup_id = tracking_id
        elif provider == "candoo":
            message.lookup_id = str(customer_id) if customer_id else None

    if include_unjobbed:
        cur.execute(
            """
            SELECT s.processed_campaign_id, BTRIM(s.tracking_id), s.provider,
                   s.provider_customer_id
            FROM sent_sms AS s
            JOIN processed_campaigns AS pc ON pc.id = s.processed_campaign_id
            WHERE pc.campaign_id = %s AND BTRIM(s.tracking_id) <> ''
            ORDER BY s.processed_campaign_id, s.tracking_id
            """,
            (campaign_id,),
        )
        for processed_id, tracking_id, provider, customer_id in cur.fetchall():
            key = (int(processed_id), str(tracking_id).strip())
            if key in messages:
                continue
            provider = str(provider or "payamsms").strip().lower()
            lookup_id = key[1] if provider == "payamsms" else (str(customer_id) if provider == "candoo" and customer_id else None)
            messages[key] = Message(int(processed_id), key[1], provider, lookup_id, source="sent_sms_without_status_job")
    return list(messages.values())


def chunks(values: Sequence[str], size: int) -> Iterator[Sequence[str]]:
    for start in range(0, len(values), size):
        yield values[start : start + size]


def payam_config() -> dict[str, str]:
    config = {
        "token_url": require_https_url("PAYAM_SMS_TOKEN_URL", "https://www.payamsms.com/auth/oauth/token"),
        "status_url": require_https_url("PAYAM_SMS_STATUS_URL", "https://www.payamsms.com/report/webservice/status"),
        "system_name": os.getenv("PAYAM_SMS_SYSTEM_NAME", "").strip(),
        "username": os.getenv("PAYAM_SMS_USERNAME", "").strip(),
        "password": read_secret("PAYAM_SMS_PASSWORD", "PayamSMS password: "),
        "scope": os.getenv("PAYAM_SMS_SCOPE", "webservice").strip() or "webservice",
        "grant_type": os.getenv("PAYAM_SMS_GRANT_TYPE", "password").strip() or "password",
        "root_access_token": os.getenv("PAYAM_SMS_ROOT_ACCESS_TOKEN", "").strip(),
    }
    if not config["system_name"] or not config["username"]:
        raise RuntimeError("PAYAM_SMS_SYSTEM_NAME and PAYAM_SMS_USERNAME are required for PayamSMS")
    return config


def retry_delay(attempt: int) -> float:
    return min(float(2**attempt), 120.0)


def payam_login(session, config: Mapping[str, str], timeout: float) -> str:
    headers = {"Content-Type": "application/x-www-form-urlencoded"}
    if config["root_access_token"]:
        headers["Authorization"] = "Basic " + config["root_access_token"]
    payload = {"systemName": config["system_name"], "username": config["username"], "password": config["password"], "scope": config["scope"], "grant_type": config["grant_type"]}
    for attempt in range(MAX_ATTEMPTS):
        response = session.post(config["token_url"], data=payload, headers=headers, timeout=timeout)
        if response.status_code in RETRYABLE_HTTP_STATUSES and attempt + 1 < MAX_ATTEMPTS:
            time.sleep(retry_delay(attempt)); continue
        if not 200 <= response.status_code < 300:
            raise RuntimeError(f"PayamSMS token HTTP {response.status_code}")
        token = response.json().get("access_token", "")
        if isinstance(token, str) and token:
            return token
        raise RuntimeError("PayamSMS token response did not contain access_token")
    raise RuntimeError("PayamSMS token retries exhausted")


def payam_statuses(
    session,
    config: Mapping[str, str],
    lookup_ids: Sequence[str],
    timeout: float,
    delay: float,
    progress: Callable[[int, int, Sequence[str], Sequence[Mapping[str, Any]]], None] | None = None,
) -> dict[str, dict[str, Any]]:
    token = payam_login(session, config, timeout)
    results: dict[str, dict[str, Any]] = {}
    for index, batch in enumerate(chunks(list(lookup_ids), PAYAM_BATCH_SIZE)):
        params = [("byCustomer", "true")] + [("ids", value) for value in batch]
        for attempt in range(MAX_ATTEMPTS):
            response = session.get(config["status_url"], params=params, headers={"Authorization": "Bearer " + token}, timeout=timeout)
            if response.status_code == 401:
                token = payam_login(session, config, timeout); continue
            if response.status_code in RETRYABLE_HTTP_STATUSES and attempt + 1 < MAX_ATTEMPTS:
                time.sleep(retry_delay(attempt)); continue
            if not 200 <= response.status_code < 300:
                raise RuntimeError(f"PayamSMS status HTTP {response.status_code}")
            payload = response.json()
            if not isinstance(payload, list):
                raise RuntimeError("PayamSMS status response was not a JSON array")
            for item in payload:
                if isinstance(item, dict) and str(item.get("customerId", "")).strip():
                    results[str(item["customerId"]).strip()] = item
            if progress:
                progress(
                    index + 1,
                    (len(lookup_ids) + PAYAM_BATCH_SIZE - 1) // PAYAM_BATCH_SIZE,
                    batch,
                    payload,
                )
            break
        else:
            raise RuntimeError("PayamSMS status retries exhausted")
        if index + 1 < (len(lookup_ids) + PAYAM_BATCH_SIZE - 1) // PAYAM_BATCH_SIZE and delay:
            time.sleep(delay)
    return results


def candoo_statuses(
    session,
    lookup_ids: Sequence[str],
    timeout: float,
    delay: float,
    progress: Callable[[int, int, Sequence[str], Sequence[Mapping[str, Any]]], None] | None = None,
) -> dict[str, dict[str, Any]]:
    api_key = os.getenv("CANDOO_SMS_API_KEY", "").strip()
    if not api_key:
        raise RuntimeError("CANDOO_SMS_API_KEY is required for Candoo")
    base_url = require_https_url("CANDOO_SMS_BASE_URL", "https://api.candoosms.com").rstrip("/")
    results: dict[str, dict[str, Any]] = {}
    for index, batch in enumerate(chunks(list(lookup_ids), CANDOO_BATCH_SIZE)):
        try:
            request_ids = [int(value) for value in batch]
        except ValueError as exc:
            raise RuntimeError("Candoo customer IDs must be positive integers") from exc
        if any(value <= 0 for value in request_ids):
            raise RuntimeError("Candoo customer IDs must be positive integers")
        for attempt in range(MAX_ATTEMPTS):
            response = session.post(base_url + "/api/v3.0.1/get-status-customer-id", json=request_ids, headers={"x-api-key": api_key}, timeout=timeout)
            # The scheduler only retries Candoo 429 to avoid repeating an
            # undocumented request after a provider-side 5xx.
            if response.status_code == 429 and attempt + 1 < MAX_ATTEMPTS:
                time.sleep(retry_delay(attempt)); continue
            if not 200 <= response.status_code < 300:
                raise RuntimeError(f"Candoo status HTTP {response.status_code}")
            payload = response.json()
            if not isinstance(payload, list):
                raise RuntimeError("Candoo status response was not a JSON array")
            for item in payload:
                if isinstance(item, dict) and item.get("customerId") is not None:
                    results[str(item["customerId"])] = item
            if progress:
                progress(
                    index + 1,
                    (len(lookup_ids) + CANDOO_BATCH_SIZE - 1) // CANDOO_BATCH_SIZE,
                    batch,
                    payload,
                )
            break
        if index + 1 < (len(lookup_ids) + CANDOO_BATCH_SIZE - 1) // CANDOO_BATCH_SIZE and delay:
            time.sleep(delay)
    return results


def candoo_status_map() -> dict[str, str]:
    result: dict[str, str] = {}
    for pair in os.getenv("CANDOO_SMS_STATUS_MAP", "").split(","):
        if not pair.strip(): continue
        code, separator, status = pair.partition(":")
        if separator and status.strip() in {"pending", "successful", "unsuccessful"}:
            result[code.strip()] = status.strip()
    return result


def report_rows(campaign_id: int, messages: Iterable[Message], provider_results: Mapping[str, Mapping[str, dict[str, Any]]]) -> Iterator[dict[str, Any]]:
    map_candoo = candoo_status_map()
    checked_at = datetime.now(timezone.utc).isoformat()
    for message in sorted(messages, key=lambda row: (row.processed_campaign_id, row.tracking_id)):
        result = provider_results.get(message.provider, {}).get(message.lookup_id or "")
        provider_code = provider_text = internal_status = ""
        delivered = undelivered = unknown = total = ""
        if result and message.provider == "payamsms":
            provider_text = str(result.get("status", ""))
            total, delivered, undelivered, unknown = (result.get("totalParts", ""), result.get("totalDeliveredParts", ""), result.get("totalUnDeliveredParts", ""), result.get("totalUnKnownParts", ""))
        elif result and message.provider == "candoo":
            provider_code = str(result.get("status", ""))
            internal_status = map_candoo.get(provider_code, "pending")
            total = 1
            delivered = 1 if internal_status == "successful" else 0
            undelivered = 1 if internal_status == "unsuccessful" else 0
            unknown = 1 if internal_status == "pending" else 0
        yield {"checked_at": checked_at, "campaign_id": campaign_id, "processed_campaign_id": message.processed_campaign_id, "source": message.source, "job_ids": ",".join(map(str, sorted(message.job_ids))), "job_executed_at": ";".join(message.job_executed_at), "job_errors": " | ".join(message.job_errors), "tracking_id": message.tracking_id, "provider": message.provider, "provider_lookup_id": message.lookup_id or "", "provider_result_found": bool(result), "provider_status_code": provider_code, "provider_status_text": provider_text, "internal_status": internal_status, "total_parts": total, "delivered_parts": delivered, "undelivered_parts": undelivered, "unknown_parts": unknown, "raw_provider_result": json.dumps(result, ensure_ascii=False, sort_keys=True) if result else ""}


def write_report(path: Path, output_format: str, rows: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if output_format == "jsonl":
        with path.open("x", encoding="utf-8") as handle:
            for row in rows: handle.write(json.dumps(row, ensure_ascii=False, default=str) + "\n")
        return
    fieldnames = list(rows[0]) if rows else ["checked_at", "campaign_id", "tracking_id", "provider", "provider_result_found"]
    with path.open("x", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=fieldnames)
        writer.writeheader(); writer.writerows(rows)


def main(argv: Sequence[str] | None = None) -> int:
    load_repository_env()
    args = parse_args(argv)
    os.umask(0o077)
    try:
        import requests
    except ImportError as exc:
        raise RuntimeError("requests is required; install scripts/requirements.txt") from exc
    with connect_read_only(args) as connection, connection.cursor() as cur:
        messages = fetch_messages(cur, args.campaign_id, args.include_unjobbed_sent_sms)
    if not messages:
        print(f"campaign_id={args.campaign_id}: no SMS status jobs or sent_sms rows found", file=sys.stderr)
        return 1
    by_provider: dict[str, list[str]] = defaultdict(list)
    for message in messages:
        if message.provider not in {"payamsms", "candoo"}:
            print(f"Skipping tracking_id={message.tracking_id}: unsupported provider {message.provider!r}", file=sys.stderr); continue
        if message.lookup_id:
            by_provider[message.provider].append(message.lookup_id)
    session = requests.Session(); session.headers["User-Agent"] = "yamata-sms-provider-status-export/1"
    provider_results: dict[str, dict[str, dict[str, Any]]] = {}
    if by_provider.get("payamsms"):
        provider_results["payamsms"] = payam_statuses(session, payam_config(), sorted(set(by_provider["payamsms"])), args.timeout, args.request_delay)
    if by_provider.get("candoo"):
        provider_results["candoo"] = candoo_statuses(session, sorted(set(by_provider["candoo"])), args.timeout, args.request_delay)
    rows = list(report_rows(args.campaign_id, messages, provider_results))
    write_report(args.output, args.format, rows)
    found = sum(1 for row in rows if row["provider_result_found"])
    no_lookup = sum(1 for row in rows if not row["provider_lookup_id"])
    print(f"Wrote {len(rows)} rows to {args.output} (provider results={found}, no provider lookup ID={no_lookup}).")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
