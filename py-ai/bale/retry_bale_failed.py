#!/usr/bin/env python3
"""
Retry sending Bale messages for numbers recorded in bale_failed_numbers.txt
(exactly like send_bale_messages.py) but reading phone numbers from the
failure log and skipping rows where Bale reports the number is not a user.
"""

import argparse
import mimetypes
import sys
import time
from pathlib import Path

import requests

PY_AI_ROOT = Path(__file__).resolve().parents[1]
if str(PY_AI_ROOT) not in sys.path:
    sys.path.insert(0, str(PY_AI_ROOT))
from operational_guard import require_live_production_execution

NOT_USER_SUBSTR = "phone number is not bale user"
NOT_USER_CODE = '"code":17'


def read_message(path: Path) -> str:
    return path.read_text(encoding="utf-8").strip()


def read_failed_numbers(path: Path) -> list[str]:
    numbers: list[str] = []
    if not path.exists():
        return numbers
    for line in path.read_text(encoding="utf-8").splitlines():
        if NOT_USER_SUBSTR in line:
            continue
        parts = line.strip().split()
        if len(parts) < 2:
            continue
        phone = parts[0].strip()
        if phone:
            print(phone)
            numbers.append(phone)
    return numbers


def send_message(
    api_base: str,
    api_key: str,
    bot_id: int,
    phone: str,
    text: str,
    file_id: str | None = None,
) -> tuple[bool, str]:
    url = f"{api_base.rstrip('/')}/api/v3/send_message"
    payload = {
        "bot_id": bot_id,
        "phone_number": phone,
        "message_data": {"message": {"text": text}},
    }
    if file_id:
        payload["message_data"]["message"]["file_id"] = file_id
    headers = {
        "api-access-key": api_key,
        "Content-Type": "application/json",
    }
    try:
        resp = requests.post(url, json=payload, headers=headers, timeout=5)
        if resp.status_code // 100 != 2:
            return False, f"HTTP {resp.status_code}: {resp.text}"
        data = resp.json()
        if data.get("error_data"):
            return False, f"API error: {data.get('error_data')}"
        return True, data.get("message_id", "")
    except Exception as e:  # noqa: BLE001
        return False, str(e)


def upload_file(api_base: str, api_key: str, path: Path) -> tuple[bool, str]:
    url = f"{api_base.rstrip('/')}/api/v3/upload_file"
    mime, _ = mimetypes.guess_type(str(path))
    headers = {"api-access-key": api_key}
    with path.open("rb") as f:
        files = {"file": (path.name, f, mime or "application/octet-stream")}
        try:
            resp = requests.post(url, files=files, headers=headers, timeout=90)
            if resp.status_code // 100 != 2:
                return False, f"HTTP {resp.status_code}: {resp.text}"
            data = resp.json()
            fid = data.get("file_id")
            if not fid:
                return False, f"Upload missing file_id: {data}"
            return True, fid
        except Exception as e:  # noqa: BLE001
            return False, str(e)


def exponential_backoff(attempt: int, base: float = 1.0, cap: float = 60.0) -> float:
    return min(cap, base * (2 ** (attempt - 1)))


def should_retry(reason: str) -> bool:
    text = reason.lower()
    if NOT_USER_SUBSTR in text or NOT_USER_CODE in text:
        return False
    return True


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Retry sending Bale messages from bale_failed_numbers.txt"
    )
    parser.add_argument(
        "--failed-file",
        type=Path,
        default=Path("bale_failed_numbers.txt"),
        help="Path to failed numbers log",
    )
    parser.add_argument(
        "--message-file",
        required=True,
        type=Path,
        help="Path to UTF-8 text file containing message",
    )
    parser.add_argument(
        "--media-file", type=Path, help="Optional media file to upload and send"
    )
    parser.add_argument("--bot-id", required=True, type=int, help="Bale bot ID")
    parser.add_argument("--api-key", required=True, help="Bale API access key")
    parser.add_argument(
        "--api-base", default="https://safir.bale.ai", help="Bale API base URL"
    )
    parser.add_argument(
        "--max-attempts",
        type=int,
        default=5,
        help="Max retry attempts per number (default 5)",
    )
    parser.add_argument(
        "--execute", action="store_true", help="retry sends; default is a dry run"
    )
    args = parser.parse_args()

    numbers = read_failed_numbers(args.failed_file)
    if not numbers:
        print(
            "No retryable phone numbers found (all were non-Bale users or file empty).",
            file=sys.stderr,
        )
        return 0

    if not args.execute:
        print(
            f"Dry run: would retry {len(numbers)} Bale recipient(s). Pass --execute in production live mode to send."
        )
        return 0
    require_live_production_execution(execute=True, action="Retrying Bale messages")
    message = read_message(args.message_file)
    media_file_id = None
    if args.media_file:
        ok, fid_or_err = upload_file(args.api_base, args.api_key, args.media_file)
        if not ok:
            print(f"Media upload failed: {fid_or_err}", file=sys.stderr)
            return 1
        media_file_id = fid_or_err

    print(f"Retrying {len(numbers)} numbers from {args.failed_file} ...")

    success = 0
    fail = 0
    failed_numbers: list[tuple[str, str]] = []

    for num in numbers:
        attempt = 1
        while attempt <= args.max_attempts:
            ok, info = send_message(
                args.api_base, args.api_key, args.bot_id, num, message, media_file_id
            )
            if ok:
                success += 1
                print(f"[OK] {num} attempt={attempt} message_id={info}")
                break
            else:
                print(f"[FAIL] {num} attempt={attempt} reason={info}", file=sys.stderr)
                if not should_retry(info) or attempt == args.max_attempts:
                    fail += 1
                    failed_numbers.append((num, info))
                    break
                sleep_s = exponential_backoff(attempt)
                time.sleep(sleep_s)
                attempt += 1

    print(f"Done. Success={success}, Fail={fail}, Total={len(numbers)}")
    if failed_numbers:
        out_path = Path("bale_failed_numbers_retry.txt")
        with out_path.open("w", encoding="utf-8") as f:
            for num, reason in failed_numbers:
                f.write(f"{num}\t{reason}\n")
        print(f"Failed numbers saved to {out_path}", file=sys.stderr)

    return 0 if fail == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
