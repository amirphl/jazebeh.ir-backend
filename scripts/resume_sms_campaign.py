#!/usr/bin/env python3
"""Safely resume the unsent tail of one SMS campaign.

The command is deliberately dry-run by default.  It is intended for an outage
where the Go campaign dispatcher is stopped; the normal Go status-job worker
can be started after this command has finished.
"""
from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from load_dotenv import DotenvError, parse_dotenv

try:
    import psycopg
    from psycopg.rows import dict_row
    import requests
except ImportError as exc:  # pragma: no cover
    raise SystemExit("install scripts requirements first: pip install -r scripts/requirements.txt") from exc


PAYAM_BATCH, CANDOO_BATCH = 200, 100
DATABASE_HOST = "172.30.0.10"
LOGGER = logging.getLogger("resume_sms_campaign")


class ResumeError(RuntimeError):
    pass


@dataclass(frozen=True)
class Recipient:
    audience_id: int
    phone: str
    uid: str
    code: str


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


def body(spec: dict[str, Any], code: str, uid: str) -> str:
    content = str(spec.get("content") or "")
    adlink = str(spec.get("adlink") or "")
    if adlink:
        domain = str(spec.get("short_link_domain") or "").strip()
        link = (domain.rstrip("/") + "/" + code) if domain else adlink.replace("{uid}", uid)
        return content.replace("{YOUR_LINK}", link) + "\nلغو۱۱"
    return content.replace("{YOUR_LINK}", "") + "\nلغو۱۱"


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument(
        "--env-file",
        type=Path,
        default=Path(__file__).resolve().parent.parent / ".env.beta",
        help="dotenv file to load; defaults to the repository-root .env.beta",
    )
    p.add_argument("--campaign-id", type=int, required=True)
    p.add_argument(
        "--log-file",
        type=Path,
        help="Append operational progress to this file; defaults to resume-sms-campaign-<id>.log",
    )
    p.add_argument("--execute", action="store_true", help="permit DB writes and provider requests")
    p.add_argument("--confirm-campaign-id", type=int, help="must equal --campaign-id with --execute")
    p.add_argument("--concurrency", type=int, default=1, help="sequential by default for outage recovery")
    args = p.parse_args()
    if args.execute and args.confirm_campaign_id != args.campaign_id:
        p.error("--execute requires --confirm-campaign-id equal to --campaign-id")
    if args.concurrency != 1:
        p.error("only sequential dispatch is supported; rerun one campaign at a time")
    if args.log_file is None:
        args.log_file = Path(f"resume-sms-campaign-{args.campaign_id}.log")
    return args


def setup_logging(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    handler = logging.FileHandler(path, encoding="utf-8")
    try:
        os.chmod(path, 0o600)
    except OSError:
        pass
    formatter = logging.Formatter("%(asctime)s %(levelname)s %(message)s", "%Y-%m-%dT%H:%M:%SZ")
    formatter.converter = time.gmtime
    handler.setFormatter(formatter)
    console = logging.StreamHandler()
    console.setFormatter(formatter)
    LOGGER.handlers.clear()
    LOGGER.setLevel(logging.INFO)
    LOGGER.addHandler(handler)
    LOGGER.addHandler(console)
    LOGGER.propagate = False


def load_environment(path: Path) -> None:
    """Load a strict dotenv file without replacing explicitly exported values."""
    try:
        values = parse_dotenv(path)
    except (DotenvError, UnicodeError) as exc:
        raise ResumeError(f"cannot load env file {path}: {exc}") from exc
    for key, value in values:
        os.environ.setdefault(key, value)


def env(name: str, default: str = "", required: bool = False) -> str:
    value = os.getenv(name, default).strip()
    if required and not value:
        raise ResumeError(f"{name} is required for --execute")
    return value


def env_int(name: str, default: int, *, minimum: int = 0, maximum: int | None = None) -> int:
    """Read an optional integer; an explicitly empty dotenv value uses default."""
    raw = env(name)
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ResumeError(f"{name} must be an integer") from exc
    if value < minimum or (maximum is not None and value > maximum):
        upper = f" and <= {maximum}" if maximum is not None else ""
        raise ResumeError(f"{name} must be >= {minimum}{upper}")
    return value


def env_float(name: str, default: float, *, minimum: float) -> float:
    """Read an optional positive float; an explicitly empty dotenv value uses default."""
    raw = env(name)
    if not raw:
        return default
    try:
        value = float(raw)
    except ValueError as exc:
        raise ResumeError(f"{name} must be a number") from exc
    if value < minimum:
        raise ResumeError(f"{name} must be >= {minimum}")
    return value


def database_connection(*, read_only: bool = False):
    """Connect from the deployment's DB_* dotenv settings, never its DB_HOST."""
    kwargs: dict[str, Any] = {
        "host": DATABASE_HOST,
        "port": env("DB_PORT", "5432"),
        "dbname": env("DB_NAME", required=True),
        "user": env("DB_USER", required=True),
        "password": env("DB_PASSWORD", required=True),
        "sslmode": env("DB_SSL_MODE", "require"),
        "connect_timeout": 10,
        "row_factory": dict_row,
    }
    conn = psycopg.connect(**kwargs)
    if read_only:
        conn.execute("SET TRANSACTION READ ONLY")
    return conn


class Resume:
    def __init__(self, campaign_id: int, execute: bool):
        self.id, self.live_mode = campaign_id, execute
        self.db = database_connection() if execute else None
        self.http = requests.Session()

    def close(self) -> None:
        if self.db:
            self.db.close()

    @staticmethod
    def database_identity(conn) -> dict[str, str]:
        row = conn.execute("""
            SELECT current_database() AS database_name,
                   current_user AS database_user,
                   inet_server_addr()::text AS server_address,
                   inet_server_port()::text AS server_port
        """).fetchone()
        return dict(row)

    def validate_provider_configuration(self, provider: str) -> None:
        """Fail configuration problems before the no-replay intent boundary."""
        if provider == "payamsms":
            env("PAYAM_SMS_USERNAME", required=True)
            env("PAYAM_SMS_PASSWORD", required=True)
            env("PAYAM_SMS_ROOT_ACCESS_TOKEN", required=True)
            return
        if provider == "candoo":
            env("CANDOO_SMS_API_KEY", required=True)
            env_int("CANDOO_SMS_MESSAGE_TYPE", 0, maximum=4)
            env_int("CANDOO_SMS_RETRY_COUNT", 0, maximum=10)
            env_int("CANDOO_SMS_VALIDITY_PERIOD", 0, maximum=172800)
            env_float("CANDOO_SMS_TIMEOUT", 30.0, minimum=0.001)
            return
        raise ResumeError(f"unsupported provider {provider}")

    def load(self, conn, *, lock: bool = True) -> tuple[dict[str, Any], list[Recipient], int, str, str]:
        row = conn.execute("""
          SELECT c.id,c.status,c.spec,pc.id pc_id,pc.audience_ids,pc.audience_codes,
                 pc.last_audience_id,pc.campaign_json,ln.provider
          FROM campaigns c JOIN processed_campaigns pc ON pc.campaign_id=c.id AND pc.is_current
          LEFT JOIN line_numbers ln ON ln.line_number = c.spec->>'line_number'
          WHERE c.id=%s
        """ + (" FOR UPDATE OF c,pc" if lock else ""), (self.id,)).fetchone()
        if not row: raise ResumeError("campaign/current processed checkpoint not found")
        if row["status"] not in ("interrupted", "approved"): raise ResumeError(f"status must be interrupted or approved, got {row['status']}")
        spec = row["campaign_json"] if isinstance(row["campaign_json"], dict) else json.loads(row["campaign_json"])
        if str(spec.get("platform", "")).lower() != "sms": raise ResumeError("campaign is not SMS")
        sender = str(spec.get("line_number") or "").strip()
        provider = str(row["provider"] or "payamsms").strip().lower()
        if not sender or provider not in ("payamsms", "candoo"): raise ResumeError("missing sender or unsupported line provider")
        ids, codes = list(row["audience_ids"] or []), list(row["audience_codes"] or [])
        if len(ids) != len(codes): raise ResumeError("audience_ids/audience_codes length mismatch")
        checkpoint = row["last_audience_id"]
        pos = 0 if checkpoint is None else (ids.index(checkpoint) + 1 if checkpoint in ids else -1)
        if pos < 0: raise ResumeError("last_audience_id is not in the stored audience order")
        durable = conn.execute("SELECT count(*) n FROM sent_sms WHERE processed_campaign_id=%s AND nullif(btrim(phone_number),'') IS NOT NULL", (row["pc_id"],)).fetchone()["n"]
        if durable != pos: raise ResumeError(f"checkpoint prefix is {pos}, but durable send intents are {durable}")
        profiles = conn.execute("SELECT id,phone_number,uid FROM audience_profiles WHERE id = ANY(%s)", (ids[pos:],)).fetchall()
        by_id = {p["id"]: p for p in profiles}
        recipients: list[Recipient] = []
        for i, audience_id in enumerate(ids[pos:], start=pos):
            p = by_id.get(audience_id)
            if not p or not p["phone_number"] or not p["uid"]: raise ResumeError(f"audience {audience_id} is missing phone_number or uid")
            recipients.append(Recipient(audience_id, p["phone_number"], p["uid"], codes[i]))
        return row, recipients, pos, sender, provider

    def dry_run(self) -> None:
        # Dry-run intentionally uses a short read-only connection, never a lock/write connection.
        with database_connection(read_only=True) as conn:
            row, recipients, pos, sender, provider = self.load(conn, lock=False)
            identity = self.database_identity(conn)
        size = PAYAM_BATCH if provider == "payamsms" else CANDOO_BATCH
        LOGGER.info("dry run: campaign=%s status=%s provider=%s checkpoint=%s tail=%s database=%s",
                    self.id, row["status"], provider, pos, len(recipients), identity)
        print(json.dumps({"campaign_id": self.id, "status": row["status"], "provider": provider,
                          "sender": sender, "processed_campaign_id": row["pc_id"], "checkpoint_position": pos,
                          "tail_count": len(recipients), "batch_size": size,
                          "expected_batches": (len(recipients)+size-1)//size,
                          "database": identity}, default=str, indent=2))

    def bot_token(self) -> str:
        base = env("BOT_API_DOMAIN", "https://jazebeh.ir", True).rstrip("/")
        r = self.http.post(base + "/api/v1/bot/auth/login", json={"username": env("BOT_USERNAME", required=True), "password": env("BOT_PASSWORD", required=True)}, timeout=30)
        r.raise_for_status()
        payload = r.json()
        if not payload.get("success", False):
            raise ResumeError(f"Bot login failed: {payload.get('message', 'unspecified API error')}")
        data = payload.get("data") or {}
        session = data.get("session") or {}
        token = session.get("access_token")
        if not isinstance(token, str) or not token.strip():
            raise ResumeError("Bot login returned no session.access_token")
        return token

    def bot_transition(self, token: str, state: str) -> None:
        base = env("BOT_API_DOMAIN", "https://jazebeh.ir", True).rstrip("/")
        r = self.http.post(f"{base}/api/v1/bot/campaigns/{self.id}/{state}", headers={"Authorization": "Bearer " + token}, timeout=30)
        if not r.ok:
            # The Go client includes a bounded error body too; it is essential
            # for an operator to distinguish a transition conflict from a server
            # deployment/database failure, without dumping an unbounded page.
            detail = r.text.strip().replace("\n", " ")[:4096]
            raise ResumeError(f"Bot transition to {state} failed: HTTP {r.status_code}" + (f"; {detail}" if detail else ""))

    def allocate(self, cur, count: int, provider: str) -> tuple[list[str], list[int | None]]:
        cur.execute("SELECT last_value FROM sequence_counters WHERE name='sms_tracking_id' FOR UPDATE")
        row = cur.fetchone(); last = int((row or {"last_value": "0"})["last_value"], 16)
        ids = [f"{last+i:016x}" for i in range(1, count+1)]
        if row: cur.execute("UPDATE sequence_counters SET last_value=%s,updated_at=now() WHERE name='sms_tracking_id'", (ids[-1],))
        else: cur.execute("INSERT INTO sequence_counters(name,last_value) VALUES('sms_tracking_id',%s)", (ids[-1],))
        customers: list[int | None] = [None] * count
        if provider == "candoo":
            cur.execute("SELECT nextval('candoo_customer_id_seq') FROM generate_series(1,%s)", (count,))
            customers = [r["nextval"] for r in cur.fetchall()]
        return ids, customers

    def persist_intent(self, row, batch: list[Recipient], provider: str) -> tuple[list[str], list[int | None]]:
        with self.db.transaction():
            cur = self.db.cursor(row_factory=dict_row); tracking, customers = self.allocate(cur, len(batch), provider)
            cur.executemany("""INSERT INTO sent_sms(processed_campaign_id,phone_number,tracking_id,provider,provider_customer_id,status,parts_delivered)
                             VALUES(%s,%s,%s,%s,%s,'pending',0)""", [(row["pc_id"], x.phone, t, provider, c) for x,t,c in zip(batch,tracking,customers)])
            cur.execute("UPDATE processed_campaigns SET last_audience_id=%s,updated_at=now() WHERE id=%s", (batch[-1].audience_id,row["pc_id"]))
            cur.execute("UPDATE campaigns SET updated_at=now() WHERE id=%s AND status='approved'", (self.id,))
        return tracking, customers

    def send(self, provider: str, sender: str, spec: dict[str, Any], batch: list[Recipient], tracking: list[str], customers: list[int | None]) -> tuple[int | None, dict[str,str], str, list[dict[str,Any]]]:
        if provider == "payamsms":
            token_url=env("PAYAM_SMS_TOKEN_URL","https://www.payamsms.com/auth/oauth/token")
            q={"systemName":env("PAYAM_SMS_SYSTEM_NAME"),"username":env("PAYAM_SMS_USERNAME",required=True),"password":env("PAYAM_SMS_PASSWORD",required=True),"scope":env("PAYAM_SMS_SCOPE","webservice"),"grant_type":env("PAYAM_SMS_GRANT_TYPE","password")}
            h={"Authorization":"Basic "+env("PAYAM_SMS_ROOT_ACCESS_TOKEN",required=True)}
            token=self.http.post(token_url,params=q,headers=h,timeout=60); token.raise_for_status(); access=token.json()["access_token"]
            payload={"sender":sender,"smsItems":[{"recipient":x.phone,"body":body(spec,x.code,x.uid),"customerId":t} for x,t in zip(batch,tracking)]}
            r=self.http.post(env("PAYAM_SMS_SEND_URL","https://www.payamsms.com/panel/webservice/sendMultipleWithSrc"),json=payload,headers={"Authorization":"Bearer "+access},timeout=60)
            raw=r.text; status=r.status_code; headers=dict(r.headers); r.raise_for_status(); return status,headers,raw,r.json()
        message_type = env_int("CANDOO_SMS_MESSAGE_TYPE", 0, maximum=4)
        retry_count = env_int("CANDOO_SMS_RETRY_COUNT", 0, maximum=10)
        validity_period = env_int("CANDOO_SMS_VALIDITY_PERIOD", 0, maximum=172800)
        payload=[{"srcNum":sender,"recipient":x.phone,"body":body(spec,x.code,x.uid),"customerId":c,"type":message_type,"retryCount":retry_count,"validityPeriod":validity_period} for x,c in zip(batch,customers)]
        r=self.http.post(env("CANDOO_SMS_BASE_URL","https://api.candoosms.com").rstrip("/")+"/api/v3.0.1/send",json=payload,headers={"x-api-key":env("CANDOO_SMS_API_KEY",required=True)},timeout=env_float("CANDOO_SMS_TIMEOUT",30.0,minimum=0.001))
        raw=r.text; status=r.status_code; headers=dict(r.headers); r.raise_for_status(); return status,headers,raw,r.json()

    def record(self, row, provider, tracking, customers, http_status, headers, raw, responses, error=None) -> None:
        by_key = {(str(x.get("customerId")),): x for x in responses if isinstance(x,dict)} if provider == "candoo" else {(str(x.get("customerId") or x.get("trackingId") or x.get("tracking_id")),): x for x in responses if isinstance(x,dict)}
        with self.db.transaction():
            cur=self.db.cursor(); cur.execute("INSERT INTO sms_provider_send_attempts(processed_campaign_id,provider,tracking_ids,http_status_code,response_headers,response_body,error,attempt_count) VALUES(%s,%s,%s,%s,%s::jsonb,%s,%s,1)", (row["pc_id"],provider,tracking,http_status,json.dumps(headers),raw,error))
            if provider == "payamsms": cur.execute("INSERT INTO payam_sms_send_responses(processed_campaign_id,tracking_ids,http_status_code,response_headers,response_body,error,attempt_count) VALUES(%s,%s,%s,%s::jsonb,%s,%s,1)", (row["pc_id"],tracking,http_status,json.dumps(headers),raw,error))
            for t,c in zip(tracking,customers):
                item=by_key.get((str(c if provider=="candoo" else t),),{})
                server=str(item.get("messageId") or item.get("serverId") or item.get("trackingId") or "") or None
                rejected=provider=="candoo" and str(item.get("status","")).upper()=="REJECTED"
                cur.execute("UPDATE sent_sms SET server_id=%s,error_code=%s,description=%s,status=%s,parts_delivered=%s,updated_at=now() WHERE processed_campaign_id=%s AND tracking_id=%s", (server,str(item.get("statusCode")) if item else None,str(item.get("status")) if item else None,"unsuccessful" if rejected else "pending",0,row["pc_id"],t))
            poll_ids = tracking if provider == "payamsms" else [t for t,c in zip(tracking,customers) if str(by_key.get((str(c),),{}).get("status", "")).upper() == "ACCEPTED"]
            offsets=(10,20,1440) if provider=="payamsms" else (10,20)
            if poll_ids:
                correlation_id = str(uuid.uuid4())
                for minutes in offsets: cur.execute("INSERT INTO campaign_status_jobs(correlation_id,processed_campaign_id,platform,provider,tracking_ids,retry_count,scheduled_at) VALUES(%s,%s,'sms',%s,%s,0,now()+(%s||' minutes')::interval)", (correlation_id,row["pc_id"],provider,poll_ids,minutes))

    def execute(self) -> None:
        assert self.db
        if not self.db.execute("SELECT pg_try_advisory_lock(%s)", (self.id,)).fetchone()["pg_try_advisory_lock"]:
            raise ResumeError("campaign is locked by another resume process")
        try:
          with self.db.transaction():
            row, recipients, _, sender, provider = self.load(self.db)
            identity = self.database_identity(self.db)
            if row["status"] == "interrupted":
                self.db.execute("UPDATE campaigns SET status='approved',updated_at=now() WHERE id=%s AND status='interrupted'", (self.id,))
                LOGGER.info("campaign=%s transitioned interrupted -> approved directly", self.id)
          self.validate_provider_configuration(provider)
          spec=row["campaign_json"] if isinstance(row["campaign_json"],dict) else json.loads(row["campaign_json"])
          size=PAYAM_BATCH if provider=="payamsms" else CANDOO_BATCH
          total_batches = (len(recipients) + size - 1) // size
          LOGGER.info("campaign=%s start provider=%s tail=%s batch_size=%s batches=%s database=%s",
                      self.id, provider, len(recipients), size, total_batches, identity)
          for batch_number, start in enumerate(range(0,len(recipients),size), start=1):
              batch=recipients[start:start+size]; tracking,customers=self.persist_intent(row,batch,provider)
              LOGGER.info("campaign=%s batch=%s/%s intent persisted recipients=%s", self.id, batch_number, total_batches, len(batch))
              try: result=self.send(provider,sender,spec,batch,tracking,customers)
              except Exception as exc:
                  # The intent is already durable. Retain an audit trail, then stop;
                  # retrying this batch could duplicate an accepted provider request.
                  self.record(row,provider,tracking,customers,None,{},"",[],str(exc))
                  LOGGER.error("campaign=%s batch=%s/%s provider request uncertain; stopping without replay", self.id, batch_number, total_batches)
                  raise ResumeError(f"provider result is uncertain after durable intent; do not replay: {exc}") from exc
              self.record(row,provider,tracking,customers,*result)
              LOGGER.info("campaign=%s batch=%s/%s provider response recorded http_status=%s", self.id, batch_number, total_batches, result[0])
          with self.db.transaction():
              result = self.db.execute("UPDATE campaigns SET status='executed',updated_at=now() WHERE id=%s AND status='approved'", (self.id,))
              if result.rowcount != 1:
                  raise ResumeError("campaign status changed during resume; refusing to mark executed")
          print(f"campaign {self.id}: resumed {len(recipients)} recipients and marked executed directly; database={identity}")
          LOGGER.info("campaign=%s completed and marked executed directly", self.id)
        finally:
          self.db.execute("SELECT pg_advisory_unlock(%s)", (self.id,))


def main() -> int:
    args=parse_args()
    try:
        setup_logging(args.log_file)
        LOGGER.info("starting campaign=%s execute=%s env_file=%s log_file=%s", args.campaign_id, args.execute, args.env_file, args.log_file)
        load_environment(args.env_file)
        job=Resume(args.campaign_id,args.execute)
        (job.execute() if args.execute else job.dry_run()); return 0
    except (ResumeError, psycopg.Error, requests.RequestException, ValueError) as exc:
        LOGGER.error("campaign=%s failed: %s", args.campaign_id, exc)
        print(f"ERROR: {exc}",file=sys.stderr); return 1
    finally:
        if "job" in locals(): job.close()

if __name__ == "__main__": raise SystemExit(main())
