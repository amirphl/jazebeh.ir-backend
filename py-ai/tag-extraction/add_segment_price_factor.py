"""
Assign a random segment price factor per unique level3 and insert into Postgres for multiple platforms.

Usage (defaults read alongside this script):
    python add_segment_price_factor.py
    python add_segment_price_factor.py --input stat_with_tags.csv2 --seed 42 --platforms sms,rubika,bale,splus
"""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path
from typing import Iterable, Tuple

import numpy as np
import pandas as pd
from psycopg2 import connect
from psycopg2.extras import execute_values

PY_AI_ROOT = Path(__file__).resolve().parents[1]
if str(PY_AI_ROOT) not in sys.path:
    sys.path.insert(0, str(PY_AI_ROOT))
from operational_guard import require_live_production_execution


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Populate segment_price_factor for each level3 with random values in [0.5, 1.5]."
    )
    parser.add_argument(
        "--input",
        "-i",
        default="stat_with_tags.csv",
        help="Input CSV file with columns: level1, level2, level3, tags, available_audience, level2_one_line, level2_inclusion, level2_exclusion.",
    )
    parser.add_argument(
        "--platforms",
        default=os.getenv("SEGMENT_PRICE_PLATFORMS", "sms,rubika,bale,splus"),
        help="Comma-separated platforms to store in segment_price_factors.platform.",
    )
    parser.add_argument(
        "--host",
        default=os.getenv("DB_HOST", "localhost"),
        help="Database host.",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.getenv("DB_PORT", "5432")),
        help="Database port.",
    )
    parser.add_argument(
        "--dbname",
        default=os.getenv("DB_NAME"),
        help="Database name.",
    )
    parser.add_argument(
        "--user",
        default=os.getenv("DB_USER"),
        help="Database user.",
    )
    parser.add_argument(
        "--password",
        default=os.getenv("DB_PASSWORD"),
        help="Database password.",
    )
    parser.add_argument(
        "--seed",
        type=int,
        default=None,
        help="Optional random seed for reproducible factors.",
    )
    parser.add_argument(
        "--batch-size",
        type=int,
        default=1000,
        help="DB insert batch size.",
    )
    parser.add_argument(
        "--execute",
        action="store_true",
        help="write price factors; default is a dry run",
    )
    return parser.parse_args()


def insert_price_factors(
    rows: Iterable[Tuple[str, str, float]],
    *,
    host: str,
    port: int,
    dbname: str,
    user: str,
    password: str,
    batch_size: int,
) -> None:
    conn = connect(host=host, port=port, dbname=dbname, user=user, password=password)
    conn.autocommit = False

    try:
        with conn.cursor() as cur:
            sql = "INSERT INTO segment_price_factors (platform, level3, price_factor) VALUES %s"
            rows_list = list(rows)
            for start in range(0, len(rows_list), batch_size):
                chunk = rows_list[start : start + batch_size]
                execute_values(cur, sql, chunk, template="(%s, %s, %s)")
        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def main() -> None:
    args = parse_args()
    rng = np.random.default_rng(args.seed)

    input_path = Path(args.input)
    if not input_path.exists():
        raise SystemExit(f"Input file not found: {input_path}")

    df = pd.read_csv(input_path)

    # Assign one factor per unique level3 (empty strings/NaN are left as NaN).
    level3_series = df["level3"].fillna("").astype(str).str.strip()
    unique_levels = sorted({lvl for lvl in level3_series.unique() if lvl})
    factors = {lvl: rng.uniform(0.5, 1.5) for lvl in unique_levels}

    platforms = [p.strip() for p in args.platforms.split(",") if p.strip()]
    if not platforms:
        raise SystemExit(
            "No platforms provided. Use --platforms or SEGMENT_PRICE_PLATFORMS."
        )

    missing_conn_fields = [
        name
        for name, val in {
            "dbname": args.dbname,
            "user": args.user,
            "password": args.password,
        }.items()
        if not val
    ]
    if missing_conn_fields:
        raise SystemExit(
            f"Missing DB connection values: {', '.join(missing_conn_fields)}. "
            "Provide via flags or environment (DB_NAME, DB_USER, DB_PASSWORD)."
        )

    rows = [
        (platform, lvl, float(factors[lvl]))
        for platform in platforms
        for lvl in unique_levels
    ]
    if not args.execute:
        print(
            f"Dry run: would insert {len(rows)} segment price factors into "
            f"{args.dbname}@{args.host}:{args.port}. Pass --execute in production live mode to write."
        )
        return
    require_live_production_execution(
        execute=True, action="Writing segment price factors"
    )

    insert_price_factors(
        rows,
        host=args.host,
        port=args.port,
        dbname=args.dbname,
        user=args.user,
        password=args.password,
        batch_size=args.batch_size,
    )

    print(
        f"Inserted {len(rows)} rows into segment_price_factors "
        f"({len(unique_levels)} level3 values across {len(platforms)} platforms)."
    )


if __name__ == "__main__":
    main()
