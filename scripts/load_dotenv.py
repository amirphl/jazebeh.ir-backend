#!/usr/bin/env python3
"""Parse a strict, non-executable dotenv file and emit NUL-delimited records."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

KEY_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
PROVISIONING_PLACEHOLDER_RE = re.compile(
    r"\$(?:db_password|jwt_secret|domain|email|grafana_password|redis_password|"
    r"sentry_postgres_password|sentry_glitchtip_secret_key|sentry_admin_password)\b"
)
RESERVED_KEYS = {
    "BASH_ENV",
    "BASHOPTS",
    "CDPATH",
    "COMPOSE_FILE",
    "COMPOSE_PROFILES",
    "DOCKER_CONFIG",
    "DOCKER_CONTEXT",
    "DOCKER_HOST",
    "DYLD_INSERT_LIBRARIES",
    "DYLD_LIBRARY_PATH",
    "ENV",
    "GLOBIGNORE",
    "IFS",
    "LD_PRELOAD",
    "LD_LIBRARY_PATH",
    "HOME",
    "PATH",
    "PYTHONHOME",
    "PYTHONINSPECT",
    "PYTHONPATH",
    "SHELLOPTS",
    "TEMP",
    "TMP",
    "TMPDIR",
    "XDG_CONFIG_HOME",
}
MAX_FILE_BYTES = 1024 * 1024


class DotenvError(ValueError):
    pass


def _decode_double_quoted(value: str, line_number: int) -> str:
    decoded: list[str] = []
    index = 0
    escapes = {"n": "\n", "r": "\r", "t": "\t", '"': '"', "\\": "\\"}
    while index < len(value):
        character = value[index]
        if character == "\\" and index + 1 < len(value):
            next_character = value[index + 1]
            decoded.append(escapes.get(next_character, "\\" + next_character))
            index += 2
            continue
        decoded.append(character)
        index += 1
    result = "".join(decoded)
    if any(character in result for character in "\0\r\n"):
        raise DotenvError(f"line {line_number}: multiline/control values are not supported")
    return result


def _parse_value(raw_value: str, line_number: int) -> str:
    value = raw_value.strip()
    if not value:
        return ""
    if value[0] in ("'", '"'):
        quote = value[0]
        escaped = False
        closing = None
        for index in range(1, len(value)):
            character = value[index]
            if quote == '"' and character == "\\" and not escaped:
                escaped = True
                continue
            if character == quote and not escaped:
                closing = index
                break
            escaped = False
        if closing is None:
            raise DotenvError(f"line {line_number}: unterminated quoted value")
        remainder = value[closing + 1 :].strip()
        if remainder and not remainder.startswith("#"):
            raise DotenvError(f"line {line_number}: unexpected text after quoted value")
        inner = value[1:closing]
        return _decode_double_quoted(inner, line_number) if quote == '"' else inner

    # An inline comment begins only when # is preceded by whitespace.
    value = re.split(r"\s+#", value, maxsplit=1)[0].rstrip()
    if any(character in value for character in "\0\r\n"):
        raise DotenvError(f"line {line_number}: control values are not supported")
    return value


def parse_dotenv(path: Path) -> list[tuple[str, str]]:
    try:
        size = path.stat().st_size
        if size > MAX_FILE_BYTES:
            raise DotenvError("dotenv file exceeds 1 MiB")
        text = path.read_text(encoding="utf-8", errors="strict")
    except OSError as exc:
        raise DotenvError(f"cannot read dotenv file: {exc}") from exc

    values: list[tuple[str, str]] = []
    seen: set[str] = set()
    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        if "=" not in line:
            raise DotenvError(f"line {line_number}: expected KEY=VALUE")
        key, raw_value = line.split("=", 1)
        key = key.strip()
        if not KEY_RE.fullmatch(key):
            raise DotenvError(f"line {line_number}: invalid variable name")
        if key in RESERVED_KEYS:
            raise DotenvError(f"line {line_number}: reserved variable {key} is not allowed")
        if key in seen:
            raise DotenvError(f"line {line_number}: duplicate variable {key}")
        seen.add(key)
        value = _parse_value(raw_value, line_number)
        placeholder = PROVISIONING_PLACEHOLDER_RE.search(value)
        if placeholder:
            raise DotenvError(
                f"line {line_number}: unresolved provisioning placeholder in {key}"
            )
        values.append((key, value))
    return values


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("dotenv_file", type=Path)
    args = parser.parse_args()
    try:
        values = parse_dotenv(args.dotenv_file)
    except (DotenvError, UnicodeError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    output = sys.stdout.buffer
    for key, value in values:
        output.write(key.encode("ascii") + b"\0" + value.encode("utf-8") + b"\0")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
