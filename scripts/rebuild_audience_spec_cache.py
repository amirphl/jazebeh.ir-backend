#!/usr/bin/env python3
"""Rebuild the Yamata audience-spec Redis cache from JSON exports.

The cache value has the same shape produced by CampaignFlowImpl:

    level1 -> level2 -> {"items": {level3: {"layer1_category": level1,
                                             "layer2_category": level2,
                                             "layer3_category": level3,
                                             "tags": [...],
                                             "available_audience": N}}}

Tag values are stringified ``tags.id`` values.  This is intentional: campaign
schedulers parse the values as database tag IDs and verify that they are still
active before executing a campaign.

Install the runtime dependency with:

    python3 -m pip install -r scripts/requirements-audience-spec.txt
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from collections import defaultdict
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

STAT_FIELDS = (
    "distinct_users",
    "black_users",
    "white_users",
    "pink_users",
    "weak_white",
    "good_white",
    "best_white",
    "weak_black",
    "good_black",
    "best_black",
    "weak_pink",
    "good_pink",
    "best_pink",
    "scored_users",
)
PLATFORMS = ("sms", "rubika", "bale", "splus")
LEVEL_FIELDS = (
    "layer1_category",
    "layer2_category",
    "layer3_category",
)
REQUIRED_TEST_LEVELS = ("L1-test", "L2-test", "L3-test")
REQUIRED_TEST_TAG_ID = 17358
MINIMUM_TEST_CAPACITY = 500
CACHE_TTL_SECONDS = 5 * 60


class InputValidationError(ValueError):
    """Raised when an input export cannot safely produce a cache value."""


@dataclass(frozen=True)
class BuildReport:
    stats_rows: int
    reference_rows: int
    tag_rows: int
    active_tags: int
    active_references: int
    leaves_written: int
    tags_written: int
    skipped_leaves_without_active_tags: tuple[tuple[str, str, str], ...]
    required_test_tag_verified_active: bool | None


def _load_json_list(path: Path, label: str) -> list[dict[str, Any]]:
    try:
        with path.open("r", encoding="utf-8") as handle:
            value = json.load(handle)
    except FileNotFoundError as exc:
        raise InputValidationError(f"{label} file does not exist: {path}") from exc
    except json.JSONDecodeError as exc:
        raise InputValidationError(
            f"{label} is not valid JSON ({path}:{exc.lineno}:{exc.colno}): {exc.msg}"
        ) from exc
    except OSError as exc:
        raise InputValidationError(f"cannot read {label} file {path}: {exc}") from exc

    if not isinstance(value, list):
        raise InputValidationError(f"{label} must contain a top-level JSON list")
    for index, row in enumerate(value):
        if not isinstance(row, dict):
            raise InputValidationError(
                f"{label} row {index} must be a JSON object, got {type(row).__name__}"
            )
    return value


def _required_nonempty_string(
    row: Mapping[str, Any], field: str, label: str, index: int
) -> str:
    value = row.get(field)
    if not isinstance(value, str) or not value.strip():
        raise InputValidationError(
            f"{label} row {index} field {field!r} must be a non-empty string"
        )
    # Do not normalize internal Unicode or whitespace. These strings are API keys
    # and must continue to match values stored on campaigns.
    return value.strip()


def _positive_int(row: Mapping[str, Any], field: str, label: str, index: int) -> int:
    value = row.get(field)
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise InputValidationError(
            f"{label} row {index} field {field!r} must be a positive integer"
        )
    return value


def _nonnegative_int(row: Mapping[str, Any], field: str, label: str, index: int) -> int:
    value = row.get(field)
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise InputValidationError(
            f"{label} row {index} field {field!r} must be a non-negative integer"
        )
    return value


def _levels(row: Mapping[str, Any], label: str, index: int) -> tuple[str, str, str]:
    return tuple(  # type: ignore[return-value]
        _required_nonempty_string(row, field, label, index) for field in LEVEL_FIELDS
    )


def _calculated_at(row: Mapping[str, Any], index: int) -> datetime:
    raw = row.get("calculated_at")
    if not isinstance(raw, str) or not raw.strip():
        raise InputValidationError(
            f"levels stats row {index} field 'calculated_at' must be a non-empty string"
        )
    normalized = raw.strip().replace("Z", "+00:00")
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError as exc:
        raise InputValidationError(
            f"levels stats row {index} has invalid calculated_at {raw!r}"
        ) from exc
    if parsed.tzinfo is None:
        raise InputValidationError(
            f"levels stats row {index} calculated_at must include a UTC offset"
        )
    return parsed


def _index_tags(tags: Sequence[Mapping[str, Any]]) -> dict[int, Mapping[str, Any]]:
    indexed: dict[int, Mapping[str, Any]] = {}
    for index, tag in enumerate(tags):
        tag_id = _positive_int(tag, "id", "tags", index)
        if tag_id in indexed:
            raise InputValidationError(f"tags contains duplicate id {tag_id}")
        if type(tag.get("is_active")) is not bool:
            raise InputValidationError(
                f"tags row {index} field 'is_active' must be a JSON boolean"
            )
        indexed[tag_id] = tag
    return indexed


def _latest_stats_by_levels(
    stats: Sequence[Mapping[str, Any]],
) -> dict[tuple[str, str, str], tuple[datetime, dict[str, int]]]:
    latest: dict[tuple[str, str, str], tuple[datetime, dict[str, int]]] = {}
    for index, row in enumerate(stats):
        levels = _levels(row, "levels stats", index)
        values = {
            field: _nonnegative_int(row, field, "levels stats", index)
            for field in STAT_FIELDS
        }
        calculated_at = _calculated_at(row, index)
        previous = latest.get(levels)
        if previous is None or calculated_at > previous[0]:
            latest[levels] = (calculated_at, values)
        elif calculated_at == previous[0] and values != previous[1]:
            joined = " / ".join(levels)
            raise InputValidationError(
                f"conflicting levels stats rows for {joined!r} at {calculated_at.isoformat()}"
            )
    return latest


def _audience_capacity(values: Mapping[str, int], platform: str) -> int:
    if platform == "sms":
        return values["white_users"] + values["pink_users"] // 3
    return values["black_users"] + values["white_users"] + values["pink_users"]


def build_audience_spec(
    stats: Sequence[Mapping[str, Any]],
    references: Sequence[Mapping[str, Any]],
    tags: Sequence[Mapping[str, Any]],
    platform: str = "sms",
    include_required_test_leaf: bool = False,
) -> tuple[dict[str, Any], BuildReport]:
    """Validate, join, and return a deterministic audience-spec cache document."""
    if platform not in PLATFORMS:
        raise InputValidationError(
            f"unsupported platform {platform!r}; choose from {PLATFORMS}"
        )

    tags_by_id = _index_tags(tags)
    active_tag_count = sum(tag["is_active"] is True for tag in tags_by_id.values())
    tags_by_levels: dict[tuple[str, str, str], set[int]] = defaultdict(set)
    seen_reference_ids: set[int] = set()
    active_references = 0

    for index, reference in enumerate(references):
        tag_id = _positive_int(reference, "id", "references", index)
        if tag_id in seen_reference_ids:
            raise InputValidationError(f"references contains duplicate tag id {tag_id}")
        seen_reference_ids.add(tag_id)
        levels = _levels(reference, "references", index)
        tag = tags_by_id.get(tag_id)
        if tag is None:
            raise InputValidationError(
                f"references row {index} points to tag id {tag_id}, which is absent from tags"
            )
        if tag["is_active"] is True:
            tags_by_levels[levels].add(tag_id)
            active_references += 1

    latest_stats = _latest_stats_by_levels(stats)
    required_test_tag_verified_active: bool | None = None
    if include_required_test_leaf:
        test_stats = latest_stats.get(REQUIRED_TEST_LEVELS)
        if test_stats is None:
            raise InputValidationError(
                "required test leaf L1-test / L2-test / L3-test has no stats row"
            )
        test_capacity = _audience_capacity(test_stats[1], platform)
        if test_capacity <= MINIMUM_TEST_CAPACITY:
            raise InputValidationError(
                "required test leaf capacity must be greater than "
                f"{MINIMUM_TEST_CAPACITY}, got {test_capacity} for {platform}"
            )

        exported_test_tag = tags_by_id.get(REQUIRED_TEST_TAG_ID)
        required_test_tag_verified_active = (
            exported_test_tag is not None and exported_test_tag["is_active"] is True
        )
        # This explicit operational test tag is allowed even when it is missing from
        # the supplied exports. The scheduler still fails closed unless PostgreSQL
        # contains the same ID as an active tag.
        tags_by_levels[REQUIRED_TEST_LEVELS].add(REQUIRED_TEST_TAG_ID)

    if not tags_by_levels:
        raise InputValidationError(
            "no active referenced tags remain; refusing to replace the cache with an "
            "empty audience spec"
        )
    missing_stats = sorted(set(tags_by_levels) - set(latest_stats))
    if missing_stats:
        preview = "; ".join(" / ".join(levels) for levels in missing_stats[:5])
        suffix = " ..." if len(missing_stats) > 5 else ""
        raise InputValidationError(
            f"{len(missing_stats)} referenced level group(s) have no stats row: "
            f"{preview}{suffix}"
        )

    skipped = tuple(sorted(set(latest_stats) - set(tags_by_levels)))
    spec: dict[str, Any] = {}
    tags_written = 0

    for levels in sorted(tags_by_levels):
        level1, level2, level3 = levels
        tag_ids = sorted(tags_by_levels[levels])
        if not tag_ids:
            # Currently unreachable because only active IDs create a group, but keep
            # this guard local to the output construction.
            continue
        stat_values = latest_stats[levels][1]
        available_audience = _audience_capacity(stat_values, platform)
        level2_node = spec.setdefault(level1, {}).setdefault(level2, {"items": {}})
        level2_node["items"][level3] = {
            "layer1_category": level1,
            "layer2_category": level2,
            "layer3_category": level3,
            "tags": [str(tag_id) for tag_id in tag_ids],
            "available_audience": available_audience,
            **stat_values,
        }
        tags_written += len(tag_ids)

    report = BuildReport(
        stats_rows=len(stats),
        reference_rows=len(references),
        tag_rows=len(tags),
        active_tags=active_tag_count,
        active_references=active_references,
        leaves_written=len(tags_by_levels),
        tags_written=tags_written,
        skipped_leaves_without_active_tags=skipped,
        required_test_tag_verified_active=required_test_tag_verified_active,
    )
    return spec, report


def redis_key(prefix: str, platform: str) -> str:
    """Match business_flow.redisKey exactly, including configured punctuation."""
    effective_prefix = prefix if prefix else "yamata"
    return f"{effective_prefix}:audience_spec:cache:v4:{platform}"


def redis_lock_key(prefix: str, platform: str) -> str:
    """Return the maintenance lock used to serialize concurrent rebuild scripts."""
    effective_prefix = prefix if prefix else "yamata"
    return f"{effective_prefix}:audience_spec:rebuild-lock:v4:{platform}"


def _json_bytes(value: Mapping[str, Any]) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        indent=2,
        sort_keys=True,
        separators=(",", ": "),
    ).encode("utf-8")


def _write_bytes_atomic(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp-{os.getpid()}")
    try:
        with temporary.open("xb") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass


def _backup_path(directory: Path, name: str) -> Path:
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    return directory / f"audience_spec_{name}_{timestamp}.json"


def store_in_redis(
    redis_url: str,
    redis_db: int | None,
    prefix: str,
    payloads: Mapping[str, bytes],
    backup_directory: Path | None,
    lock_timeout: int,
) -> list[str]:
    try:
        import redis  # type: ignore[import-not-found]
    except ImportError as exc:
        raise RuntimeError(
            "the 'redis' package is required; run "
            "'python3 -m pip install -r scripts/requirements-audience-spec.txt'"
        ) from exc

    connection_options: dict[str, Any] = {
        "decode_responses": False,
        "socket_connect_timeout": 10,
        "socket_timeout": 30,
        "health_check_interval": 30,
    }
    if redis_db is not None:
        connection_options["db"] = redis_db
    try:
        client = redis.Redis.from_url(redis_url, **connection_options)
    except (redis.exceptions.RedisError, ValueError) as exc:
        raise RuntimeError("invalid or unusable Redis URL") from exc
    written_keys: list[str] = []
    acquired_locks: list[tuple[str, Any]] = []
    selected_platforms = list(payloads)

    try:
        try:
            client.ping()
            # Serialize maintenance rebuilds. The application may independently
            # refresh a key, so every write is complete, validated JSON with the
            # same short TTL as the Go service.
            for platform in selected_platforms:
                lock_key = redis_lock_key(prefix, platform)
                lock = client.lock(
                    lock_key,
                    timeout=lock_timeout,
                    blocking_timeout=lock_timeout,
                    thread_local=False,
                )
                if not lock.acquire(blocking=True):
                    raise RuntimeError(
                        f"could not acquire Redis audience-spec lock {lock_key!r}"
                    )
                acquired_locks.append((lock_key, lock))

            # If acquiring a later lock had to wait, restore a full timeout on all
            # locks before beginning file and cache I/O.
            for lock_key, lock in acquired_locks:
                if not lock.reacquire():
                    raise RuntimeError(f"could not refresh Redis lock {lock_key!r}")

            for platform in selected_platforms:
                cache_key = redis_key(prefix, platform)
                previous = client.get(cache_key)
                if previous is not None and backup_directory is not None:
                    backup = _backup_path(backup_directory, platform)
                    _write_bytes_atomic(backup, previous)
                    print(f"Backed up {cache_key} to {backup}")

            # Commit every selected platform in one Redis transaction so readers
            # cannot observe a partially updated multi-platform rebuild.
            pipeline = client.pipeline(transaction=True)
            for platform in selected_platforms:
                pipeline.set(
                    redis_key(prefix, platform),
                    payloads[platform],
                    ex=CACHE_TTL_SECONDS,
                )
            acknowledgements = pipeline.execute()
            if len(acknowledgements) != len(selected_platforms) or not all(
                acknowledgement is True for acknowledgement in acknowledgements
            ):
                raise RuntimeError("Redis did not acknowledge every audience-spec SET")

            cache_keys = [
                redis_key(prefix, platform) for platform in selected_platforms
            ]
            stored_payloads = client.mget(cache_keys)
            for platform, cache_key, stored in zip(
                selected_platforms, cache_keys, stored_payloads, strict=True
            ):
                if stored != payloads[platform]:
                    raise RuntimeError(
                        f"post-write verification failed for Redis key {cache_key!r}"
                    )
                written_keys.append(cache_key)
        except redis.exceptions.RedisError as exc:
            raise RuntimeError(f"Redis operation failed: {exc}") from exc
    finally:
        release_error: RuntimeError | None = None
        for lock_key, lock in reversed(acquired_locks):
            try:
                lock.release()
            except redis.exceptions.RedisError as exc:
                release_error = RuntimeError(
                    f"lost Redis lock {lock_key!r} before it could be released"
                )
                release_error.__cause__ = exc
        try:
            client.close()
        except redis.exceptions.RedisError as exc:
            if release_error is None:
                release_error = RuntimeError(f"failed to close Redis connection: {exc}")
                release_error.__cause__ = exc
        if release_error is not None and sys.exc_info()[0] is None:
            raise release_error

    return written_keys


def _env_int(name: str) -> int | None:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return None
    try:
        value = int(raw)
    except ValueError as exc:
        raise InputValidationError(
            f"environment variable {name} must be an integer"
        ) from exc
    if value < 0:
        raise InputValidationError(
            f"environment variable {name} must be greater than or equal to zero"
        )
    return value


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Rebuild and atomically replace the audience-spec Redis cache."
    )
    parser.add_argument("--stats", type=Path, default=Path("src_layer_all_stats.json"))
    parser.add_argument("--references", type=Path, default=Path("src_reference.json"))
    parser.add_argument("--tags", type=Path, default=Path("tags.json"))
    parser.add_argument(
        "--platform",
        action="append",
        choices=PLATFORMS,
        dest="platforms",
        help="platform cache to replace; repeat for multiple platforms (default: sms)",
    )
    parser.add_argument(
        "--redis-db",
        type=int,
        default=_env_int("CACHE_REDIS_DB"),
        help="override Redis database (default: CACHE_REDIS_DB or URL database)",
    )
    parser.add_argument(
        "--redis-prefix",
        default=os.getenv("CACHE_REDIS_PREFIX", "yamata:"),
        help="cache prefix used by the Go service (default: CACHE_REDIS_PREFIX or 'yamata:')",
    )
    parser.add_argument(
        "--output",
        type=Path,
        help="also atomically write the generated platform JSON to this path",
    )
    parser.add_argument(
        "--backup-directory",
        type=Path,
        default=Path("audience-spec-backups"),
        help="directory for the previous Redis value (default: audience-spec-backups)",
    )
    parser.add_argument(
        "--no-backup",
        action="store_true",
        help="do not back up an existing Redis value before replacing it",
    )
    parser.add_argument(
        "--lock-timeout",
        type=int,
        default=60,
        help="seconds to wait for and hold each platform lock (default: 60)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="validate/build only; do not connect to or modify Redis",
    )
    parser.add_argument(
        "--execute",
        action="store_true",
        help="confirm production Redis updates; default is dry-run",
    )
    args = parser.parse_args(argv)
    from guard_environment import environment_banner, require_production_confirmation

    if args.execute and args.dry_run:
        parser.error("--execute and --dry-run cannot be combined")
    args.dry_run = not args.execute
    if args.execute:
        require_production_confirmation(True)
    else:
        environment_banner()
    args.redis_url = os.getenv("CACHE_REDIS_URL")
    if args.redis_db is not None and args.redis_db < 0:
        parser.error("--redis-db must be greater than or equal to zero")
    if args.lock_timeout <= 0:
        parser.error("--lock-timeout must be greater than zero")
    if not args.platforms:
        args.platforms = ["sms"]
    # Avoid surprising repeated writes when the same flag is supplied twice.
    args.platforms = list(dict.fromkeys(args.platforms))
    if not args.dry_run and not args.redis_url:
        parser.error("CACHE_REDIS_URL is required unless --dry-run is used")
    return args


def main(argv: Sequence[str] | None = None) -> int:
    os.umask(0o077)
    try:
        args = parse_args(argv)
        stats = _load_json_list(args.stats, "levels stats")
        references = _load_json_list(args.references, "references")
        tags = _load_json_list(args.tags, "tags")
        specs: dict[str, dict[str, Any]] = {}
        payloads: dict[str, bytes] = {}
        reports: dict[str, BuildReport] = {}
        for platform in args.platforms:
            spec, report = build_audience_spec(
                stats,
                references,
                tags,
                platform=platform,
                include_required_test_leaf=True,
            )
            specs[platform] = spec
            payloads[platform] = _json_bytes(spec)
            reports[platform] = report
            print(
                f"Built {platform} audience spec: {report.leaves_written} leaves, "
                f"{report.tags_written} tag references, {len(payloads[platform])} bytes"
            )
        report = reports[args.platforms[0]]
        print(
            f"Inputs: {report.stats_rows} stats rows, {report.reference_rows} references, "
            f"{report.tag_rows} tags ({report.active_tags} active)"
        )
        if report.skipped_leaves_without_active_tags:
            print(
                "Skipped stats leaves with no active referenced tags: "
                f"{len(report.skipped_leaves_without_active_tags)}",
                file=sys.stderr,
            )
            for levels in report.skipped_leaves_without_active_tags:
                print(f"  - {' / '.join(levels)}", file=sys.stderr)
        if report.required_test_tag_verified_active is False:
            print(
                f"WARNING: required test tag {REQUIRED_TEST_TAG_ID} was not verified "
                "as active in tags.json; ensure it exists and is active in PostgreSQL",
                file=sys.stderr,
            )

        if args.output is not None:
            output_value: Mapping[str, Any]
            output_value = specs[args.platforms[0]] if len(specs) == 1 else specs
            _write_bytes_atomic(args.output, _json_bytes(output_value) + b"\n")
            print(f"Wrote generated JSON to {args.output}")

        if args.dry_run:
            for platform in args.platforms:
                print(
                    f"Dry run: would replace Redis key {redis_key(args.redis_prefix, platform)}"
                )
            return 0

        backup_directory = None if args.no_backup else args.backup_directory
        written_keys = store_in_redis(
            redis_url=args.redis_url,
            redis_db=args.redis_db,
            prefix=args.redis_prefix,
            payloads=payloads,
            backup_directory=backup_directory,
            lock_timeout=args.lock_timeout,
        )
        for key in written_keys:
            print(f"Replaced and verified Redis key {key}")
        return 0
    except (InputValidationError, RuntimeError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
