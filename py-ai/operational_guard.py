"""Environment and confirmation checks for side-effecting py-ai utilities."""

from __future__ import annotations

import os


def _environment() -> tuple[str, str]:
    return (
        os.getenv("APP_ENV", "").strip().lower(),
        os.getenv("EXTERNAL_SERVICES_MODE", "").strip().lower(),
    )


def require_live_production_execution(*, execute: bool, action: str) -> None:
    """Require an explicit production/live opt-in before an external write."""
    if not execute:
        raise RuntimeError(f"{action} is a dry run; pass --execute to perform it")
    environment, mode = _environment()
    if environment != "production" or mode != "live":
        raise RuntimeError(
            f"{action} requires APP_ENV=production and EXTERNAL_SERVICES_MODE=live"
        )


def require_confirmed_execution(*, execute: bool, action: str) -> None:
    """Require opt-in and a valid configured environment for a disruptive read."""
    if not execute:
        raise RuntimeError(f"{action} is a dry run; pass --execute to perform it")
    environment, mode = _environment()
    expected_mode = {"development": "mock", "staging": "mock", "production": "live"}
    if expected_mode.get(environment) != mode:
        raise RuntimeError(
            f"{action} requires a valid APP_ENV/EXTERNAL_SERVICES_MODE pair"
        )
