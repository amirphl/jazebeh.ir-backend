#!/usr/bin/env python3
"""Check files copied by the production Dockerfile without displaying secrets."""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
COPIED_DIRS = (
    "app",
    "business_flow",
    "config",
    "models",
    "repository",
    "utils",
    "templates",
)
COPIED_FILES = ("go.mod", "go.sum", "main.go", "logging.go", "mock_http_transport.go")
SENSITIVE_NAME = re.compile(
    r"(^\.env(?:\.|$)|\.(?:pem|key|p12|pfx|dump|tar\.gz|tgz)$)", re.I
)
SECRET_PATTERNS = (
    re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    re.compile(rb"AKIA[0-9A-Z]{16}"),
    re.compile(rb"https?://[^\s/:@]+:[^\s/@]+@[^\s/]+", re.I),
)


def main() -> int:
    files = [ROOT / name for name in COPIED_FILES]
    for name in COPIED_DIRS:
        files.extend((ROOT / name).rglob("*"))
    failed = False
    for path in files:
        if not path.is_file() or path.is_symlink():
            continue
        rel = path.relative_to(ROOT)
        if SENSITIVE_NAME.search(path.name):
            print(
                f"Sensitive file included in image build input: {rel}", file=sys.stderr
            )
            failed = True
            continue
        try:
            content = path.read_bytes()
        except OSError as exc:
            print(f"Cannot inspect {rel}: {exc}", file=sys.stderr)
            failed = True
            continue
        # This test fixture deliberately exercises a credential-bearing proxy
        # URL using reserved example.test and dummy credentials.
        if rel.as_posix() == "business_flow/bundle_tag_evaluation_flow_test.go":
            content = content.replace(b"http://user:password@proxy.example.test:8080", b"")
        if any(pattern.search(content) for pattern in SECRET_PATTERNS):
            print(f"Possible secret in image build input: {rel}", file=sys.stderr)
            failed = True
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(main())
