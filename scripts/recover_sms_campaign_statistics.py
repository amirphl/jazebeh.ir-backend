#!/usr/bin/env python3
"""Safely recover and publish SMS statistics for a fixed campaign set.

The SMS scheduler normally fetches PayamSMS delivery statuses, upserts them in
``sms_status_results``, aggregates that table, stores a copy on the current
``processed_campaigns`` row, and publishes the aggregate to Jazebeh.

This one-off recovery tool deliberately avoids scheduler/database mutations:

* PostgreSQL is opened in read-only mode.
* It never creates or executes status jobs, updates processed campaigns, or
  sends SMS messages.
* Fresh PayamSMS results are overlaid on persisted status rows in memory, which
  is equivalent to the scheduler's upsert for the purpose of aggregation.
* It refuses to publish unless every sent-SMS tracking ID has a status.
* Publishing is opt-in with ``--push`` and a second confirmation.

The only state-changing request is the requested Jazebeh statistics POST.
Without ``--push`` the script fetches statuses and prints a preview only.

Secrets are accepted only through environment variables or hidden prompts:
``DB_PASSWORD``, ``PAYAM_SMS_PASSWORD``, ``PAYAM_SMS_ROOT_ACCESS_TOKEN`` (when
required by the provider), and ``BOT_PASSWORD`` (only with ``--push``).
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.parse
from collections.abc import Iterable, Iterator, Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

from script_common import (
    read_secret,
    require_live_provider_mode,
    validate_database_port,
    validate_https_origin,
)

# Deliberately fixed: this utility must not be repurposed accidentally for a
# broader campaign set.
CAMPAIGN_IDS = (935, 944, 945, 943, 937, 933, 942)
STATUS_BATCH_SIZE = 200  # Matches smsSendBatchSize in the Go scheduler.
RETRYABLE_HTTP_STATUSES = frozenset((429, 500, 502, 503, 504))
MAX_STATUS_ATTEMPTS = 5
MAX_UNAUTHORIZED_ATTEMPTS = 3


@dataclass(frozen=True)
class ProcessedCampaign:
    campaign_id: int
    processed_campaign_id: int


@dataclass(frozen=True)
class CampaignSnapshot:
    processed: ProcessedCampaign
    all_tracking_ids: tuple[str, ...]
    provider_tracking_ids: tuple[str, ...]
    persisted_statuses: dict[str, dict[str, Any]]


@dataclass(frozen=True)
class PreparedStatistics:
    processed: ProcessedCampaign
    stats: dict[str, Any]
    requested_count: int
    fresh_count: int
    persisted_count: int


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Fetch PayamSMS statuses and recover statistics for campaigns "
            + ", ".join(str(value) for value in CAMPAIGN_IDS)
        )
    )
    parser.add_argument(
        "--db-host", default=os.getenv("DB_HOST", "127.0.0.1"), help="PostgreSQL host"
    )
    parser.add_argument(
        "--db-port",
        type=int,
        default=int(os.getenv("DB_PORT", "5432")),
        help="PostgreSQL port",
    )
    parser.add_argument("--db-name", default=os.getenv("DB_NAME", ""))
    parser.add_argument("--db-user", default=os.getenv("DB_USER", ""))
    parser.add_argument("--db-sslmode", default=os.getenv("DB_SSL_MODE", "require"))
    parser.add_argument(
        "--jazebeh-domain",
        default=os.getenv("BOT_API_DOMAIN", "https://jazebeh.ir"),
        help="Jazebeh HTTPS origin",
    )
    parser.add_argument("--bot-username", default=os.getenv("BOT_USERNAME", ""))
    parser.add_argument(
        "--batch-size",
        type=int,
        default=STATUS_BATCH_SIZE,
        help=f"PayamSMS IDs per request (1-{STATUS_BATCH_SIZE})",
    )
    parser.add_argument(
        "--request-delay",
        type=float,
        default=1.0,
        help="Delay between PayamSMS status requests in seconds (default: 1)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=60.0,
        help="HTTP timeout in seconds (default: 60)",
    )
    parser.add_argument(
        "--push",
        action="store_true",
        help="Publish prepared statistics to Jazebeh; default is preview only",
    )
    parser.add_argument(
        "--yes",
        action="store_true",
        help="With --push, skip the interactive PUSH confirmation",
    )
    args = parser.parse_args(argv)

    validate_database_port(parser, args.db_port)
    args.jazebeh_domain = validate_https_origin(
        parser, "--jazebeh-domain", args.jazebeh_domain
    )
    if not args.db_name.strip():
        parser.error("--db-name or DB_NAME is required")
    if not args.db_user.strip():
        parser.error("--db-user or DB_USER is required")
    if not 1 <= args.batch_size <= STATUS_BATCH_SIZE:
        parser.error(f"--batch-size must be between 1 and {STATUS_BATCH_SIZE}")
    if args.request_delay < 0:
        parser.error("--request-delay must be non-negative")
    if args.timeout <= 0:
        parser.error("--timeout must be greater than zero")
    if args.yes and not args.push:
        parser.error("--yes is only valid with --push")
    if args.push and not args.bot_username.strip():
        parser.error("--bot-username or BOT_USERNAME is required with --push")
    return args


def require_https_endpoint(environment_name: str, default: str) -> str:
    value = os.getenv(environment_name, default).strip()
    try:
        parsed = urllib.parse.urlparse(value)
        port = parsed.port
    except ValueError as exc:
        raise RuntimeError(f"{environment_name} is not a valid URL") from exc
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
        raise RuntimeError(
            f"{environment_name} must be an HTTPS URL without credentials or query"
        )
    return value


def require_payam_config() -> dict[str, str]:
    config = {
        "token_url": require_https_endpoint(
            "PAYAM_SMS_TOKEN_URL", "https://www.payamsms.com/auth/oauth/token"
        ),
        "status_url": require_https_endpoint(
            "PAYAM_SMS_STATUS_URL",
            "https://www.payamsms.com/report/webservice/status",
        ),
        "system_name": os.getenv("PAYAM_SMS_SYSTEM_NAME", "").strip(),
        "username": os.getenv("PAYAM_SMS_USERNAME", "").strip(),
        "password": read_secret("PAYAM_SMS_PASSWORD", "PayamSMS password: "),
        "scope": os.getenv("PAYAM_SMS_SCOPE", "webservice").strip() or "webservice",
        "grant_type": os.getenv("PAYAM_SMS_GRANT_TYPE", "password").strip()
        or "password",
        "root_access_token": os.getenv("PAYAM_SMS_ROOT_ACCESS_TOKEN", "").strip(),
    }
    missing = [key for key in ("system_name", "username") if not config[key]]
    if missing:
        names = ", ".join("PAYAM_SMS_" + key.upper() for key in missing)
        raise RuntimeError(f"missing required environment variable(s): {names}")
    return config


def connect_read_only_db(args: argparse.Namespace):
    try:
        import psycopg2
    except ImportError as exc:
        raise RuntimeError(
            "psycopg2 is required; install scripts/requirements.txt"
        ) from exc

    connection = psycopg2.connect(
        host=args.db_host,
        port=args.db_port,
        dbname=args.db_name,
        user=args.db_user,
        password=args.db_password,
        sslmode=args.db_sslmode,
        connect_timeout=10,
        application_name="recover_sms_campaign_statistics_readonly",
        options="-c statement_timeout=60000 -c default_transaction_read_only=on",
    )
    connection.set_session(readonly=True, autocommit=True)
    return connection


def fetch_current_processed_campaign(cur, campaign_id: int) -> ProcessedCampaign | None:
    cur.execute(
        """
        SELECT id
        FROM processed_campaigns
        WHERE campaign_id = %s AND is_current
        LIMIT 1
        """,
        (campaign_id,),
    )
    row = cur.fetchone()
    if row is None:
        return None
    return ProcessedCampaign(campaign_id=campaign_id, processed_campaign_id=int(row[0]))


def fetch_campaign_snapshot(cur, processed: ProcessedCampaign) -> CampaignSnapshot:
    cur.execute(
        """
        SELECT DISTINCT tracking_id, (phone_number <> '') AS was_submitted
        FROM sent_sms
        WHERE processed_campaign_id = %s AND BTRIM(tracking_id) <> ''
        ORDER BY tracking_id
        """,
        (processed.processed_campaign_id,),
    )
    submitted_by_tracking: dict[str, bool] = {}
    for raw_tracking_id, was_submitted in cur.fetchall():
        tracking_id = str(raw_tracking_id).strip()
        submitted_by_tracking[tracking_id] = submitted_by_tracking.get(
            tracking_id, False
        ) or bool(was_submitted)

    cur.execute(
        """
        SELECT tracking_id, server_id, total_parts, total_delivered_parts,
               total_undelivered_parts, total_unknown_parts, status
        FROM sms_status_results
        WHERE processed_campaign_id = %s
        """,
        (processed.processed_campaign_id,),
    )
    persisted_statuses: dict[str, dict[str, Any]] = {}
    for row in cur.fetchall():
        tracking_id = str(row[0]).strip()
        if not tracking_id:
            continue
        persisted_statuses[tracking_id] = {
            "tracking_id": tracking_id,
            "server_id": row[1],
            "total_parts": None if row[2] is None else int(row[2]),
            "total_delivered_parts": None if row[3] is None else int(row[3]),
            "total_undelivered_parts": None if row[4] is None else int(row[4]),
            "total_unknown_parts": None if row[5] is None else int(row[5]),
            "status": row[6],
        }

    all_tracking_ids = tuple(sorted(submitted_by_tracking))
    provider_tracking_ids = tuple(
        tracking_id
        for tracking_id in all_tracking_ids
        if submitted_by_tracking[tracking_id]
    )
    return CampaignSnapshot(
        processed=processed,
        all_tracking_ids=all_tracking_ids,
        provider_tracking_ids=provider_tracking_ids,
        persisted_statuses=persisted_statuses,
    )


def chunks(values: Sequence[str], size: int) -> Iterator[tuple[str, ...]]:
    for start in range(0, len(values), size):
        yield tuple(values[start : start + size])


def payam_login(session, config: Mapping[str, str], timeout: float) -> str:
    headers = {"Content-Type": "application/x-www-form-urlencoded"}
    if config["root_access_token"]:
        headers["Authorization"] = "Basic " + config["root_access_token"]
    form_data = {
        "systemName": config["system_name"],
        "username": config["username"],
        "password": config["password"],
        "scope": config["scope"],
        "grant_type": config["grant_type"],
    }
    for attempt in range(MAX_STATUS_ATTEMPTS):
        try:
            response = session.post(
                config["token_url"],
                data=form_data,
                headers=headers,
                timeout=timeout,
            )
        except Exception as exc:
            if attempt + 1 >= MAX_STATUS_ATTEMPTS:
                raise RuntimeError("PayamSMS token request failed") from exc
            time.sleep(retry_delay(attempt))
            continue
        if response.status_code in RETRYABLE_HTTP_STATUSES:
            if attempt + 1 >= MAX_STATUS_ATTEMPTS:
                raise RuntimeError(
                    f"PayamSMS token HTTP {response.status_code} after retries"
                )
            time.sleep(retry_delay(attempt))
            continue
        if response.status_code < 200 or response.status_code >= 300:
            raise RuntimeError(f"PayamSMS token HTTP {response.status_code}")
        try:
            body = response.json()
        except ValueError as exc:
            raise RuntimeError("PayamSMS token response was not valid JSON") from exc
        token = body.get("access_token", "") if isinstance(body, dict) else ""
        if not isinstance(token, str) or not token:
            raise RuntimeError("PayamSMS token response did not contain access_token")
        return token
    raise RuntimeError(
        "PayamSMS token retry loop ended unexpectedly"
    )  # pragma: no cover


def retry_delay(attempt: int) -> int:
    return min(2**attempt, 120)


def parse_nonnegative_integer(item: Mapping[str, Any], key: str) -> int:
    value = item.get(key, 0)
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise RuntimeError(f"PayamSMS status item has invalid {key}")
    return value


def parse_status_items(
    payload: Any, requested_tracking_ids: set[str]
) -> tuple[dict[str, dict[str, Any]], int]:
    if not isinstance(payload, list):
        raise RuntimeError("PayamSMS status response was not a JSON array")
    statuses: dict[str, dict[str, Any]] = {}
    ignored = 0
    for item in payload:
        if not isinstance(item, dict):
            raise RuntimeError("PayamSMS status response contained a non-object item")
        raw_tracking_id = item.get("customerId", "")
        if not isinstance(raw_tracking_id, str):
            raise RuntimeError("PayamSMS status item has a non-string customerId")
        tracking_id = raw_tracking_id.strip()
        if not tracking_id or tracking_id not in requested_tracking_ids:
            ignored += 1
            continue
        server_id = item.get("serverId")
        if server_id is not None and not isinstance(server_id, (str, int)):
            raise RuntimeError("PayamSMS status item has an invalid serverId")
        status = item.get("status", "")
        if not isinstance(status, str):
            raise RuntimeError("PayamSMS status item has a non-string status")
        statuses[tracking_id] = {
            "tracking_id": tracking_id,
            "server_id": None if server_id is None else str(server_id),
            "total_parts": parse_nonnegative_integer(item, "totalParts"),
            "total_delivered_parts": parse_nonnegative_integer(
                item, "totalDeliveredParts"
            ),
            "total_undelivered_parts": parse_nonnegative_integer(
                item, "totalUnDeliveredParts"
            ),
            "total_unknown_parts": parse_nonnegative_integer(item, "totalUnKnownParts"),
            "status": status,
        }
    return statuses, ignored


def fetch_status_batch(
    session,
    config: Mapping[str, str],
    token: str,
    tracking_ids: Sequence[str],
    timeout: float,
) -> tuple[dict[str, dict[str, Any]], str, int]:
    requested = set(tracking_ids)
    unauthorized_attempt = 0
    current_token = token
    while unauthorized_attempt < MAX_UNAUTHORIZED_ATTEMPTS:
        for attempt in range(MAX_STATUS_ATTEMPTS):
            try:
                response = session.get(
                    config["status_url"],
                    params=[("byCustomer", "true")]
                    + [("ids", tracking_id) for tracking_id in tracking_ids],
                    headers={"Authorization": "Bearer " + current_token},
                    timeout=timeout,
                )
            except Exception as exc:
                if attempt + 1 >= MAX_STATUS_ATTEMPTS:
                    raise RuntimeError("PayamSMS status request failed") from exc
                time.sleep(retry_delay(attempt))
                continue

            if response.status_code == 401:
                break
            if response.status_code in RETRYABLE_HTTP_STATUSES:
                if attempt + 1 >= MAX_STATUS_ATTEMPTS:
                    raise RuntimeError(
                        f"PayamSMS status HTTP {response.status_code} after retries"
                    )
                time.sleep(retry_delay(attempt))
                continue
            if response.status_code < 200 or response.status_code >= 300:
                raise RuntimeError(f"PayamSMS status HTTP {response.status_code}")
            try:
                payload = response.json()
            except ValueError as exc:
                raise RuntimeError(
                    "PayamSMS status response was not valid JSON"
                ) from exc
            statuses, ignored = parse_status_items(payload, requested)
            return statuses, current_token, ignored
        else:  # pragma: no cover - the retry loop always returns or raises
            raise RuntimeError("PayamSMS status retry loop ended unexpectedly")

        unauthorized_attempt += 1
        if unauthorized_attempt >= MAX_UNAUTHORIZED_ATTEMPTS:
            raise RuntimeError(
                "PayamSMS status remained unauthorized after token refresh"
            )
        time.sleep(retry_delay(unauthorized_attempt - 1))
        current_token = payam_login(session, config, timeout)

    raise RuntimeError("PayamSMS status authorization loop ended unexpectedly")


def fetch_fresh_statuses(
    session,
    config: Mapping[str, str],
    token: str,
    tracking_ids: Sequence[str],
    batch_size: int,
    request_delay: float,
    timeout: float,
) -> tuple[dict[str, dict[str, Any]], str, int]:
    fresh: dict[str, dict[str, Any]] = {}
    ignored_count = 0
    batches = list(chunks(tracking_ids, batch_size))
    current_token = token
    for index, batch in enumerate(batches, start=1):
        statuses, current_token, ignored = fetch_status_batch(
            session, config, current_token, batch, timeout
        )
        fresh.update(statuses)
        ignored_count += ignored
        print(
            f"    PayamSMS batch {index}/{len(batches)}: "
            f"requested={len(batch)} returned={len(statuses)}"
        )
        if index < len(batches) and request_delay:
            time.sleep(request_delay)
    return fresh, current_token, ignored_count


def aggregate_statuses(statuses: Iterable[Mapping[str, Any]]) -> dict[str, Any]:
    rows = list(statuses)

    def sql_sum(key: str) -> int:
        # PostgreSQL SUM ignores NULL and the scheduler wraps the result in
        # COALESCE(..., 0).
        return sum(int(row[key]) for row in rows if row[key] is not None)

    return {
        "aggregatedTotalRecords": len(rows),
        # This deliberately mirrors the scheduler SQL, including 0 == 0.
        "aggregatedTotalSent": sum(
            1
            for row in rows
            if row["total_parts"] is not None
            and row["total_delivered_parts"] is not None
            and row["total_parts"] == row["total_delivered_parts"]
        ),
        "aggregatedTotalParts": sql_sum("total_parts"),
        "aggregatedTotalDeliveredParts": sql_sum("total_delivered_parts"),
        "aggregatedTotalUnDeliveredParts": sql_sum("total_undelivered_parts"),
        "aggregatedTotalUnKnownParts": sql_sum("total_unknown_parts"),
        "updatedAt": datetime.now(tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }


def jazebeh_login(
    session, domain: str, username: str, password: str, timeout: float
) -> str:
    response = session.post(
        domain + "/api/v1/bot/auth/login",
        json={"username": username, "password": password},
        timeout=timeout,
    )
    if response.status_code < 200 or response.status_code >= 300:
        raise RuntimeError(f"Jazebeh login HTTP {response.status_code}")
    try:
        body = response.json()
    except ValueError as exc:
        raise RuntimeError("Jazebeh login response was not valid JSON") from exc
    if not isinstance(body, dict) or not body.get("success"):
        raise RuntimeError("Jazebeh login was rejected")
    try:
        token = body["data"]["session"]["access_token"]
    except (KeyError, TypeError) as exc:
        raise RuntimeError(
            "Jazebeh login response did not contain an access token"
        ) from exc
    if not isinstance(token, str) or not token:
        raise RuntimeError("Jazebeh login response returned an empty access token")
    return token


def push_statistics(
    session,
    domain: str,
    token: str,
    campaign_id: int,
    stats: Mapping[str, Any],
    timeout: float,
) -> None:
    response = session.post(
        domain + f"/api/v1/bot/campaigns/{campaign_id}/statistics",
        json={"statistics": dict(stats)},
        headers={"Authorization": "Bearer " + token},
        timeout=timeout,
    )
    if response.status_code < 200 or response.status_code >= 300:
        raise RuntimeError(
            f"Jazebeh statistics HTTP {response.status_code} for campaign {campaign_id}"
        )
    # The Go scheduler treats any 2xx response as success; do the same.


def confirm_push(args: argparse.Namespace) -> None:
    if not args.push or args.yes:
        return
    if not sys.stdin.isatty():
        raise RuntimeError("--push requires --yes when stdin is not interactive")
    expected = "PUSH"
    entered = input(
        "Type PUSH to publish statistics for the fixed campaign list "
        f"{list(CAMPAIGN_IDS)}: "
    )
    if entered != expected:
        raise RuntimeError("push confirmation did not match; nothing was published")


def require_requests():
    try:
        import requests
    except ImportError as exc:
        raise RuntimeError(
            "requests is required; install scripts/requirements.txt"
        ) from exc
    return requests


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    require_live_provider_mode()
    os.umask(0o077)
    args.db_password = read_secret("DB_PASSWORD", "Database password: ")
    payam_config = require_payam_config()
    requests = require_requests()

    print(f"Campaigns: {list(CAMPAIGN_IDS)}")
    print(f"Mode: {'PUSH' if args.push else 'PREVIEW (no statistics POST)'}")
    print("Database: read-only")

    connection = connect_read_only_db(args)
    session = requests.Session()
    session.headers.update({"User-Agent": "yamata-sms-statistics-recovery/1"})
    prepared: list[PreparedStatistics] = []
    errors = 0
    try:
        with connection.cursor() as cur:
            snapshots: list[CampaignSnapshot] = []
            for campaign_id in CAMPAIGN_IDS:
                processed = fetch_current_processed_campaign(cur, campaign_id)
                if processed is None:
                    print(
                        f"campaign_id={campaign_id}: ERROR no current processed campaign"
                    )
                    errors += 1
                    continue
                snapshot = fetch_campaign_snapshot(cur, processed)
                if not snapshot.all_tracking_ids:
                    print(
                        f"campaign_id={campaign_id}: ERROR processed_campaign_id="
                        f"{processed.processed_campaign_id} has no sent_sms tracking IDs"
                    )
                    errors += 1
                    continue
                snapshots.append(snapshot)

            if not snapshots:
                return 1

            print("Obtaining PayamSMS access token...")
            payam_token = payam_login(session, payam_config, args.timeout)
            for snapshot in snapshots:
                campaign_id = snapshot.processed.campaign_id
                print(
                    f"campaign_id={campaign_id}: processed_campaign_id="
                    f"{snapshot.processed.processed_campaign_id} "
                    f"tracking_ids={len(snapshot.all_tracking_ids)} "
                    f"provider_ids={len(snapshot.provider_tracking_ids)}"
                )
                try:
                    fresh, payam_token, ignored = fetch_fresh_statuses(
                        session,
                        payam_config,
                        payam_token,
                        snapshot.provider_tracking_ids,
                        args.batch_size,
                        args.request_delay,
                        args.timeout,
                    )
                    combined = dict(snapshot.persisted_statuses)
                    combined.update(fresh)
                    missing = sorted(set(snapshot.all_tracking_ids) - set(combined))
                    if missing:
                        print(
                            f"  ERROR: incomplete statuses; missing={len(missing)}. "
                            "Statistics will not be published."
                        )
                        errors += 1
                        continue
                    if ignored:
                        print(
                            f"  WARNING: ignored {ignored} empty or unexpected provider item(s)"
                        )
                    stats = aggregate_statuses(combined.values())
                    print("  stats=" + json.dumps(stats, sort_keys=True))
                    if stats["aggregatedTotalRecords"] == 0:
                        print("  ERROR: aggregate contains no records")
                        errors += 1
                        continue
                    if stats["aggregatedTotalSent"] <= 0:
                        print(
                            "  SKIP: scheduler would not publish aggregatedTotalSent=0"
                        )
                        continue
                    prepared.append(
                        PreparedStatistics(
                            processed=snapshot.processed,
                            stats=stats,
                            requested_count=len(snapshot.provider_tracking_ids),
                            fresh_count=len(fresh),
                            persisted_count=len(snapshot.persisted_statuses),
                        )
                    )
                except Exception as exc:
                    print(f"  ERROR: {exc}", file=sys.stderr)
                    errors += 1

            if not args.push:
                print(
                    f"Preview complete. publishable={len(prepared)} errors={errors}; "
                    "no statistics were posted."
                )
                return 1 if errors else 0

            if not prepared:
                print("No complete campaign statistics are eligible to publish.")
                return 1 if errors else 0

            confirm_push(args)
            bot_password = read_secret("BOT_PASSWORD", "Jazebeh bot password: ")
            bot_token = jazebeh_login(
                session,
                args.jazebeh_domain,
                args.bot_username,
                bot_password,
                args.timeout,
            )

            published = 0
            for item in prepared:
                # Re-check election immediately before the only intended write.
                current = fetch_current_processed_campaign(
                    cur, item.processed.campaign_id
                )
                if current != item.processed:
                    print(
                        f"campaign_id={item.processed.campaign_id}: ERROR current "
                        "processed campaign changed; refusing stale publication"
                    )
                    errors += 1
                    continue
                try:
                    push_statistics(
                        session,
                        args.jazebeh_domain,
                        bot_token,
                        item.processed.campaign_id,
                        item.stats,
                        args.timeout,
                    )
                    print(
                        f"campaign_id={item.processed.campaign_id}: OK statistics published"
                    )
                    published += 1
                except Exception as exc:
                    print(f"campaign_id={item.processed.campaign_id}: ERROR {exc}")
                    errors += 1
            print(
                f"Push complete. published={published} eligible={len(prepared)} errors={errors}"
            )
            return 1 if errors else 0
    finally:
        session.close()
        connection.close()


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (KeyboardInterrupt, EOFError):
        print("Cancelled; no further statistics will be published.", file=sys.stderr)
        raise SystemExit(130) from None
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
