#!/usr/bin/env python3
"""Print fresh, read-only SMS delivery statistics for one campaign.

The script reads the campaign's existing sent-SMS/status-job batches, fetches
their current status from Candoo and/or PayamSMS, and prints a JSON report. It
opens PostgreSQL in a read-only transaction and never creates jobs, sends SMS,
or writes campaign/status data.

Required environment variables depend on the provider used by the campaign:
DB_NAME, DB_USER, DB_PASSWORD; PAYAM_SMS_* and/or CANDOO_SMS_API_KEY.
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
from collections import Counter, defaultdict
from collections.abc import Mapping, Sequence
from datetime import datetime, timezone
from typing import Any

from export_sms_campaign_provider_statuses import (
    candoo_status_map,
    candoo_statuses,
    connect_read_only,
    fetch_messages,
    load_repository_env,
    payam_config,
    payam_statuses,
)
from script_common import validate_database_port, validate_positive_ids


logger = logging.getLogger("yamata.sms_statistics_report")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("campaign_id", type=int, help="campaigns.id to inspect")
    parser.add_argument("--db-host", default=os.getenv("DB_HOST", "127.0.0.1"))
    parser.add_argument("--db-port", type=int, default=int(os.getenv("DB_PORT", "5432")))
    parser.add_argument("--db-name", default=os.getenv("DB_NAME", ""))
    parser.add_argument("--db-user", default=os.getenv("DB_USER", ""))
    parser.add_argument("--db-sslmode", default=os.getenv("DB_SSL_MODE", "require"))
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument(
        "--requests-per-second",
        type=float,
        default=10.0,
        help="target provider request rate; backs off on provider throttling (default: 10)",
    )
    parser.add_argument(
        "--request-delay",
        type=float,
        default=0.0,
        help="extra delay after each successful request in seconds (default: 0)",
    )
    parser.add_argument("--verbose", action="store_true", help="log progress for every provider batch")
    parser.add_argument(
        "--include-unjobbed-sent-sms",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="include submitted messages which have no status job (default: true)",
    )
    args = parser.parse_args(argv)
    validate_positive_ids(parser, "campaign_id", [args.campaign_id])
    validate_database_port(parser, args.db_port)
    if not args.db_name.strip() or not args.db_user.strip():
        parser.error("--db-name/DB_NAME and --db-user/DB_USER are required")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.requests_per_second <= 0:
        parser.error("--requests-per-second must be positive")
    if args.request_delay < 0:
        parser.error("--request-delay must be non-negative")
    return args


def provider_request_delay(args: argparse.Namespace) -> float:
    """Use a conservative, evenly spaced request cadence for both providers."""
    return max(args.request_delay, 1.0 / args.requests_per_second)


def as_nonnegative_int(item: Mapping[str, Any], key: str) -> int:
    value = item.get(key)
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise RuntimeError(f"provider status has invalid {key}: {value!r}")
    return value


def fresh_status_rows(messages, results: Mapping[str, Mapping[str, Mapping[str, Any]]]):
    """Return scheduler-shaped rows, plus diagnostic counts for missing data."""
    rows: list[dict[str, int]] = []
    missing_lookup = 0
    missing_result = 0
    unsupported = Counter()
    candoo_map = candoo_status_map()

    for message in messages:
        provider = message.provider
        if provider not in {"payamsms", "candoo"}:
            unsupported[provider] += 1
            continue
        if not message.lookup_id:
            missing_lookup += 1
            continue
        item = results.get(provider, {}).get(message.lookup_id)
        if item is None:
            missing_result += 1
            continue
        if provider == "payamsms":
            rows.append(
                {
                    "total_parts": as_nonnegative_int(item, "totalParts"),
                    "delivered_parts": as_nonnegative_int(item, "totalDeliveredParts"),
                    "undelivered_parts": as_nonnegative_int(item, "totalUnDeliveredParts"),
                    "unknown_parts": as_nonnegative_int(item, "totalUnKnownParts"),
                }
            )
            continue

        # This mirrors candooSMSProvider.mapStatus: unmapped values stay pending.
        status = candoo_map.get(str(item.get("status", "")), "pending")
        rows.append(
            {
                "total_parts": 1,
                "delivered_parts": int(status == "successful"),
                "undelivered_parts": int(status == "unsuccessful"),
                "unknown_parts": int(status == "pending"),
            }
        )
    return rows, missing_lookup, missing_result, dict(unsupported)


def aggregate(rows: Sequence[Mapping[str, int]]) -> dict[str, int]:
    """Match SMSStatusResultRepository.AggregateByCampaign's totals."""
    return {
        "aggregatedTotalRecords": len(rows),
        "aggregatedTotalSent": sum(
            row["total_parts"] == row["delivered_parts"] for row in rows
        ),
        "aggregatedTotalParts": sum(row["total_parts"] for row in rows),
        "aggregatedTotalDeliveredParts": sum(row["delivered_parts"] for row in rows),
        "aggregatedTotalUnDeliveredParts": sum(row["undelivered_parts"] for row in rows),
        "aggregatedTotalUnKnownParts": sum(row["unknown_parts"] for row in rows),
    }


def log_provider_batch(
    campaign_id: int,
    provider: str,
    completed: int,
    total: int,
    requested_ids: Sequence[str],
    response_items: Sequence[Mapping[str, Any]],
) -> None:
    """Log a small provider-debug sample without exposing recipient data."""
    output_keys = (
        ("customerId", "status", "totalParts", "totalDeliveredParts", "totalUnDeliveredParts", "totalUnKnownParts")
        if provider == "payamsms"
        else ("customerId", "messageId", "status")
    )
    response_sample = [
        {key: item[key] for key in output_keys if key in item}
        for item in response_items[:3]
        if isinstance(item, Mapping)
    ]
    logger.info(
        "campaign_id=%d provider=%s batch=%d/%d requested=%d returned=%d request_sample=%s response_sample=%s",
        campaign_id,
        provider,
        completed,
        total,
        len(requested_ids),
        len(response_items),
        json.dumps(list(requested_ids[:3]), ensure_ascii=False),
        json.dumps(response_sample, ensure_ascii=False, sort_keys=True),
    )


def main(argv: Sequence[str] | None = None) -> int:
    load_repository_env()
    args = parse_args(argv)
    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    os.umask(0o077)
    try:
        import requests
    except ImportError as exc:
        raise RuntimeError("requests is required; install scripts/requirements.txt") from exc

    logger.info("campaign_id=%d connecting to PostgreSQL in read-only mode", args.campaign_id)
    # connect_read_only prompts for DB_PASSWORD only when it was not exported.
    with connect_read_only(args) as connection, connection.cursor() as cur:
        messages = fetch_messages(cur, args.campaign_id, args.include_unjobbed_sent_sms)
    if not messages:
        raise RuntimeError("campaign has no SMS status-job or sent_sms rows")
    logger.info(
        "campaign_id=%d discovered_messages=%d include_unjobbed_sent_sms=%s",
        args.campaign_id,
        len(messages),
        args.include_unjobbed_sent_sms,
    )

    lookup_ids: dict[str, set[str]] = defaultdict(set)
    for message in messages:
        if message.provider in {"payamsms", "candoo"} and message.lookup_id:
            lookup_ids[message.provider].add(message.lookup_id)

    for provider, ids in sorted(lookup_ids.items()):
        batch_size = 200 if provider == "payamsms" else 100
        logger.info(
            "campaign_id=%d provider=%s unique_lookup_ids=%d request_batches=%d",
            args.campaign_id,
            provider,
            len(ids),
            (len(ids) + batch_size - 1) // batch_size,
        )

    session = requests.Session()
    session.headers["User-Agent"] = "yamata-sms-statistics-report/1"
    request_delay = provider_request_delay(args)
    logger.info(
        "campaign_id=%d provider_request_rate_target=%.2f/s spacing=%.3fs",
        args.campaign_id,
        args.requests_per_second,
        request_delay,
    )
    results: dict[str, dict[str, dict[str, Any]]] = {}
    if lookup_ids["payamsms"]:
        logger.info("campaign_id=%d fetching PayamSMS delivery statuses", args.campaign_id)
        results["payamsms"] = payam_statuses(
            session,
            payam_config(),
            sorted(lookup_ids["payamsms"]),
            args.timeout,
            request_delay,
            lambda completed, total, requested, response: log_provider_batch(
                args.campaign_id,
                "payamsms",
                completed,
                total,
                requested,
                response,
            ),
        )
        logger.info("campaign_id=%d provider=payamsms results=%d", args.campaign_id, len(results["payamsms"]))
    if lookup_ids["candoo"]:
        logger.info("campaign_id=%d fetching Candoo delivery statuses", args.campaign_id)
        results["candoo"] = candoo_statuses(
            session,
            sorted(lookup_ids["candoo"]),
            args.timeout,
            request_delay,
            lambda completed, total, requested, response: log_provider_batch(
                args.campaign_id,
                "candoo",
                completed,
                total,
                requested,
                response,
            ),
        )
        logger.info("campaign_id=%d provider=candoo results=%d", args.campaign_id, len(results["candoo"]))

    rows, missing_lookup, missing_result, unsupported = fresh_status_rows(messages, results)
    report = {
        "campaignID": args.campaign_id,
        "checkedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "database": "read-only",
        "statistics": aggregate(rows),
        "coverage": {
            "messagesDiscovered": len(messages),
            "messagesWithProviderLookupID": sum(len(ids) for ids in lookup_ids.values()),
            "freshProviderResults": len(rows),
            "missingProviderLookupID": missing_lookup,
            "missingProviderResult": missing_result,
            "unsupportedProviderMessages": unsupported,
            "lookupIDsByProvider": {provider: len(ids) for provider, ids in lookup_ids.items()},
        },
        "note": "Statistics include only fresh provider results; no database rows were written.",
    }
    logger.info(
        "campaign_id=%d aggregation complete records=%d delivered_parts=%d unknown_parts=%d missing_provider_results=%d",
        args.campaign_id,
        report["statistics"]["aggregatedTotalRecords"],
        report["statistics"]["aggregatedTotalDeliveredParts"],
        report["statistics"]["aggregatedTotalUnKnownParts"],
        missing_result,
    )
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
