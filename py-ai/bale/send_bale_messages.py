#!/usr/bin/env python3
"""
Send a message to a list of phone numbers via Bale bot.

Usage example:
  python send_bale_messages.py \
      --xlsx numbers.xlsx \
      --message-file message.txt \
      --bot-id 123456 \
      --api-key YOUR_API_ACCESS_KEY \
      --api-base https://safir.bale.ai
"""

import argparse
import mimetypes
import sys
import time
from pathlib import Path

import pandas as pd
import requests

PY_AI_ROOT = Path(__file__).resolve().parents[1]
if str(PY_AI_ROOT) not in sys.path:
    sys.path.insert(0, str(PY_AI_ROOT))
from operational_guard import require_live_production_execution

NOT_USER_SUBSTR = "phone number is not bale user"
NOT_USER_CODE = '"code":17'


def read_message(path: Path) -> str:
    return path.read_text(encoding="utf-8").strip()


def read_numbers(path: Path) -> list[str]:
    df = pd.read_excel(path)
    if df.empty:
        return []
    first_col = df.iloc[:, 0].astype(str).str.strip()
    # drop header-like values and blanks
    nums = ["98" + n for n in first_col if n and not n.lower().startswith("mobile")]
    return nums


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
    except Exception as e:
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
        except Exception as e:
            return False, str(e)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Send Bale messages from XLSX phone list."
    )
    parser.add_argument(
        "--xlsx",
        required=True,
        type=Path,
        help="Path to XLSX file (first column = phone numbers)",
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
        "--api-base",
        default="https://safir.bale.ai",
        help="Bale API base URL (default: safir.bale.ai)",
    )
    parser.add_argument(
        "--delay-ms",
        type=float,
        default=20.0,
        help="Delay between requests in milliseconds (default: 10)",
    )
    parser.add_argument(
        "--execute", action="store_true", help="send messages; default is a dry run"
    )
    args = parser.parse_args()

    numbers = read_numbers(args.xlsx)
    if not numbers:
        print("No phone numbers found.", file=sys.stderr)
        return 1
    if not args.execute:
        print(
            f"Dry run: would send to {len(numbers)} Bale recipient(s). Pass --execute in production live mode to send."
        )
        return 0
    require_live_production_execution(execute=True, action="Sending Bale messages")
    message = read_message(args.message_file)
    media_file_id = None
    if args.media_file:
        ok, fid_or_err = upload_file(args.api_base, args.api_key, args.media_file)
        if not ok:
            print(f"Media upload failed: {fid_or_err}", file=sys.stderr)
            return 1
        media_file_id = fid_or_err

    print("Go ...")

    delay_s = args.delay_ms / 1000.0
    success = 0
    fail = 0
    failed_numbers: list[tuple[str, str]] = []

    for num in numbers:
        ok, info = send_message(
            args.api_base, args.api_key, args.bot_id, num, message, media_file_id
        )
        if ok:
            success += 1
            print(f"[OK] {num} message_id={info}")
        else:
            fail += 1
            # Do not enqueue for retry if Bale says number is not a user (code 17)
            if NOT_USER_SUBSTR in info.lower() or NOT_USER_CODE in info.lower():
                print(f"[SKIP-RETRY] {num} reason={info}", file=sys.stderr)
            else:
                failed_numbers.append((num, info))
            print(f"[FAIL] {num} reason={info}", file=sys.stderr)
        time.sleep(delay_s)

    print(f"Done. Success={success}, Fail={fail}, Total={len(numbers)}")
    if failed_numbers:
        fail_path = Path("bale_failed_numbers.txt")
        with fail_path.open("w", encoding="utf-8") as f:
            for num, reason in failed_numbers:
                f.write(f"{num}\t{reason}\n")
        print(f"Failed numbers saved to {fail_path}", file=sys.stderr)
    return 0 if fail == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
