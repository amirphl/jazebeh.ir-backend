#!/usr/bin/env python3
import json
import os
import re
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

MAX_LOG_LINE_CHARS = 1024 * 1024
MAX_LINES_PER_POLL = 1000
DEFAULT_HEALTHCHECK_FILE = "/tmp/nginx-sentry-forwarder-last-success"


def getenv(name: str, default: str = "") -> str:
    return os.getenv(name, default).strip()


class SentryStoreClient:
    def __init__(self) -> None:
        dsn = getenv("SENTRY_DSN")
        if not dsn:
            raise RuntimeError("SENTRY_DSN is not configured")

        parsed = urllib.parse.urlparse(dsn)
        if parsed.scheme not in ("http", "https"):
            raise RuntimeError("SENTRY_DSN scheme must be http or https")
        if not parsed.netloc or not parsed.username:
            raise RuntimeError("SENTRY_DSN is invalid")

        prefix, _, project_id = parsed.path.strip("/").rpartition("/")
        if not project_id or not project_id.isdigit():
            raise RuntimeError("SENTRY_DSN project ID must be numeric")

        hostname = parsed.hostname or ""
        if ":" in hostname and not hostname.startswith("["):
            hostname = f"[{hostname}]"
        try:
            port = parsed.port
        except ValueError as exc:
            raise RuntimeError("SENTRY_DSN has an invalid port") from exc
        if getenv("SENTRY_ENVIRONMENT").lower() == "staging" and (
            parsed.scheme != "http" or hostname != "sentry-beta" or port != 8000
        ):
            raise RuntimeError(
                "staging SENTRY_DSN must use http://<key>@sentry-beta:8000/sentry/<project-id>"
            )
        endpoint = hostname if port is None else f"{hostname}:{port}"
        path_prefix = f"/{prefix}" if prefix else ""
        self.store_url = (
            f"{parsed.scheme}://{endpoint}{path_prefix}/api/{project_id}/store/"
        )
        sentry_key = urllib.parse.unquote(parsed.username)
        sentry_secret = urllib.parse.unquote(parsed.password or "")
        if not re.fullmatch(r"[A-Za-z0-9_-]+", sentry_key) or (
            sentry_secret and not re.fullmatch(r"[A-Za-z0-9_-]+", sentry_secret)
        ):
            raise RuntimeError("SENTRY_DSN contains invalid authentication characters")
        self.auth_header = (
            "Sentry sentry_version=7, "
            "sentry_client=yamata-nginx-forwarder/1.0, "
            f"sentry_key={sentry_key}, "
            f"sentry_secret={sentry_secret}"
        )
        self.environment = getenv("SENTRY_ENVIRONMENT", getenv("APP_ENV", "production"))
        self.release = getenv("SENTRY_RELEASE", getenv("VERSION", "unknown"))
        self.server_name = getenv("SENTRY_SERVER_NAME", "nginx-beta")
        try:
            self.timeout = float(getenv("SENTRY_TIMEOUT_SECONDS", "2"))
        except ValueError as exc:
            raise RuntimeError("SENTRY_TIMEOUT_SECONDS must be numeric") from exc
        if self.timeout <= 0:
            raise RuntimeError("SENTRY_TIMEOUT_SECONDS must be greater than zero")
        self.public_base_url = getenv("SENTRY_PUBLIC_BASE_URL")
        self.sentry_ui_domain = getenv("SENTRY_UI_DOMAIN")
        if self.public_base_url:
            try:
                public_url = urllib.parse.urlsplit(self.public_base_url)
                public_hostname = public_url.hostname
                public_port = public_url.port
            except ValueError as exc:
                raise RuntimeError("SENTRY_PUBLIC_BASE_URL is invalid") from exc
            if (
                public_url.scheme not in ("http", "https")
                or not public_hostname
                or public_url.username is not None
                or public_url.password is not None
                or public_url.query
                or public_url.fragment
                or (public_port is not None and not 1 <= public_port <= 65535)
            ):
                raise RuntimeError(
                    "SENTRY_PUBLIC_BASE_URL must be an HTTP(S) URL without credentials or query"
                )
            self.public_base_url = urllib.parse.urlunsplit(
                (
                    public_url.scheme,
                    public_url.netloc,
                    public_url.path.rstrip("/"),
                    "",
                    "",
                )
            )

    def send(self, payload: dict) -> None:
        body = json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            self.store_url,
            data=body,
            method="POST",
            headers={
                "Content-Type": "application/json",
                "X-Sentry-Auth": self.auth_header,
            },
        )
        with urllib.request.urlopen(request, timeout=self.timeout) as response:
            if response.status >= 400:
                raise RuntimeError(f"upstream returned {response.status}")

    def event(
        self,
        *,
        message: str,
        level: str,
        status: int | None,
        request_data: dict,
        extra: dict,
    ) -> dict:
        tags = {
            "log.source": "nginx",
        }
        if status is not None:
            tags["http.status_code"] = str(status)
        if "method" in request_data:
            tags["http.method"] = request_data["method"]

        return {
            "event_id": secrets.token_hex(16),
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "platform": "python",
            "logger": "yamata-nginx-forwarder",
            "level": level,
            "server_name": self.server_name,
            "environment": self.environment,
            "release": self.release,
            "message": message[:2048],
            "tags": tags,
            "request": request_data,
            "extra": extra,
        }


class Tailer:
    def __init__(self, path: str) -> None:
        self.path = path
        self.handle = None
        self._inode: int | None = None

    def open(self) -> None:
        handle = open(self.path, "r", encoding="utf-8", errors="replace")
        try:
            if handle.seekable():
                handle.seek(0, os.SEEK_END)
            inode = os.fstat(handle.fileno()).st_ino
        except Exception:
            handle.close()
            raise
        self.handle = handle
        self._inode = inode

    def close(self) -> None:
        if self.handle is not None:
            self.handle.close()
        self.handle = None
        self._inode = None

    def _rotated(self) -> bool:
        try:
            info = os.stat(self.path)
            return info.st_ino != self._inode or (
                self.handle is not None and info.st_size < self.handle.tell()
            )
        except FileNotFoundError:
            # A deleted log must not keep the forwarder healthy merely because
            # it still has an open descriptor for the old file.
            return True

    def poll(self) -> list[str]:
        if self.handle is None:
            self.open()
        elif self._rotated():
            self.close()
            self.open()

        lines = []
        while len(lines) < MAX_LINES_PER_POLL:
            line = self.handle.readline(MAX_LOG_LINE_CHARS + 1)
            if not line:
                break
            if len(line) > MAX_LOG_LINE_CHARS:
                while line and not line.endswith("\n"):
                    line = self.handle.readline(MAX_LOG_LINE_CHARS + 1)
                continue
            lines.append(line.rstrip("\n"))
        return lines


def parse_request(raw_request: str) -> tuple[str, str]:
    parts = raw_request.split(" ")
    if len(parts) < 2:
        return "", ""
    return parts[0], parts[1]


def record_healthcheck_success(path: str) -> None:
    """Record that both configured log files were successfully polled."""
    with open(path, "a", encoding="utf-8"):
        pass
    os.utime(path, None)


SENSITIVE_VALUE_RE = re.compile(
    r"(?i)(authorization|password|passwd|token|secret|api[_-]?key|session|cookie)"
    r"([=:][^\s&,;]+)"
)
BEARER_RE = re.compile(r"(?i)\bBearer\s+[A-Za-z0-9._~+/-]+=*")
URL_QUERY_RE = re.compile(r"((?:https?://|/)[^\s?'\"]+)\?[^\s'\"]+")


def redact_text(value: str) -> str:
    value = URL_QUERY_RE.sub(r"\1?[REDACTED]", value)
    value = BEARER_RE.sub("Bearer [REDACTED]", value)
    return SENSITIVE_VALUE_RE.sub(lambda match: match.group(1) + "=[REDACTED]", value)


def strip_query(value: str) -> str:
    if not value:
        return ""
    try:
        parsed = urllib.parse.urlsplit(value)
        return urllib.parse.urlunsplit(
            (parsed.scheme, parsed.netloc, parsed.path, "", "")
        )
    except ValueError:
        return value.split("?", 1)[0].split("#", 1)[0]


def build_request_url(
    base_url: str, path: str, host: str = "", scheme: str = ""
) -> str:
    path = strip_query(path)
    if base_url and path:
        return urllib.parse.urljoin(base_url.rstrip("/") + "/", path.lstrip("/"))
    if not path:
        return path
    safe_host = safe_log_host(host)
    if not safe_host:
        return path
    safe_scheme = scheme if scheme in ("http", "https") else "https"
    return f"{safe_scheme}://{safe_host}{path}"


def safe_log_host(value: str) -> str:
    value = value.strip()
    if not re.fullmatch(r"(?:[A-Za-z0-9-]+\.)*[A-Za-z0-9-]+(?::[0-9]{1,5})?", value):
        return ""
    return value


def should_skip_access_entry(
    client: SentryStoreClient, host: str, method: str, path: str
) -> bool:
    if host == client.sentry_ui_domain and method.upper() == "POST":
        if path.startswith("/api/") and path.endswith("/store/"):
            return True
        if path.startswith("/api/") and path.endswith("/envelope/"):
            return True
    return False


def handle_access_line(client: SentryStoreClient, line: str) -> None:
    if not line:
        return

    try:
        entry = json.loads(line)
    except json.JSONDecodeError:
        return

    try:
        status = int(entry.get("status", "0"))
    except ValueError:
        return

    if status < 400:
        return

    method, path = parse_request(entry.get("request", ""))
    path = strip_query(path)
    host = entry.get("host", "")
    safe_host = safe_log_host(host)
    scheme = entry.get("scheme", "https")
    if should_skip_access_entry(client, host, method, path):
        return

    request_data = {
        "method": method,
        "url": build_request_url(
            client.public_base_url, path, host=host, scheme=scheme
        ),
        "headers": {
            "Host": safe_host,
            "User-Agent": redact_text(entry.get("http_user_agent", ""))[:512],
        },
    }

    message = f"Nginx returned HTTP {status} for {method} {path}".strip()
    extra = {
        "request_time": entry.get("request_time"),
        "request_id": entry.get("request_id"),
        "upstream_addr": entry.get("upstream_addr"),
        "upstream_status": entry.get("upstream_status"),
        "upstream_response_time": entry.get("upstream_response_time"),
        "referrer": strip_query(entry.get("http_referrer", "")),
    }

    client.send(
        client.event(
            message=message,
            level="error" if status >= 500 else "warning",
            status=status,
            request_data=request_data,
            extra=extra,
        )
    )


def handle_error_line(client: SentryStoreClient, line: str) -> None:
    if not line:
        return

    request_data = {
        "url": build_request_url(client.public_base_url, "/"),
        "method": "NGINX",
    }
    safe_line = redact_text(line)[:4096]
    extra = {"line": safe_line}
    client.send(
        client.event(
            message=safe_line[:2048],
            level="error",
            status=None,
            request_data=request_data,
            extra=extra,
        )
    )


def main() -> int:
    access_log = getenv("NGINX_ACCESS_LOG", "/var/log/nginx/access.log")
    error_log = getenv("NGINX_ERROR_LOG", "/var/log/nginx/error.log")
    healthcheck_file = getenv(
        "NGINX_SENTRY_FORWARDER_HEALTHCHECK_FILE", DEFAULT_HEALTHCHECK_FILE
    )
    if getenv("SENTRY_ENVIRONMENT") == "staging" and not getenv("SENTRY_DSN"):
        print(
            "staging Sentry forwarder disabled until SENTRY_DSN is configured",
            flush=True,
        )
        while True:
            record_healthcheck_success(healthcheck_file)
            time.sleep(30)
    try:
        poll_interval = float(getenv("NGINX_LOG_POLL_INTERVAL_SECONDS", "0.5"))
    except ValueError:
        print("invalid NGINX_LOG_POLL_INTERVAL_SECONDS", file=sys.stderr)
        return 2
    if poll_interval <= 0:
        print(
            "NGINX_LOG_POLL_INTERVAL_SECONDS must be greater than zero", file=sys.stderr
        )
        return 2

    try:
        client = SentryStoreClient()
    except RuntimeError as exc:
        print(f"sentry forwarder configuration error: {exc}", file=sys.stderr)
        return 2

    access_tailer = Tailer(access_log)
    error_tailer = Tailer(error_log)

    while True:
        try:
            access_lines = access_tailer.poll()
            error_lines = error_tailer.poll()
            record_healthcheck_success(healthcheck_file)

            for line in access_lines:
                handle_access_line(client, line)
            for line in error_lines:
                handle_error_line(client, line)
        except FileNotFoundError:
            time.sleep(poll_interval)
        except urllib.error.URLError as exc:
            print(f"sentry forwarder network error: {exc}", file=sys.stderr)
            time.sleep(poll_interval)
        except Exception as exc:  # noqa: BLE001
            print(f"sentry forwarder error: {exc}", file=sys.stderr)
            time.sleep(poll_interval)

        time.sleep(poll_interval)


if __name__ == "__main__":
    raise SystemExit(main())
