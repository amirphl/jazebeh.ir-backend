#!/usr/bin/env python3
"""
Push fake "all-failed" statistics to Jazebeh for a list of campaigns.

For each campaign ID the script:
  1. Fetches the latest processed_campaign row and reads len(audience_ids)
     as the total number of records.
  2. Builds fake statistics where every message is considered failed:
       aggregatedTotalRecords          = len(audience_ids)
       aggregatedTotalSent             = 0
       aggregatedTotalParts            = len(audience_ids) * parts_per_recipient
       aggregatedTotalDeliveredParts   = 0
       aggregatedTotalUnDeliveredParts = len(audience_ids) * parts_per_recipient
       aggregatedTotalUnKnownParts     = 0
  3. POSTs to POST /api/v1/bot/campaigns/{id}/statistics.

Usage:
  python push_campaign_fake_stats.py \\
      --campaign-ids 101 102 103 \\
      --parts-per-recipient 1 \\
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


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Push fake all-failed statistics to Jazebeh."
    )
    parser.add_argument(
        "--campaign-ids",
        required=True,
        nargs="+",
        type=int,
        metavar="ID",
        help="One or more campaign IDs",
    )
    parser.add_argument(
        "--parts-per-recipient",
        required=True,
        type=int,
        help="Exact message part count for each failed recipient",
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
        help="Compute fake stats but do not push to Jazebeh",
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
    if args.parts_per_recipient <= 0:
        parser.error("--parts-per-recipient must be greater than zero")
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


def fetch_audience_count(cur, campaign_id: int) -> tuple[int, int] | None:
    """
    Returns (processed_campaign_id, audience_count) for the latest processed_campaign
    row matching campaign_id, or None if not found.
    audience_count is cardinality(audience_ids) — the number of targeted audiences.
    """
    cur.execute(
        """
        SELECT id, COALESCE(array_length(audience_ids, 1), 0)
        FROM processed_campaigns
        WHERE campaign_id = %s
        ORDER BY id DESC
        LIMIT 1
        """,
        (campaign_id,),
    )
    row = cur.fetchone()
    if row is None:
        return None
    return row[0], row[1]


def build_fake_stats(total: int, parts_per_recipient: int) -> dict:
    """All-failed stats: every record is undelivered, none sent."""
    return {
        "aggregatedTotalRecords": total,
        "aggregatedTotalSent": 0,
        "aggregatedTotalParts": total * parts_per_recipient,
        "aggregatedTotalDeliveredParts": 0,
        "aggregatedTotalUnDeliveredParts": total * parts_per_recipient,
        "aggregatedTotalUnKnownParts": 0,
        "updatedAt": datetime.now(tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    args = parse_args()
    args.db_password = read_secret("DB_PASSWORD", "Database password: ")

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

        result = fetch_audience_count(cur, campaign_id)
        if result is None:
            print(f"  SKIP: no processed_campaign found for campaign_id={campaign_id}")
            skip_count += 1
            continue

        pc_id, audience_count = result
        print(f"  processed_campaign_id={pc_id} audience_count={audience_count}")

        if audience_count == 0:
            print(f"  SKIP: audience_ids is empty for processed_campaign_id={pc_id}")
            skip_count += 1
            continue

        stats = build_fake_stats(audience_count, args.parts_per_recipient)
        print(f"  fake stats: {json.dumps(stats, indent=4)}")

        if args.dry_run:
            print("  DRY-RUN: skipping push to Jazebeh")
            success_count += 1
            continue

        try:
            assert token is not None
            jazebeh_push_statistics(args.jazebeh_domain, token, campaign_id, stats)
            print(f"  OK: fake statistics pushed for campaign_id={campaign_id}")
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
