import io
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import count_characters
import extract_pg_dump_copy
import load_dotenv
import nginx_sentry_forwarder
import resend_torobpay_sms
import script_common


class DumpFilterTests(unittest.TestCase):
    def test_only_allowlisted_copy_is_emitted(self):
        content = """-- PostgreSQL dump
DROP TABLE public.customers;
COPY public.audience_profiles (id, uid) FROM stdin;
1\tu1
\\.
\\! touch /tmp/should-not-run
"""
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "dump.sql"
            source.write_text(content, encoding="utf-8")
            output = io.StringIO()
            found = extract_pg_dump_copy.extract_copy_sections(
                source, {"audience_profiles"}, output
            )
        self.assertEqual(found, {"audience_profiles"})
        self.assertEqual(
            output.getvalue(),
            "COPY public.audience_profiles (id, uid) FROM stdin;\n1\tu1\n\\.\n",
        )

    def test_unexpected_copy_table_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "dump.sql"
            source.write_text(
                "COPY public.secrets (id) FROM stdin;\n1\n\\.\n", encoding="utf-8"
            )
            with self.assertRaises(extract_pg_dump_copy.DumpValidationError):
                extract_pg_dump_copy.extract_copy_sections(
                    source, {"audience_profiles"}, io.StringIO()
                )


class SentryForwarderTests(unittest.TestCase):
    def test_staging_rejects_external_sentry(self):
        with mock.patch.dict(
            os.environ,
            {
                "SENTRY_DSN": "https://key@example.com/1",
                "SENTRY_ENVIRONMENT": "staging",
            },
            clear=True,
        ):
            with self.assertRaises(RuntimeError):
                nginx_sentry_forwarder.SentryStoreClient()

    def test_staging_accepts_internal_glitchtip_dsn(self):
        with mock.patch.dict(
            os.environ,
            {
                "SENTRY_DSN": "http://key@sentry-beta:8000/sentry/1",
                "SENTRY_ENVIRONMENT": "staging",
            },
            clear=True,
        ):
            client = nginx_sentry_forwarder.SentryStoreClient()
        self.assertEqual(
            client.store_url, "http://sentry-beta:8000/sentry/api/1/store/"
        )

    def test_dsn_credentials_are_not_in_store_url(self):
        with mock.patch.dict(
            os.environ,
            {"SENTRY_DSN": "http://public:secret@sentry:9000/1"},
            clear=True,
        ):
            client = nginx_sentry_forwarder.SentryStoreClient()
        self.assertEqual(client.store_url, "http://sentry:9000/api/1/store/")
        self.assertNotIn("secret", client.store_url)

    def test_query_and_sensitive_values_are_redacted(self):
        self.assertEqual(
            nginx_sentry_forwarder.strip_query("/reset?token=secret#x"), "/reset"
        )
        redacted = nginx_sentry_forwarder.redact_text(
            "GET /reset?email=user@example.com authorization=Bearer.abc password=hunter2 token=xyz"
        )
        self.assertNotIn("hunter2", redacted)
        self.assertNotIn("xyz", redacted)
        self.assertNotIn("user@example.com", redacted)

    def test_configured_public_origin_wins_over_untrusted_host_header(self):
        self.assertEqual(
            nginx_sentry_forwarder.build_request_url(
                "https://api.example.com", "/health?token=x", host="evil.example"
            ),
            "https://api.example.com/health",
        )

    def test_healthcheck_marker_is_created_and_refreshed(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / "forwarder-health"
            nginx_sentry_forwarder.record_healthcheck_success(str(marker))
            first_mtime = marker.stat().st_mtime_ns
            nginx_sentry_forwarder.record_healthcheck_success(str(marker))
            self.assertTrue(marker.is_file())
            self.assertGreaterEqual(marker.stat().st_mtime_ns, first_mtime)

    def test_tailer_retries_when_a_rotated_log_is_temporarily_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory) / "access.log"
            log.write_text("old entry\n", encoding="utf-8")
            tailer = nginx_sentry_forwarder.Tailer(str(log))
            self.addCleanup(tailer.close)
            self.assertEqual(tailer.poll(), [])

            log.unlink()
            with self.assertRaises(FileNotFoundError):
                tailer.poll()
            self.assertIsNone(tailer.handle)

            log.write_text("replacement entry\n", encoding="utf-8")
            self.assertEqual(tailer.poll(), [])
            with log.open("a", encoding="utf-8") as handle:
                handle.write("new entry\n")
            self.assertEqual(tailer.poll(), ["new entry"])


class EnvironmentLoaderTests(unittest.TestCase):
    def test_dotenv_values_are_data_and_reserved_shell_variables_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env.beta"
            path.write_text(
                "DB_PASSWORD='literal $(never executed)'\nDOMAIN=example.com # comment\n",
                encoding="utf-8",
            )
            self.assertEqual(
                load_dotenv.parse_dotenv(path),
                [
                    ("DB_PASSWORD", "literal $(never executed)"),
                    ("DOMAIN", "example.com"),
                ],
            )
            path.write_text("PATH=/tmp/attacker\n", encoding="utf-8")
            with self.assertRaises(load_dotenv.DotenvError):
                load_dotenv.parse_dotenv(path)

    def test_shell_loader_does_not_execute_command_substitution(self):
        with tempfile.TemporaryDirectory() as directory:
            directory_path = Path(directory)
            marker = directory_path / "executed"
            dotenv = directory_path / ".env.beta"
            dotenv.write_text(
                f"DB_PASSWORD=$(touch {marker})\nDOMAIN=example.com\n",
                encoding="utf-8",
            )
            helper = Path(__file__).with_name("load-yamata-env.sh")
            result = subprocess.run(
                [
                    "bash",
                    "-c",
                    'source "$1"; load_yamata_env_file "$2"; printf "%s" "$DB_PASSWORD"',
                    "bash",
                    str(helper),
                    str(dotenv),
                ],
                check=True,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.stdout, f"$(touch {marker})")
            self.assertFalse(marker.exists())

    def test_unresolved_provisioning_placeholder_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env.beta"
            path.write_text(
                'DB_PASSWORD="$db_password"\nDOMAIN="api.$domain"\n',
                encoding="utf-8",
            )
            with self.assertRaises(load_dotenv.DotenvError):
                load_dotenv.parse_dotenv(path)

    def test_shell_loader_suppresses_xtrace_secret_output(self):
        with tempfile.TemporaryDirectory() as directory:
            dotenv = Path(directory) / ".env.beta"
            dotenv.write_text("DB_PASSWORD=trace-secret-value\n", encoding="utf-8")
            helper = Path(__file__).with_name("load-yamata-env.sh")
            result = subprocess.run(
                [
                    "bash",
                    "-x",
                    "-c",
                    'source "$1"; load_yamata_env_file "$2"; test -n "$DB_PASSWORD"',
                    "bash",
                    str(helper),
                    str(dotenv),
                ],
                check=True,
                capture_output=True,
                text=True,
            )
            self.assertNotIn("trace-secret-value", result.stderr)

    def test_https_origin_rejects_userinfo(self):
        parser = mock.MagicMock()
        parser.error.side_effect = ValueError
        with self.assertRaises(ValueError):
            script_common.validate_https_origin(
                parser, "--jazebeh-domain", "https://user:secret@example.com"
            )


class UtilityConsistencyTests(unittest.TestCase):
    def test_displayed_expansion_matches_counted_short_link(self):
        text = "Visit {YOUR_LINK}"
        expanded = count_characters.expand_text(text, "https://long", "jo1n.ir")
        self.assertEqual(expanded, "Visit jo1n.ir/123456")
        self.assertEqual(
            count_characters.count_characters(text, "https://long", "jo1n.ir"),
            len(expanded) + 6,
        )

    def test_sms_token_password_is_sent_in_post_body_not_url(self):
        response = mock.MagicMock()
        response.status = 200
        response.read.return_value = json.dumps({"access_token": "token"}).encode()
        response.__enter__.return_value = response
        config = {
            **resend_torobpay_sms.PAYAM_SMS,
            "token_url": "https://sms.example/token",
            "system_name": "system",
            "username": "user",
            "password": "secret-password",
            "root_access_token": "",
        }
        with (
            mock.patch.object(resend_torobpay_sms, "PAYAM_SMS", config),
            mock.patch("urllib.request.urlopen", return_value=response) as urlopen,
        ):
            self.assertEqual(resend_torobpay_sms.get_token(5), "token")
        request = urlopen.call_args.args[0]
        self.assertEqual(request.full_url, "https://sms.example/token")
        self.assertNotIn("secret-password", request.full_url)
        self.assertIn(b"password=secret-password", request.data)

    def test_sms_audit_key_contains_no_recipient_or_row_identifier(self):
        key = resend_torobpay_sms.row_key("customer-secret", "+989121234567", "body")
        self.assertTrue(key.startswith("v2|"))
        self.assertNotIn("customer-secret", key)
        self.assertNotIn("989121234567", key)


class DeploymentMigrationSafetyTests(unittest.TestCase):
    @staticmethod
    def _paths():
        scripts_dir = Path(__file__).resolve().parent
        return (
            scripts_dir.parent,
            scripts_dir / "apply-yamata-required-migrations.sh",
            scripts_dir / "deploy-beta.sh",
        )

    @staticmethod
    def _fake_docker(directory: Path):
        docker_log = directory / "docker.log"
        docker = directory / "docker"
        docker.write_text(
            """#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >> "$FAKE_DOCKER_LOG"
case "${1:-}" in
  info) exit 0 ;;
  inspect)
    if [ "${2:-}" = "-f" ]; then printf 'true\\n'; fi
    exit 0
    ;;
  exec)
    if [ "${3:-}" = "printenv" ]; then
      case "${4:-}" in
        POSTGRES_USER) printf 'test_user\\n' ;;
        POSTGRES_DB) printf 'test_db\\n' ;;
      esac
    else
      printf 't\\n'
    fi
    exit 0
    ;;
esac
exit 1
""",
            encoding="utf-8",
        )
        docker.chmod(0o755)
        return docker_log

    def test_routine_deploy_uses_read_only_schema_verification(self):
        _, _, deploy = self._paths()
        content = deploy.read_text(encoding="utf-8")
        self.assertIn("apply-yamata-required-migrations.sh --verify-only", content)

    def test_routine_deploy_recreates_nginx_to_refresh_file_bind_mounts(self):
        _, _, deploy = self._paths()
        content = deploy.read_text(encoding="utf-8")
        self.assertIn("up -d --force-recreate nginx-beta", content)

    def test_routine_deploy_does_not_gate_on_the_nginx_sentry_forwarder(self):
        _, _, deploy = self._paths()
        content = deploy.read_text(encoding="utf-8")
        self.assertNotIn("wait_for_nginx_sentry_forwarder_health", content)
        self.assertIn(
            "nginx-sentry-forwarder-beta health check continues in background",
            content,
        )

    def test_nginx_sentry_forwarder_healthcheck_is_prompt(self):
        compose = Path(__file__).resolve().parents[1] / "docker-compose.beta.yml"
        content = compose.read_text(encoding="utf-8")
        forwarder = content.split("  nginx-sentry-forwarder-beta:\n", 1)[1].split(
            "  cert-monitor-beta:\n", 1
        )[0]
        self.assertIn("interval: 5s", forwarder)
        self.assertIn("start_period: 30s", forwarder)

    def test_pgadmin_query_autocomplete_is_provisioned_and_enforced(self):
        root = Path(__file__).resolve().parents[1]
        preferences = json.loads(
            (root / "docker/pgadmin/preferences.json").read_text(encoding="utf-8")
        )
        self.assertTrue(
            preferences["preferences"][
                "sqleditor:auto_completion:autocomplete_on_key_press"
            ]
        )

        compose = (root / "docker-compose.beta.yml").read_text(encoding="utf-8")
        self.assertIn(
            "PGADMIN_PREFERENCES_JSON_FILE: /pgadmin4/preferences.json", compose
        )
        self.assertIn(
            "./docker/pgadmin/preferences.json:/pgadmin4/preferences.json:ro",
            compose,
        )

        deploy = (root / "scripts/deploy-beta.sh").read_text(encoding="utf-8")
        self.assertIn("configure_pgadmin_query_autocomplete", deploy)
        self.assertIn(
            "sqleditor:auto_completion:autocomplete_on_key_press=true", deploy
        )

    def test_production_check_only_warns_for_degraded_sentry_forwarding(self):
        production_check = (
            Path(__file__).resolve().parents[1] / "scripts/check-yamata-production.sh"
        )
        content = production_check.read_text(encoding="utf-8")
        required_health_checks = content.split("for container in \\\n", 2)[2].split(
            "done\n", 1
        )[0]
        self.assertNotIn("yamata-nginx-sentry-forwarder-beta", required_health_checks)
        self.assertIn("Nginx errors may not reach Sentry", content)

    def test_required_schema_helper_is_read_only_by_default(self):
        project_root, helper, _ = self._paths()
        with tempfile.TemporaryDirectory() as directory:
            directory_path = Path(directory)
            docker_log = self._fake_docker(directory_path)
            env = {
                **os.environ,
                "PATH": f"{directory_path}:{os.environ['PATH']}",
                "FAKE_DOCKER_LOG": str(docker_log),
            }
            result = subprocess.run(
                [str(helper), str(project_root)],
                check=True,
                capture_output=True,
                text=True,
                env=env,
            )
            docker_calls = docker_log.read_text(encoding="utf-8")

        self.assertIn("Verification mode: no migrations will be applied", result.stdout)
        self.assertIn("Required schema verified", result.stdout)
        self.assertNotIn("Applying ", result.stdout)
        self.assertNotIn("exec -i", docker_calls)

    def test_required_schema_modes_are_mutually_exclusive(self):
        project_root, helper, _ = self._paths()
        result = subprocess.run(
            [str(helper), "--repair", "--verify-only", str(project_root)],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("mutually exclusive", result.stderr)


if __name__ == "__main__":
    unittest.main()
