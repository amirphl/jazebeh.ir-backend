#!/usr/bin/env python3
"""
Resend TorobPay installment SMS rows from a CSV/TSV export.

Default mode is dry-run. Use --send to call PayamSMS. Credentials are read
only from PAYAM_SMS_* environment variables.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import io
import json
import logging
import os
import posixpath
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
import zipfile
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any
from script_common import require_live_provider_mode

try:
    from zoneinfo import ZoneInfo
except ImportError:  # pragma: no cover
    ZoneInfo = None


TARGET_TEXT = "از ترب\u200cپی قسطی بخر"
LINK_RE = re.compile(r"\bjo1n\.ir/?([A-Za-z0-9]+)\b")
SEND_DELAY_SECONDS = 0.010
MAX_INPUT_BYTES = 512 * 1024 * 1024
MAX_XLSX_UNCOMPRESSED_BYTES = 256 * 1024 * 1024
LOGGER = logging.getLogger("torobpay_resend")

PAYAM_SMS = {
    "token_url": os.getenv(
        "PAYAM_SMS_TOKEN_URL", "https://www.payamsms.com/auth/oauth/token"
    ),
    "system_name": os.getenv("PAYAM_SMS_SYSTEM_NAME", ""),
    "username": os.getenv("PAYAM_SMS_USERNAME", ""),
    "password": os.getenv("PAYAM_SMS_PASSWORD", ""),
    "scope": os.getenv("PAYAM_SMS_SCOPE", "webservice"),
    "grant_type": os.getenv("PAYAM_SMS_GRANT_TYPE", "password"),
    "root_access_token": os.getenv("PAYAM_SMS_ROOT_ACCESS_TOKEN", ""),
    "send_url": os.getenv(
        "PAYAM_SMS_SEND_URL",
        "https://www.payamsms.com/panel/webservice/sendMultipleWithSrc",
    ),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Correct jo1n.ir short links and resend selected SMS rows."
    )
    parser.add_argument(
        "input_path", help="Path to the Excel .xlsx file or CSV/TSV export."
    )
    parser.add_argument(
        "--send",
        action="store_true",
        help="Actually send SMS. Without this flag the script only prints a dry-run.",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=10,
        help="Number of non-sent matching rows to process from the start. Default: 10.",
    )
    parser.add_argument(
        "--sent-log",
        default="torobpay_sms_resend_sent.jsonl",
        help="JSONL file used to store attempts and skip successful rows.",
    )
    parser.add_argument(
        "--delimiter",
        default=None,
        help="CSV delimiter override. By default the script sniffs the file; tabs are supported.",
    )
    parser.add_argument(
        "--encoding",
        default="auto",
        help="CSV/TSV input encoding, or 'auto' for BOM-based detection. Ignored for .xlsx. Default: auto.",
    )
    parser.add_argument(
        "--csv-mode",
        choices=("csv", "lines"),
        default="csv",
        help="CSV parsing mode. Use 'lines' if a broken export collapses many physical lines into fewer CSV records. Default: csv.",
    )
    parser.add_argument(
        "--sheet",
        default="1",
        help="Excel sheet name, 1-based sheet index, or 'all'. Only used for .xlsx. Default: 1.",
    )
    parser.add_argument(
        "--list-sheets",
        action="store_true",
        help="Print Excel sheet names and parsed row counts, then exit.",
    )
    parser.add_argument(
        "--text-col",
        type=int,
        default=4,
        help="1-based text/message column. Default: 4.",
    )
    parser.add_argument(
        "--sender-col",
        type=int,
        default=5,
        help="1-based sender/source number column. Default: 5.",
    )
    parser.add_argument(
        "--recipient-col",
        type=int,
        default=6,
        help="1-based recipient/mobile column. Default: 6.",
    )
    parser.add_argument(
        "--row-id-col",
        type=int,
        default=1,
        help="1-based stable row id column for sent-state keys. Default: 1.",
    )
    parser.add_argument(
        "--sender",
        default=None,
        help="Override sender/source number instead of reading --sender-col.",
    )
    parser.add_argument(
        "--test-phone",
        default=None,
        help="Send only one corrected matching row to this phone number for testing. Does not mark the original row as sent.",
    )
    parser.add_argument(
        "--test-row",
        type=int,
        default=1,
        help="1-based index among non-sent matching rows to use with --test-phone. Default: 1.",
    )
    parser.add_argument(
        "--contains",
        default=TARGET_TEXT,
        help="Text that must be present in the SMS body. Default is the TorobPay phrase.",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=60.0,
        help="HTTP timeout in seconds. Default: 60.",
    )
    parser.add_argument(
        "--send-date-delay-seconds",
        type=int,
        default=60,
        help="Seconds to add to PayamSMS sendDate, using Tehran time. Default: 60.",
    )
    parser.add_argument(
        "--log-file",
        default="torobpay_sms_resend.log",
        help="Human-readable log file. Default: torobpay_sms_resend.log.",
    )
    parser.add_argument(
        "--log-level",
        default="INFO",
        choices=("DEBUG", "INFO", "WARNING", "ERROR"),
        help="Logging verbosity. Default: INFO.",
    )
    args = parser.parse_args()
    for option_name in ("text_col", "sender_col", "recipient_col", "row_id_col"):
        if getattr(args, option_name) <= 0:
            parser.error(f"--{option_name.replace('_', '-')} must be greater than zero")
    if args.delimiter is not None and len(args.delimiter) != 1:
        parser.error("--delimiter must be exactly one character")
    return args


def open_private_append(path_value: str | Path):
    path = Path(path_value)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink():
        raise OSError(f"refusing symlinked output file: {path}")
    flags = os.O_WRONLY | os.O_CREAT | os.O_APPEND
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    descriptor = os.open(path, flags, 0o600)
    os.fchmod(descriptor, 0o600)
    return os.fdopen(descriptor, "a", encoding="utf-8")


def setup_logging(log_file: str, level_name: str) -> None:
    level = getattr(logging, level_name.upper(), logging.INFO)
    formatter = logging.Formatter(
        "%(asctime)s %(levelname)s %(message)s",
        datefmt="%Y-%m-%dT%H:%M:%S%z",
    )

    file_handler = logging.StreamHandler(open_private_append(log_file))
    file_handler.setFormatter(formatter)
    file_handler.setLevel(level)

    console_handler = logging.StreamHandler()
    console_handler.setFormatter(formatter)
    console_handler.setLevel(level)

    LOGGER.setLevel(level)
    for old_handler in LOGGER.handlers:
        old_handler.close()
    LOGGER.handlers.clear()
    LOGGER.addHandler(file_handler)
    LOGGER.addHandler(console_handler)
    LOGGER.propagate = False


def detect_text_encoding(path: Path, encoding: str) -> str:
    if encoding.lower() != "auto":
        return encoding
    head = path.read_bytes()[:4]
    if head.startswith(b"\xff\xfe"):
        return "utf-16-le"
    if head.startswith(b"\xfe\xff"):
        return "utf-16-be"
    if head.startswith(b"\xef\xbb\xbf"):
        return "utf-8-sig"
    return "utf-8-sig"


def sniff_delimiter(path: Path, encoding: str) -> str:
    sample = path.read_text(encoding=encoding, errors="strict")[:8192]
    try:
        dialect = csv.Sniffer().sniff(sample, delimiters="\t,;|")
        return dialect.delimiter
    except csv.Error:
        return "\t" if "\t" in sample else ","


def read_rows(path: Path, delimiter: str, encoding: str) -> list[list[str]]:
    with path.open("r", encoding=encoding, newline="") as f:
        return [row for row in csv.reader(f, delimiter=delimiter)]


def read_table(path: Path, args: argparse.Namespace) -> list[list[str]]:
    if path.suffix.lower() == ".xlsx":
        return read_xlsx_rows(path, args.sheet)

    encoding = detect_text_encoding(path, args.encoding)
    delimiter = (
        args.delimiter
        if args.delimiter is not None
        else sniff_delimiter(path, encoding)
    )
    text = path.read_text(encoding=encoding, errors="strict")
    physical_lines = text.splitlines()
    LOGGER.info(
        "csv diagnostics path=%s encoding=%s delimiter=%r physical_lines=%d mode=%s",
        path,
        encoding,
        delimiter,
        len(physical_lines),
        args.csv_mode,
    )

    if args.csv_mode == "lines":
        quote_fragment_count = sum(
            1 for line in physical_lines if line.count('"') % 2 == 1
        )
        if quote_fragment_count > 0:
            LOGGER.warning(
                "--csv-mode lines found %d physical lines with unbalanced quotes. This usually means quoted multiline fields; use default --csv-mode csv instead.",
                quote_fragment_count,
            )
        return [line.split(delimiter) for line in physical_lines if line != ""]

    rows = list(csv.reader(io.StringIO(text, newline=""), delimiter=delimiter))
    LOGGER.info(
        "csv parsed records=%d physical_lines=%d",
        len(rows),
        len(physical_lines),
    )
    if len(physical_lines) > 1000 and len(rows) < len(physical_lines) // 10:
        LOGGER.warning(
            "CSV parser produced far fewer records than physical lines: records=%d physical_lines=%d. If the file truly has one row per physical line, try --csv-mode lines; otherwise this can be normal for quoted multiline cells.",
            len(rows),
            len(physical_lines),
        )
    return rows


def read_xlsx_rows(path: Path, sheet_selector: str) -> list[list[str]]:
    with zipfile.ZipFile(path) as zf:
        validate_xlsx_archive(zf)
        shared_strings = read_shared_strings(zf)
        sheets = workbook_sheets(zf)
        if sheet_selector.strip().lower() == "all":
            all_rows: list[list[str]] = []
            for sheet in sheets:
                sheet_rows = read_xlsx_sheet_rows(zf, sheet["path"], shared_strings)
                LOGGER.info(
                    "loaded excel sheet name=%s index=%d path=%s rows=%d",
                    sheet["name"],
                    sheet["index"],
                    sheet["path"],
                    len(sheet_rows),
                )
                all_rows.extend(sheet_rows)
            return all_rows

        sheet = select_sheet(sheets, sheet_selector)
        rows = read_xlsx_sheet_rows(zf, sheet["path"], shared_strings)
        LOGGER.info(
            "loaded excel sheet name=%s index=%d path=%s rows=%d",
            sheet["name"],
            sheet["index"],
            sheet["path"],
            len(rows),
        )
        if len(rows) < 1000 and len(sheets) > 1:
            LOGGER.warning(
                "selected sheet has only %d rows; workbook has %d sheets. Run --list-sheets or use --sheet all/name/index if the data is elsewhere.",
                len(rows),
                len(sheets),
            )
        return rows


def read_xlsx_sheet_rows(
    zf: zipfile.ZipFile,
    sheet_path: str,
    shared_strings: list[str],
) -> list[list[str]]:
    root = parse_xlsx_xml(zf.read(sheet_path), sheet_path)
    rows: list[list[str]] = []
    ns = {"x": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
    for row_el in root.findall(".//x:sheetData/x:row", ns):
        values: list[str] = []
        for cell in row_el.findall("x:c", ns):
            ref = cell.attrib.get("r", "")
            col_index = excel_col_to_index(ref)
            while len(values) < col_index:
                values.append("")
            values.append(read_xlsx_cell(cell, shared_strings, ns))
        rows.append(values)
    return rows


def read_shared_strings(zf: zipfile.ZipFile) -> list[str]:
    try:
        raw = zf.read("xl/sharedStrings.xml")
    except KeyError:
        return []
    root = parse_xlsx_xml(raw, "xl/sharedStrings.xml")
    ns = {"x": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
    strings: list[str] = []
    for si in root.findall("x:si", ns):
        parts = [t.text or "" for t in si.findall(".//x:t", ns)]
        strings.append("".join(parts))
    return strings


def workbook_sheets(zf: zipfile.ZipFile) -> list[dict[str, Any]]:
    wb_ns = {
        "x": "http://schemas.openxmlformats.org/spreadsheetml/2006/main",
        "r": "http://schemas.openxmlformats.org/officeDocument/2006/relationships",
    }
    rel_ns = {"rel": "http://schemas.openxmlformats.org/package/2006/relationships"}

    workbook = parse_xlsx_xml(zf.read("xl/workbook.xml"), "xl/workbook.xml")
    rels = parse_xlsx_xml(
        zf.read("xl/_rels/workbook.xml.rels"), "xl/_rels/workbook.xml.rels"
    )
    rel_targets = {
        rel.attrib["Id"]: rel.attrib["Target"]
        for rel in rels.findall("rel:Relationship", rel_ns)
    }

    sheet_elements = workbook.findall(".//x:sheets/x:sheet", wb_ns)
    if not sheet_elements:
        raise SystemExit(f"No sheets found in {zf.filename}")

    sheets: list[dict[str, Any]] = []
    for index, sheet in enumerate(sheet_elements, start=1):
        rel_id = sheet.attrib.get(f"{{{wb_ns['r']}}}id", "")
        target = rel_targets.get(rel_id)
        if not target:
            raise SystemExit(f"Could not resolve worksheet relationship {rel_id!r}")
        sheets.append(
            {
                "index": index,
                "name": sheet.attrib.get("name", f"Sheet{index}"),
                "path": normalize_xlsx_target(target),
            }
        )
    return sheets


def select_sheet(sheets: list[dict[str, Any]], sheet_selector: str) -> dict[str, Any]:
    if sheet_selector.isdigit():
        index = int(sheet_selector) - 1
        if 0 <= index < len(sheets):
            return sheets[index]
    else:
        for sheet in sheets:
            if sheet["name"] == sheet_selector:
                return sheet
    available = ", ".join(f"{sheet['index']}:{sheet['name']}" for sheet in sheets)
    raise SystemExit(
        f"Sheet {sheet_selector!r} not found. Available sheets: {available}"
    )


def normalize_xlsx_target(target: str) -> str:
    if target.startswith("/"):
        normalized = posixpath.normpath(target.lstrip("/"))
    elif target.startswith("xl/"):
        normalized = posixpath.normpath(target)
    else:
        normalized = posixpath.normpath(posixpath.join("xl", target))
    if not normalized.startswith("xl/worksheets/") or ".." in normalized.split("/"):
        raise ValueError("workbook contains an invalid worksheet path")
    return normalized


def validate_xlsx_archive(zf: zipfile.ZipFile) -> None:
    total_size = 0
    for member in zf.infolist():
        if member.flag_bits & 0x1:
            raise ValueError("encrypted XLSX members are not supported")
        total_size += member.file_size
        if total_size > MAX_XLSX_UNCOMPRESSED_BYTES:
            raise ValueError("XLSX uncompressed content exceeds 256 MiB")


def parse_xlsx_xml(raw: bytes, member_name: str) -> ET.Element:
    uppercase = raw[:4096].upper()
    if b"<!DOCTYPE" in uppercase or b"<!ENTITY" in uppercase:
        raise ValueError(
            f"XLSX member {member_name} contains a forbidden XML declaration"
        )
    return ET.fromstring(raw)


def list_xlsx_sheets(path: Path) -> list[dict[str, Any]]:
    with zipfile.ZipFile(path) as zf:
        validate_xlsx_archive(zf)
        shared_strings = read_shared_strings(zf)
        sheets = workbook_sheets(zf)
        for sheet in sheets:
            sheet["rows"] = len(read_xlsx_sheet_rows(zf, sheet["path"], shared_strings))
        return sheets


def read_xlsx_cell(
    cell: ET.Element,
    shared_strings: list[str],
    ns: dict[str, str],
) -> str:
    cell_type = cell.attrib.get("t")
    if cell_type == "inlineStr":
        return "".join(t.text or "" for t in cell.findall(".//x:t", ns)).strip()

    value_el = cell.find("x:v", ns)
    value = "" if value_el is None or value_el.text is None else value_el.text
    if cell_type == "s":
        try:
            return shared_strings[int(value)].strip()
        except (ValueError, IndexError):
            return value.strip()
    return value.strip()


def excel_col_to_index(cell_ref: str) -> int:
    letters = re.match(r"^[A-Za-z]+", cell_ref or "")
    if not letters:
        return 1
    total = 0
    for ch in letters.group(0).upper():
        total = total * 26 + (ord(ch) - ord("A") + 1)
    return total


def col(row: list[str], one_based_index: int) -> str:
    idx = one_based_index - 1
    if idx < 0 or idx >= len(row):
        return ""
    return row[idx].strip()


def correct_short_links(text: str) -> str:
    return LINK_RE.sub(lambda m: f"jo1n.ir/{m.group(1)}", text)


def row_key(row_id: str, recipient: str, corrected_text: str) -> str:
    digest = hashlib.sha256(
        "\0".join((row_id, recipient, corrected_text)).encode("utf-8")
    ).hexdigest()
    return f"v2|{digest}"


def legacy_row_key(row_id: str, recipient: str, corrected_text: str) -> str:
    """Recognize old state entries without writing embedded phone numbers again."""
    digest = hashlib.sha256(corrected_text.encode("utf-8")).hexdigest()[:16]
    return f"{row_id}|{recipient}|{digest}"


def load_sent_keys(path: Path) -> set[str]:
    sent: set[str] = set()
    if not path.exists():
        return sent
    with path.open("r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                item = json.loads(line)
            except json.JSONDecodeError:
                continue
            if item.get("ok") is True and item.get("row_key"):
                sent.add(str(item["row_key"]))
    return sent


def append_attempt(path: Path, attempt: dict[str, Any]) -> None:
    with open_private_append(path) as f:
        f.write(json.dumps(attempt, ensure_ascii=False, sort_keys=True) + "\n")


def masked_phone(value: str) -> str:
    compact = re.sub(r"\s+", "", value)
    return "***" + compact[-4:] if len(compact) >= 4 else "[invalid]"


def message_metadata(value: str) -> dict[str, Any]:
    return {
        "message_length": len(value),
        "message_sha256": hashlib.sha256(value.encode("utf-8")).hexdigest(),
    }


def tehran_now() -> datetime:
    if ZoneInfo is not None:
        return datetime.now(ZoneInfo("Asia/Tehran"))
    return datetime.now(timezone(timedelta(hours=3, minutes=30)))


def tehran_send_date(delay_seconds: int) -> str:
    return (tehran_now() + timedelta(seconds=delay_seconds)).strftime(
        "%Y-%m-%d %H:%M:%S"
    )


def http_json(
    method: str,
    url: str,
    payload: Any | None,
    headers: dict[str, str],
    timeout: float,
) -> tuple[int, Any, str]:
    body = (
        None
        if payload is None
        else json.dumps(payload, ensure_ascii=False).encode("utf-8")
    )
    req = urllib.request.Request(url, data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            parsed = json.loads(raw) if raw.strip() else None
            return resp.status, parsed, raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw) if raw.strip() else None
        except json.JSONDecodeError:
            parsed = None
        return e.code, parsed, raw


def get_token(timeout: float) -> str:
    form_body = urllib.parse.urlencode(
        {
            "systemName": PAYAM_SMS["system_name"],
            "username": PAYAM_SMS["username"],
            "password": PAYAM_SMS["password"],
            "scope": PAYAM_SMS["scope"] or "webservice",
            "grant_type": PAYAM_SMS["grant_type"] or "password",
        }
    ).encode("ascii")
    headers = {"Content-Type": "application/x-www-form-urlencoded"}
    if PAYAM_SMS["root_access_token"].strip():
        headers["Authorization"] = "Basic " + PAYAM_SMS["root_access_token"].strip()

    request = urllib.request.Request(
        PAYAM_SMS["token_url"], data=form_body, method="POST", headers=headers
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            status = response.status
            raw = response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        exc.read()
        raise RuntimeError(f"PayamSMS token HTTP {exc.code}") from exc
    if status < 200 or status >= 300:
        raise RuntimeError(f"PayamSMS token HTTP {status}")
    try:
        parsed = json.loads(raw) if raw.strip() else None
    except json.JSONDecodeError as exc:
        raise RuntimeError("PayamSMS token response was not valid JSON") from exc
    token = parsed.get("access_token", "") if isinstance(parsed, dict) else ""
    if not token:
        raise RuntimeError("PayamSMS token response did not contain access_token")
    return token


def send_sms(
    token: str,
    sender: str,
    recipient: str,
    body: str,
    tracking_id: str,
    timeout: float,
    send_date_delay_seconds: int,
) -> tuple[bool, int, Any, str, str]:
    send_date = tehran_send_date(send_date_delay_seconds)
    payload = {
        "sender": sender,
        "smsItems": [
            {
                "recipient": recipient,
                "body": body,
                "customerId": tracking_id,
                "sendDate": send_date,
            }
        ],
    }
    headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json; charset=utf-8",
    }
    status, parsed, raw = http_json(
        "POST", PAYAM_SMS["send_url"], payload, headers, timeout
    )
    ok = 200 <= status < 300
    if ok and isinstance(parsed, list):
        for item in parsed:
            err = str(item.get("errorCode") or "").strip()
            if err:
                ok = False
                break
    return ok, status, parsed, raw, send_date


def build_candidates(
    args: argparse.Namespace, sent_keys: set[str]
) -> list[dict[str, Any]]:
    path = Path(args.input_path)
    LOGGER.info("reading input path=%s", path)
    rows = read_table(path, args)
    LOGGER.info("loaded rows count=%d", len(rows))
    candidates: list[dict[str, Any]] = []
    matched_count = 0
    skipped_sent_count = 0
    selection_limit = args.test_row if args.test_phone else args.limit

    for row_number, row in enumerate(rows, start=1):
        original_text = col(row, args.text_col)
        if args.contains not in original_text:
            continue
        matched_count += 1
        corrected_text = correct_short_links(original_text)
        row_id = col(row, args.row_id_col) or str(row_number)
        recipient = col(row, args.recipient_col)
        sender = (args.sender or col(row, args.sender_col)).strip()
        if not re.fullmatch(r"\+?[0-9]{7,16}", recipient):
            LOGGER.warning(
                "skipping matching row with invalid recipient row_number=%s columns=%d",
                row_number,
                len(row),
            )
            continue
        if not re.fullmatch(r"[A-Za-z0-9+_-]{2,32}", sender):
            LOGGER.warning(
                "skipping matching row with invalid sender row_number=%s",
                row_number,
            )
            continue
        key = row_key(row_id, recipient, corrected_text)
        if (
            key in sent_keys
            or legacy_row_key(row_id, recipient, corrected_text) in sent_keys
        ):
            skipped_sent_count += 1
            continue
        if len(candidates) >= selection_limit:
            continue
        candidate = {
            "row_number": row_number,
            "row_key": key,
            "sender": sender,
            "recipient": recipient,
            "original_text": original_text,
            "corrected_text": corrected_text,
            "changed": original_text != corrected_text,
        }
        candidates.append(candidate)
        LOGGER.info(
            "selected row_number=%s recipient=%s changed=%s message_sha256=%s message_length=%d",
            row_number,
            masked_phone(recipient),
            candidate["changed"],
            message_metadata(corrected_text)["message_sha256"],
            len(corrected_text),
        )
    LOGGER.info(
        "matched rows=%d skipped_already_sent=%d eligible_non_sent=%d limit=%d",
        matched_count,
        skipped_sent_count,
        matched_count - skipped_sent_count,
        args.limit,
    )
    return candidates


def validate_send_config() -> None:
    missing = [
        key
        for key in ("system_name", "username", "password")
        if not PAYAM_SMS.get(key, "").strip()
    ]
    if missing:
        raise SystemExit(
            "Set required PAYAM_SMS_* environment variables before --send. Missing: "
            + ", ".join(missing)
        )
    for key in ("token_url", "send_url"):
        try:
            parsed = urllib.parse.urlparse(PAYAM_SMS[key])
            port = parsed.port
        except ValueError as exc:
            raise SystemExit(f"PAYAM_SMS {key} is invalid") from exc
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.query
            or parsed.fragment
            or (port is not None and not 1 <= port <= 65535)
        ):
            raise SystemExit(
                f"PAYAM_SMS {key} must be an absolute HTTPS URL without userinfo, query, or fragment"
            )


def main() -> int:
    args = parse_args()
    os.umask(0o077)
    setup_logging(args.log_file, args.log_level)
    if args.limit < 0:
        raise SystemExit("--limit must be >= 0")
    if args.test_row <= 0:
        raise SystemExit("--test-row must be >= 1")
    if args.send_date_delay_seconds < 0:
        raise SystemExit("--send-date-delay-seconds must be >= 0")
    if args.timeout <= 0:
        raise SystemExit("--timeout must be greater than zero")
    if args.test_phone and not re.fullmatch(r"\+?[0-9]{7,16}", args.test_phone.strip()):
        raise SystemExit(
            "--test-phone must contain 7 to 16 digits with an optional leading +"
        )

    input_path = Path(args.input_path)
    if not input_path.is_file():
        raise SystemExit(f"Input file does not exist or is not regular: {input_path}")
    if input_path.stat().st_size > MAX_INPUT_BYTES:
        raise SystemExit("Input file exceeds the 512 MiB safety limit")
    if args.list_sheets:
        if input_path.suffix.lower() != ".xlsx":
            raise SystemExit("--list-sheets only works with .xlsx files")
        for sheet in list_xlsx_sheets(input_path):
            line = f"{sheet['index']}\t{sheet['name']}\trows={sheet['rows']}"
            LOGGER.info("excel sheet %s", line)
            print(line)
        return 0

    sent_log = Path(args.sent_log)
    sent_keys = load_sent_keys(sent_log)
    LOGGER.info(
        "starting mode=%s input=%s limit=%d sent_log=%s already_sent=%d send_date_delay_seconds=%d",
        "test-phone" if args.test_phone else ("send" if args.send else "dry-run"),
        args.input_path,
        args.limit,
        sent_log,
        len(sent_keys),
        args.send_date_delay_seconds,
    )
    candidates = build_candidates(args, sent_keys)
    if args.test_phone:
        if len(candidates) < args.test_row:
            raise SystemExit(
                f"--test-row {args.test_row} requested, but only {len(candidates)} matching non-sent rows were found"
            )
        original = candidates[args.test_row - 1]
        test_item = dict(original)
        test_item["recipient"] = args.test_phone.strip()
        test_item["row_key"] = "test|" + row_key(
            original["row_key"], test_item["recipient"], test_item["corrected_text"]
        )
        candidates = [test_item]
        LOGGER.info(
            "test-phone mode selected source_row_number=%s original_recipient=%s test_recipient=%s message_sha256=%s",
            original["row_number"],
            masked_phone(original["recipient"]),
            masked_phone(test_item["recipient"]),
            message_metadata(test_item["corrected_text"])["message_sha256"],
        )

    print(
        json.dumps(
            {
                "mode": "test-phone"
                if args.test_phone
                else ("send" if args.send else "dry-run"),
                "selected_rows": len(candidates),
                "already_sent_rows": len(sent_keys),
                "sent_log": str(sent_log),
            },
            ensure_ascii=False,
        )
    )

    if not args.send:
        for item in candidates:
            LOGGER.info(
                "dry-run row_number=%s recipient=%s changed=%s message_sha256=%s message_length=%d",
                item["row_number"],
                masked_phone(item["recipient"]),
                item["changed"],
                message_metadata(item["corrected_text"])["message_sha256"],
                len(item["corrected_text"]),
            )
            print(
                json.dumps(
                    {
                        "row_number": item["row_number"],
                        "row_key": item["row_key"],
                        "recipient": masked_phone(item["recipient"]),
                        "changed": item["changed"],
                        **message_metadata(item["corrected_text"]),
                    },
                    ensure_ascii=False,
                )
            )
        LOGGER.info("dry-run complete selected_rows=%d", len(candidates))
        return 0

    require_live_provider_mode()
    validate_send_config()
    LOGGER.info("requesting PayamSMS token")
    token = get_token(args.timeout)
    LOGGER.info("PayamSMS token acquired")

    exit_code = 0
    for item in candidates:
        attempt = {
            "attempted_at": datetime.now(timezone.utc)
            .isoformat(timespec="seconds")
            .replace("+00:00", "Z"),
            "row_number": item["row_number"],
            "row_key": item["row_key"],
            "recipient": masked_phone(item["recipient"]),
            "changed": item["changed"],
            **message_metadata(item["corrected_text"]),
        }
        try:
            LOGGER.info(
                "sending row_number=%s recipient=%s message_sha256=%s",
                item["row_number"],
                masked_phone(item["recipient"]),
                message_metadata(item["corrected_text"])["message_sha256"],
            )
            ok, status, _parsed, raw, send_date = send_sms(
                token=token,
                sender=item["sender"],
                recipient=item["recipient"],
                body=item["corrected_text"],
                tracking_id=item["row_key"],
                timeout=args.timeout,
                send_date_delay_seconds=args.send_date_delay_seconds,
            )
            attempt.update(
                {
                    "ok": ok,
                    "http_status": status,
                    "provider_response_sha256": hashlib.sha256(
                        raw.encode("utf-8")
                    ).hexdigest(),
                    "payamsms_send_date": send_date,
                    "payamsms_send_date_timezone": "Asia/Tehran",
                    "payamsms_send_date_delay_seconds": args.send_date_delay_seconds,
                }
            )
            if not ok:
                exit_code = 1
                LOGGER.error(
                    "send failed row_number=%s recipient=%s send_date_tehran=%s http_status=%s",
                    item["row_number"],
                    masked_phone(item["recipient"]),
                    send_date,
                    status,
                )
            else:
                LOGGER.info(
                    "send succeeded row_number=%s recipient=%s send_date_tehran=%s http_status=%s",
                    item["row_number"],
                    masked_phone(item["recipient"]),
                    send_date,
                    status,
                )
        except Exception as exc:
            attempt.update({"ok": False, "error_type": type(exc).__name__})
            LOGGER.exception(
                "send exception row_number=%s recipient=%s",
                item["row_number"],
                masked_phone(item["recipient"]),
            )
            exit_code = 1
        if args.test_phone:
            attempt["test_phone_mode"] = True
            LOGGER.info(
                "test-phone mode: recording audit without marking original row recipient=%s",
                masked_phone(item["recipient"]),
            )
        append_attempt(sent_log, attempt)
        print(json.dumps(attempt, ensure_ascii=False))
        time.sleep(SEND_DELAY_SECONDS)

    LOGGER.info(
        "send run complete selected_rows=%d exit_code=%d", len(candidates), exit_code
    )
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
