#!/usr/bin/env python3
"""Shared validation helpers for operational Python scripts."""

from __future__ import annotations

import argparse
import getpass
import os
import sys
import urllib.parse
from guard_environment import environment_banner


def require_live_provider_mode() -> None:
    environment = environment_banner()
    mode = os.getenv("EXTERNAL_SERVICES_MODE", "live").strip().lower()
    if environment != "production" or mode != "live":
        raise RuntimeError(
            "This standalone tool contacts a live provider and requires APP_ENV=production and live mode"
        )


def read_secret(environment_name: str, prompt: str) -> str:
    """Read a secret from the environment or a non-echoing interactive prompt."""
    value = os.getenv(environment_name, "")
    if value:
        return value
    if not sys.stdin.isatty():
        raise RuntimeError(
            f"{environment_name} must be set when stdin is not interactive"
        )
    value = getpass.getpass(prompt)
    if not value:
        raise RuntimeError(f"{environment_name} must not be empty")
    return value


def validate_https_origin(
    parser: argparse.ArgumentParser, option_name: str, value: str
) -> str:
    """Validate and normalize an HTTPS origin without accepting userinfo or paths."""
    if not value or any(ord(character) < 32 for character in value) or "\\" in value:
        parser.error(f"{option_name} contains invalid characters")
    try:
        parsed = urllib.parse.urlparse(value)
        port = parsed.port
    except ValueError:
        parser.error(f"{option_name} is not a valid URL")
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.params
        or parsed.query
        or parsed.fragment
        or parsed.path not in ("", "/")
        or parsed.netloc.endswith(":")
        or (port is not None and not 1 <= port <= 65535)
    ):
        parser.error(
            f"{option_name} must be an HTTPS origin without credentials, path, or query"
        )
    return value.rstrip("/")


def validate_database_port(parser: argparse.ArgumentParser, port: int) -> None:
    if not 1 <= port <= 65535:
        parser.error("--db-port must be between 1 and 65535")


def validate_positive_ids(
    parser: argparse.ArgumentParser, option_name: str, values: list[int]
) -> None:
    if any(value <= 0 for value in values):
        parser.error(f"{option_name} must contain only positive integers")
