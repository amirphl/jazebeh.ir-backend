#!/usr/bin/env python3
"""Fail closed before production-only operational writes or provider calls."""

import os
import sys


def environment_banner() -> str:
    env = os.getenv("APP_ENV", "").strip().lower()
    mode = os.getenv("EXTERNAL_SERVICES_MODE", "").strip().lower()
    print(
        f"Operational environment: APP_ENV={env or '<unset>'}, EXTERNAL_SERVICES_MODE={mode or '<unset>'}",
        file=sys.stderr,
    )
    return env


def require_production_confirmation(confirmed: bool) -> None:
    env = environment_banner()
    if (
        env != "production"
        or os.getenv("EXTERNAL_SERVICES_MODE", "live").strip().lower() != "live"
    ):
        raise RuntimeError(
            "Live operational changes require APP_ENV=production and live mode"
        )
    if not confirmed:
        raise RuntimeError("Live operational changes require explicit confirmation")
