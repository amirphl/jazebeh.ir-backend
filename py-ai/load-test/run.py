#!/usr/bin/env python3
import argparse
import asyncio
import csv
import json
import random
import sys
import time
from datetime import datetime
from pathlib import Path
from statistics import StatisticsError, mean, median, variance
from typing import Any, Dict, List, Tuple

import aiohttp

PY_AI_ROOT = Path(__file__).resolve().parents[1]
if str(PY_AI_ROOT) not in sys.path:
    sys.path.insert(0, str(PY_AI_ROOT))
from operational_guard import require_confirmed_execution


def parse_uids_file(path: Path) -> List[str]:
    """
    Accepts comma-separated OR newline-separated UIDs, trims whitespace, removes empties/dupes.
    """
    text = path.read_text(encoding="utf-8")
    # split by comma or newline
    raw = []
    for part in text.replace("\n", ",").split(","):
        p = part.strip()
        if p:
            raw.append(p)
    # keep duplicates allowed? Generally not needed; we dedupe.
    return list(dict.fromkeys(raw))  # dedupe preserving order


def pct(values: List[float], p: float) -> float:
    """
    Simple percentile (p in [0,100]) using nearest-rank on sorted data.
    """
    if not values:
        return float("nan")
    if p <= 0:
        return values[0]
    if p >= 100:
        return values[-1]
    k = max(1, int(round(p / 100 * len(values))))
    return values[sorted(range(len(values)), key=lambda i: values[i])[k - 1]]


async def fetch_one(
    session: aiohttp.ClientSession,
    url: str,
    timeout_sec: float,
) -> Tuple[float, int, str]:
    """
    Returns (latency_seconds, status_code, error_message)
    error_message is '' on success; otherwise a short description.
    """
    t0 = time.perf_counter()
    try:
        async with session.get(url, timeout=timeout_sec) as resp:
            await resp.read()  # read body to complete timing fairly
            dt = time.perf_counter() - t0
            return dt, resp.status, ""
    except Exception as e:
        dt = time.perf_counter() - t0
        return dt, 0, str(e)


async def run_second(
    session: aiohttp.ClientSession,
    base_url: str,
    uids: List[str],
    batch_size: int,
    timeout_sec: float,
) -> List[Dict[str, Any]]:
    """
    Launch batch_size requests concurrently and wait for completion.
    Returns list of result dicts.
    """
    chosen = [random.choice(uids) for _ in range(batch_size)]
    urls = [f"{base_url.rstrip('/')}/s/{u}" for u in chosen]
    tasks = [asyncio.create_task(fetch_one(session, url, timeout_sec)) for url in urls]
    done = await asyncio.gather(*tasks)
    now_iso = datetime.utcnow().isoformat() + "Z"
    results = []
    for uid, url, (lat, status, err) in zip(chosen, urls, done):
        results.append(
            {
                "ts_utc": now_iso,
                "uid": uid,
                "url": url,
                "latency_sec": lat,
                "status": status,
                "error": err,
            }
        )
    return results


def summarize(results: List[Dict[str, Any]]) -> Dict[str, Any]:
    """
    Compute summary stats for all responses and for successes only.
    """
    lat_all = [r["latency_sec"] for r in results]
    lat_ok = [
        r["latency_sec"] for r in results if r["status"] and 200 <= r["status"] < 400
    ]
    n_total = len(results)
    n_ok = len(lat_ok)
    n_err = n_total - n_ok

    def safe_mean(x):
        try:
            return mean(x)
        except StatisticsError:
            return float("nan")

    def safe_var(x):
        try:
            # sample variance; if only one sample, return 0 to avoid error
            return 0.0 if len(x) < 2 else variance(x)
        except StatisticsError:
            return float("nan")

    def safe_med(x):
        try:
            return median(x)
        except StatisticsError:
            return float("nan")

    def safe_min(x):
        return min(x) if x else float("nan")

    def safe_max(x):
        return max(x) if x else float("nan")

    def q(x, p):
        return pct(sorted(x), p) if x else float("nan")

    stats_all = {
        "count": n_total,
        "mean": safe_mean(lat_all),
        "median": safe_med(lat_all),
        "variance": safe_var(lat_all),
        "min": safe_min(lat_all),
        "p50": q(lat_all, 50),
        "p90": q(lat_all, 90),
        "p95": q(lat_all, 95),
        "p99": q(lat_all, 99),
        "max": safe_max(lat_all),
    }
    stats_ok = {
        "count": n_ok,
        "mean": safe_mean(lat_ok),
        "median": safe_med(lat_ok),
        "variance": safe_var(lat_ok),
        "min": safe_min(lat_ok),
        "p50": q(lat_ok, 50),
        "p90": q(lat_ok, 90),
        "p95": q(lat_ok, 95),
        "p99": q(lat_ok, 99),
        "max": safe_max(lat_ok),
    }

    status_counts: Dict[str, int] = {}
    for r in results:
        key = str(r["status"]) if r["status"] else "error"
        status_counts[key] = status_counts.get(key, 0) + 1

    return {
        "total_requests": n_total,
        "ok_requests": n_ok,
        "error_requests": n_err,
        "error_rate": (n_err / n_total) if n_total else 0.0,
        "status_counts": status_counts,
        "latency_all": stats_all,
        "latency_success": stats_ok,
    }


async def main_async(args):
    uids = parse_uids_file(Path(args.uids_file))
    if not uids:
        raise SystemExit("No UIDs found in file.")
    if args.batch_size <= 0:
        raise SystemExit("--batch-size must be > 0")
    if args.duration_sec <= 0:
        raise SystemExit("--duration-sec must be > 0")

    ts_tag = datetime.utcnow().strftime("%Y%m%dT%H%M%SZ")
    outdir = Path(args.output_dir)
    outdir.mkdir(parents=True, exist_ok=True)
    summary_path = outdir / f"loadtest_{ts_tag}_summary.json"
    raw_path = outdir / f"loadtest_{ts_tag}_raw.csv"

    timeout = aiohttp.ClientTimeout(total=args.request_timeout_sec)
    connector = aiohttp.TCPConnector(limit=args.max_connections)
    headers = {}
    if args.user_agent:
        headers["User-Agent"] = args.user_agent

    results: List[Dict[str, Any]] = []
    started = time.perf_counter()
    started_utc = datetime.utcnow().isoformat() + "Z"
    deadline = started + args.duration_sec

    async with aiohttp.ClientSession(
        timeout=timeout, connector=connector, headers=headers
    ) as session:
        # CSV writer (streaming to avoid large RAM use)
        with raw_path.open("w", newline="", encoding="utf-8") as fcsv:
            writer = csv.writer(fcsv)
            writer.writerow(["ts_utc", "uid", "url", "latency_sec", "status", "error"])

            # loop each second
            while True:
                now = time.perf_counter()
                if now >= deadline:
                    break
                sec_start = now

                batch_results = await run_second(
                    session=session,
                    base_url=args.base_url,
                    uids=uids,
                    batch_size=args.batch_size,
                    timeout_sec=args.request_timeout_sec,
                )
                # write batch rows
                for r in batch_results:
                    writer.writerow(
                        [
                            r["ts_utc"],
                            r["uid"],
                            r["url"],
                            f"{r['latency_sec']:.6f}",
                            r["status"],
                            r["error"],
                        ]
                    )
                results.extend(batch_results)

                # sleep until next second boundary (best effort)
                elapsed = time.perf_counter() - sec_start
                await asyncio.sleep(max(0.0, 1.0 - elapsed))

    # Compute summary
    total_time = time.perf_counter() - started
    summary = summarize(results)
    summary.update(
        {
            "run_started_utc": started_utc,
            "base_url": args.base_url,
            "batch_size_per_second": args.batch_size,
            "duration_sec_requested": args.duration_sec,
            "duration_sec_actual": total_time,
            "requests_per_second_avg": (summary["total_requests"] / total_time)
            if total_time > 0
            else 0.0,
            "output_raw_csv": str(raw_path),
        }
    )

    summary_path.write_text(json.dumps(summary, indent=2), encoding="utf-8")
    print(f"Done. Summary: {summary_path}")
    print(f"Raw results: {raw_path}")


def main():
    p = argparse.ArgumentParser(
        description="Explicitly-confirmed rate-based load tester using pre-generated UIDs."
    )
    p.add_argument(
        "--uids-file",
        required=True,
        help="Path to comma-separated or newline-separated UIDs",
    )
    p.add_argument(
        "--base-url",
        default="http://127.0.0.1:8080",
        help="Base URL (default: local API)",
    )
    p.add_argument(
        "--batch-size", type=int, default=10, help="Requests per second (default: 10)"
    )
    p.add_argument(
        "--duration-sec",
        type=int,
        default=30,
        help="Run duration in seconds (default: 30)",
    )
    p.add_argument(
        "--request-timeout-sec",
        type=float,
        default=5.0,
        help="Per-request timeout seconds (default: 5)",
    )
    p.add_argument(
        "--max-connections",
        type=int,
        default=1000,
        help="Max concurrent TCP connections (default: 1000)",
    )
    p.add_argument(
        "--user-agent",
        default="MonstractLoadTester/1.0",
        help="Optional User-Agent header",
    )
    p.add_argument(
        "--output-dir",
        default="loadtest_out",
        help="Directory to write results (default: loadtest_out)",
    )
    p.add_argument(
        "--execute", action="store_true", help="perform requests; default is a dry run"
    )
    args = p.parse_args()
    if args.batch_size <= 0 or args.duration_sec <= 0:
        p.error("--batch-size and --duration-sec must be greater than zero")
    if args.request_timeout_sec <= 0 or args.max_connections <= 0:
        p.error("--request-timeout-sec and --max-connections must be greater than zero")
    if not args.execute:
        print(
            f"Dry run: would send up to {args.batch_size * args.duration_sec} requests to {args.base_url}. Pass --execute to run."
        )
        return
    require_confirmed_execution(execute=True, action="Running a load test")
    asyncio.run(main_async(args))


if __name__ == "__main__":
    main()
