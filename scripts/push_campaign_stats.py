#!/usr/bin/env python3
"""
Recalculate and push campaign statistics to Jazebeh.

For each supplied campaign ID the script:
  1. Resolves the processed_campaign_id from processed_campaigns.
  2. Aggregates stats from the platform-specific status-result table
     (sms_status_results / bale_status_results / splus_status_results /
      rubika_status_results), mirroring the Go AggregateByCampaign query.
  3. POSTs the statistics to the Jazebeh bot API
     (POST /api/v1/bot/campaigns/{id}/statistics).

Usage:
  python push_campaign_stats.py \\
      --platform sms \\
      --campaign-ids 101 102 103 \\
      --db-host 127.0.0.1 --db-port 5432 --db-name yamata \\
      --db-user postgres \\
      --jazebeh-domain https://jazebeh.ir \\
      --bot-username botuser

Set DB_PASSWORD and BOT_PASSWORD in the environment, or enter them at the
interactive prompts. Secrets are never accepted on the command line.
"""

import argparse
import json
import os
import sys
from guard_environment import environment_banner, require_production_confirmation
from datetime import datetime, timezone

from script_common import (
    read_secret,
    validate_database_port,
    validate_https_origin,
    validate_positive_ids,
)

PLATFORMS = ("sms", "bale", "splus", "rubika")

STATUS_TABLE = {
    "sms": "sms_status_results",
    "bale": "bale_status_results",
    "splus": "splus_status_results",
    "rubika": "rubika_status_results",
}

AGGREGATE_SQL = """
SELECT
    COUNT(*)                                                                     AS aggregated_total_records,
    COALESCE(SUM(CASE WHEN total_parts = total_delivered_parts THEN 1 ELSE 0 END), 0) AS aggregated_total_sent,
    COALESCE(SUM(total_parts),           0)                                      AS aggregated_total_parts,
    COALESCE(SUM(total_delivered_parts), 0)                                      AS aggregated_delivered_parts,
    COALESCE(SUM(total_undelivered_parts), 0)                                    AS aggregated_undelivered,
    COALESCE(SUM(total_unknown_parts),   0)                                      AS aggregated_unknown
FROM {table}
WHERE processed_campaign_id = %s
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Recalculate and push campaign statistics to Jazebeh."
    )
    parser.add_argument(
        "--platform",
        required=True,
        choices=PLATFORMS,
        help="Campaign platform (sms / bale / splus / rubika)",
    )
    parser.add_argument(
        "--campaign-ids",
        required=True,
        nargs="+",
        type=int,
        metavar="ID",
        help="One or more campaign IDs",
    )
    # DB args
    parser.add_argument("--db-host", default="127.0.0.1", help="PostgreSQL host")
    parser.add_argument("--db-port", type=int, default=5432, help="PostgreSQL port")
    parser.add_argument("--db-name", required=True, help="Database name")
    parser.add_argument("--db-user", required=True, help="Database user")
    parser.add_argument("--db-sslmode", default=os.getenv("DB_SSL_MODE", "require"))
    # Jazebeh args
    parser.add_argument(
        "--jazebeh-domain",
        default="https://jazebeh.ir",
        help="Jazebeh API domain (no trailing slash)",
    )
    parser.add_argument("--bot-username", required=True, help="Jazebeh bot username")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Compute stats but do not push to Jazebeh",
    )
    parser.add_argument(
        "--execute",
        action="store_true",
        help="confirm production publish; default is dry-run",
    )
    args = parser.parse_args()
    if args.execute and args.dry_run:
        parser.error("--execute and --dry-run cannot be combined")
    args.dry_run = not args.execute
    if args.execute:
        require_production_confirmation(True)
    else:
        environment_banner()
    args.jazebeh_domain = validate_https_origin(
        parser, "--jazebeh-domain", args.jazebeh_domain
    )
    validate_database_port(parser, args.db_port)
    validate_positive_ids(parser, "--campaign-ids", args.campaign_ids)
    return args


# ---------------------------------------------------------------------------
# Jazebeh helpers
# ---------------------------------------------------------------------------


def jazebeh_login(domain: str, username: str, password: str) -> str:
    try:
        import requests
    except ImportError as exc:
        raise RuntimeError(
            "requests is required; install scripts/requirements.txt"
        ) from exc
    url = domain.rstrip("/") + "/api/v1/bot/auth/login"
    resp = requests.post(
        url,
        json={"username": username, "password": password},
        timeout=30,
    )
    resp.raise_for_status()
    body = resp.json()
    if not body.get("success"):
        raise RuntimeError("Jazebeh login was rejected")
    token = body["data"]["session"]["access_token"]
    if not token:
        raise RuntimeError("Jazebeh login returned empty access token")
    return token


def jazebeh_push_statistics(
    domain: str, token: str, campaign_id: int, stats: dict
) -> None:
    try:
        import requests
    except ImportError as exc:
        raise RuntimeError(
            "requests is required; install scripts/requirements.txt"
        ) from exc
    url = domain.rstrip("/") + f"/api/v1/bot/campaigns/{campaign_id}/statistics"
    resp = requests.post(
        url,
        json={"statistics": stats},
        headers={"Authorization": f"Bearer {token}"},
        timeout=30,
    )
    resp.raise_for_status()
    body = resp.json()
    if not body.get("success"):
        raise RuntimeError(f"Push statistics failed for campaign {campaign_id}")


# ---------------------------------------------------------------------------
# DB helpers
# ---------------------------------------------------------------------------


def connect_db(args: argparse.Namespace):
    try:
        import psycopg2
    except ImportError as exc:
        raise RuntimeError(
            "psycopg2 is required; install scripts/requirements.txt"
        ) from exc
    return psycopg2.connect(
        host=args.db_host,
        port=args.db_port,
        dbname=args.db_name,
        user=args.db_user,
        password=args.db_password,
        sslmode=args.db_sslmode,
        connect_timeout=10,
    )


def fetch_processed_campaign_id(cur, campaign_id: int) -> int | None:
    cur.execute(
        "SELECT id FROM processed_campaigns WHERE campaign_id = %s ORDER BY id DESC LIMIT 1",
        (campaign_id,),
    )
    row = cur.fetchone()
    return row[0] if row else None


def aggregate_stats(cur, platform: str, processed_campaign_id: int) -> dict | None:
    table = STATUS_TABLE[platform]
    cur.execute(AGGREGATE_SQL.format(table=table), (processed_campaign_id,))
    row = cur.fetchone()
    if row is None:
        return None

    (
        total_records,
        total_sent,
        total_parts,
        delivered_parts,
        undelivered,
        unknown,
    ) = row

    if total_records == 0:
        return None

    return {
        "aggregatedTotalRecords": total_records,
        "aggregatedTotalSent": total_sent,
        "aggregatedTotalParts": total_parts,
        "aggregatedTotalDeliveredParts": delivered_parts,
        "aggregatedTotalUnDeliveredParts": undelivered,
        "aggregatedTotalUnKnownParts": unknown,
        "updatedAt": datetime.now(tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    args = parse_args()
    args.db_password = read_secret("DB_PASSWORD", "Database password: ")

    print(f"Platform : {args.platform}")
    print(f"Campaigns: {args.campaign_ids}")
    print(f"Dry-run  : {args.dry_run}")

    conn = connect_db(args)
    conn.autocommit = True
    cur = conn.cursor()

    token: str | None = None
    if not args.dry_run:
        args.bot_password = read_secret("BOT_PASSWORD", "Jazebeh bot password: ")
        print("Logging in to Jazebeh...")
        token = jazebeh_login(args.jazebeh_domain, args.bot_username, args.bot_password)
        print("Login successful.")

    success_count = 0
    skip_count = 0
    error_count = 0

    for campaign_id in args.campaign_ids:
        print(f"\n--- campaign_id={campaign_id} ---")

        processed_campaign_id = fetch_processed_campaign_id(cur, campaign_id)
        if processed_campaign_id is None:
            print(f"  SKIP: no processed_campaign found for campaign_id={campaign_id}")
            skip_count += 1
            continue

        print(f"  processed_campaign_id={processed_campaign_id}")

        stats = aggregate_stats(cur, args.platform, processed_campaign_id)
        if stats is None:
            print(
                f"  SKIP: no status results yet for processed_campaign_id={processed_campaign_id}"
            )
            skip_count += 1
            continue

        print(f"  stats: {json.dumps(stats, indent=4)}")

        if args.dry_run:
            print("  DRY-RUN: skipping push to Jazebeh")
            success_count += 1
            continue

        if stats["aggregatedTotalSent"] <= 0:
            print("  SKIP: aggregatedTotalSent=0, nothing to push")
            skip_count += 1
            continue

        try:
            assert token is not None
            jazebeh_push_statistics(args.jazebeh_domain, token, campaign_id, stats)
            print(f"  OK: statistics pushed for campaign_id={campaign_id}")
            success_count += 1
        except Exception as exc:
            print(f"  ERROR: {exc}", file=sys.stderr)
            error_count += 1

    cur.close()
    conn.close()

    print(f"\nDone. success={success_count} skipped={skip_count} errors={error_count}")
    if error_count > 0:
        sys.exit(1)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
