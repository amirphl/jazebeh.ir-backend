#!/usr/bin/env python3
import argparse
import base64
import os
import sys
from pathlib import Path
from typing import List, Optional

from psycopg2 import connect, sql
from psycopg2.extras import execute_values

PY_AI_ROOT = Path(__file__).resolve().parents[1]
if str(PY_AI_ROOT) not in sys.path:
    sys.path.insert(0, str(PY_AI_ROOT))
from operational_guard import require_live_production_execution


def urlsafe_uid(n_bytes: int, max_len: int) -> str:
    """
    Generate a base64url (no padding) string from n_bytes of randomness,
    trimmed to max_len characters.
    """
    raw = base64.urlsafe_b64encode(os.urandom(n_bytes)).decode("ascii").rstrip("=")
    return raw[:max_len]


def generate_uids(target: int, uid_len: int) -> List[str]:
    # n_bytes ~ ceil(uid_len * 3 / 4). Add a little headroom.
    n_bytes = max(8, (uid_len * 3 + 3) // 4)
    seen = set()
    out: List[str] = []
    while len(out) < target:
        uid = urlsafe_uid(n_bytes, uid_len)
        if uid and uid not in seen:
            seen.add(uid)
            out.append(uid)
    return out


def insert_batch(
    conn,
    table: str,
    rows: List[tuple],
):
    """
    Bulk insert with ON CONFLICT DO NOTHING.
    Only the necessary columns are provided so defaults fill the rest:
    (uid, campaign_id, phone_number, link)
    """
    query = sql.SQL("""
        INSERT INTO {} (uid, campaign_id, phone_number, link)
        VALUES %s
        ON CONFLICT (uid) DO NOTHING
        RETURNING uid
    """).format(sql.Identifier(*table.split(".")))
    with conn.cursor() as cur:
        return [
            row[0]
            for row in execute_values(cur, query, rows, page_size=1000, fetch=True)
        ]


def main():
    parser = argparse.ArgumentParser(
        description="Generate UIDs and insert into short_links."
    )
    # Postgres
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", type=int, default=5432)
    parser.add_argument("--dbname", required=True)
    parser.add_argument("--user", required=True)
    parser.add_argument("--password", required=True)
    # Business args
    parser.add_argument("--table", default="short_links", help="Target table name")
    parser.add_argument(
        "--count", type=int, default=10000, help="How many rows to insert"
    )
    parser.add_argument(
        "--uid-length", type=int, default=12, help="UID character length (<=64)"
    )
    parser.add_argument("--phone-number", required=True)
    parser.add_argument("--link", required=True)
    parser.add_argument("--campaign-id", type=int, default=None)
    parser.add_argument(
        "--outfile", default="uids.txt", help="Path to write comma-separated UIDs"
    )
    parser.add_argument(
        "--batch-size", type=int, default=2000, help="DB insert batch size"
    )
    parser.add_argument(
        "--execute", action="store_true", help="insert rows; default is a dry run"
    )
    args = parser.parse_args()

    if args.uid_length < 6 or args.uid_length > 64:
        print("--uid-length must be between 6 and 64.", file=sys.stderr)
        sys.exit(2)
    if args.count <= 0 or args.batch_size <= 0:
        print("--count and --batch-size must be greater than zero.", file=sys.stderr)
        sys.exit(2)
    if not args.execute:
        print(
            f"Dry run: would insert {args.count} UIDs into {args.dbname}@{args.host}:{args.port}. Pass --execute in production live mode to write."
        )
        return
    require_live_production_execution(execute=True, action="Generating short-link UIDs")

    # Generate initial pool
    target = args.count
    uids = generate_uids(target, args.uid_length)

    # Connect
    conn = connect(
        host=args.host,
        port=args.port,
        dbname=args.dbname,
        user=args.user,
        password=args.password,
    )
    conn.autocommit = False

    inserted_uids: List[str] = []
    try:
        idx = 0
        while len(inserted_uids) < target:
            # Prepare batch
            take = min(args.batch_size, target - len(inserted_uids))
            batch_uids = uids[idx : idx + take]
            idx += take

            # Build rows (uid, campaign_id, phone_number, link)
            rows = [
                (uid, args.campaign_id, args.phone_number, args.link)
                for uid in batch_uids
            ]

            # Insert
            inserted_uids.extend(insert_batch(conn, args.table, rows))
            conn.commit()

            # ON CONFLICT can drop generated IDs. Continue until PostgreSQL
            # confirms that the requested number of IDs was inserted.
            if len(inserted_uids) < target and idx >= len(uids):
                need_more = (target - len(inserted_uids)) + args.batch_size
                uids.extend(generate_uids(need_more, args.uid_length))

        # Write UIDs to file (comma-separated)
        with open(args.outfile, "w", encoding="utf-8") as f:
            f.write(",".join(inserted_uids))

        print(f"Done. Target rows: {target}. UIDs saved to: {args.outfile}")

    except Exception as e:
        conn.rollback()
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    finally:
        conn.close()


if __name__ == "__main__":
    main()
