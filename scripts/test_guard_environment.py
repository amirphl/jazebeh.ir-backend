import os
import unittest
from unittest import mock

from guard_environment import require_production_confirmation


class GuardEnvironmentTests(unittest.TestCase):
    def test_requires_environment_live_mode_and_confirmation(self):
        for environment, mode, confirmed in (
            ("", "live", True),
            ("staging", "mock", True),
            ("production", "mock", True),
            ("production", "live", False),
        ):
            with (
                self.subTest(environment=environment, mode=mode, confirmed=confirmed),
                mock.patch.dict(
                    os.environ,
                    {"APP_ENV": environment, "EXTERNAL_SERVICES_MODE": mode},
                    clear=True,
                ),
            ):
                with self.assertRaises(RuntimeError):
                    require_production_confirmation(confirmed)

    def test_allows_confirmed_production(self):
        with mock.patch.dict(
            os.environ,
            {"APP_ENV": "production", "EXTERNAL_SERVICES_MODE": "live"},
            clear=True,
        ):
            require_production_confirmation(True)


if __name__ == "__main__":
    unittest.main()
