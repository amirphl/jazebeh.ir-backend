import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


class StagingEdgeConfigTests(unittest.TestCase):
    def test_scheduler_metrics_and_mock_network_isolation(self):
        compose = (ROOT / "docker-compose.staging.yml").read_text()
        self.assertIn("yamata-network-beta:\n    internal: true", compose)
        self.assertIn("pgadmin-edge-network-beta:\n    internal: true", compose)
        self.assertIn('METRICS_ENABLED: "true"', compose)
        self.assertIn('METRICS_PORT: "9091"', compose)

        prometheus = (ROOT / "docker/prometheus_staging.yml").read_text()
        self.assertIn("yamata_campaign_scheduler", prometheus)
        self.assertIn("campaign-scheduler-beta:9091", prometheus)

    def test_path_specific_limits_and_vendor_headers(self):
        template = (
            ROOT / "docker/nginx/sites-available/staging.conf.template"
        ).read_text()
        for fragment in (
            "location ^~ /api/v1/auth/ { limit_req zone=auth",
            "location ^~ /api/v1/admin/auth/ { limit_req zone=auth",
            "location ~ ^/api/ { limit_req zone=api",
            "location ^~ /grafana/ { limit_req zone=grafana",
            "location ^~ /pgadmin/ { return 404; }",
        ):
            self.assertIn(fragment, template)
        for location in ("/grafana/", "/sentry/"):
            line = next(
                line
                for line in template.splitlines()
                if f"location ^~ {location}" in line
            )
            self.assertIn("include /etc/nginx/staging-vendor-security.conf", line)
            self.assertNotIn("proxy_pass_header Content-Security-Policy", line)

    def test_pgadmin_proxy_uses_vendor_csp_and_common_headers(self):
        template = (
            ROOT / "docker/nginx/sites-available/staging-pgadmin.conf.template"
        ).read_text()
        self.assertIn("include /etc/nginx/staging-vendor-security.conf", template)
        snippet = (ROOT / "docker/nginx/staging-vendor-security.conf").read_text()
        self.assertIn("$upstream_http_content_security_policy", snippet)
        for header in (
            "Strict-Transport-Security",
            "X-Frame-Options",
            "X-Content-Type-Options",
        ):
            self.assertIn(header, snippet)


if __name__ == "__main__":
    unittest.main()
