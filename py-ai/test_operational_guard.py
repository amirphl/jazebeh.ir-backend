import os
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

PY_AI_ROOT = Path(__file__).resolve().parent
if str(PY_AI_ROOT) not in sys.path:
    sys.path.insert(0, str(PY_AI_ROOT))

from operational_guard import (
    require_confirmed_execution,
    require_live_production_execution,
)


class OperationalGuardTests(unittest.TestCase):
    def test_live_production_write_requires_execute_and_live_production(self):
        with patch.dict(
            os.environ,
            {"APP_ENV": "production", "EXTERNAL_SERVICES_MODE": "live"},
            clear=True,
        ):
            with self.assertRaises(RuntimeError):
                require_live_production_execution(execute=False, action="test")
            require_live_production_execution(execute=True, action="test")

        for environment, mode in (
            ("development", "mock"),
            ("staging", "mock"),
            ("production", "mock"),
        ):
            with (
                self.subTest(environment=environment, mode=mode),
                patch.dict(
                    os.environ,
                    {"APP_ENV": environment, "EXTERNAL_SERVICES_MODE": mode},
                    clear=True,
                ),
            ):
                with self.assertRaises(RuntimeError):
                    require_live_production_execution(execute=True, action="test")

    def test_disruptive_execution_requires_a_valid_environment_pair(self):
        for environment, mode in (
            ("development", "mock"),
            ("staging", "mock"),
            ("production", "live"),
        ):
            with (
                self.subTest(environment=environment, mode=mode),
                patch.dict(
                    os.environ,
                    {"APP_ENV": environment, "EXTERNAL_SERVICES_MODE": mode},
                    clear=True,
                ),
            ):
                require_confirmed_execution(execute=True, action="test")

        with patch.dict(
            os.environ,
            {"APP_ENV": "production", "EXTERNAL_SERVICES_MODE": "mock"},
            clear=True,
        ):
            with self.assertRaises(RuntimeError):
                require_confirmed_execution(execute=True, action="test")


if __name__ == "__main__":
    unittest.main()
