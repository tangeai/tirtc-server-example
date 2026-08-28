from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("doctor.py")
SPEC = importlib.util.spec_from_file_location("doctor", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class DoctorTest(unittest.TestCase):
    def test_version_matching_accepts_major_minor_line(self) -> None:
        self.assertTrue(MODULE.version_matches((5, 5, 2), "5.5"))
        self.assertTrue(MODULE.version_matches((5, 5, 2), "5.5.x"))
        self.assertFalse(MODULE.version_matches((5, 4, 4), "5.5"))

    def test_parse_idf_version(self) -> None:
        self.assertEqual((5, 5, 1), MODULE.parse_version("ESP-IDF v5.5.1"))
        self.assertEqual((5, 5), MODULE.parse_version("v5.5"))
        self.assertIsNone(MODULE.parse_version("unknown"))

    def test_contract_comparison_accepts_explicit_unset(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            contract = root / "build-contract.env"
            config = root / "sdkconfig.defaults"
            contract.write_text(
                "CONFIG_FREERTOS_HZ=1000\n"
                "CONFIG_FREERTOS_USE_TRACE_FACILITY=off\n"
                "CONFIG_FREERTOS_USE_STATS_FORMATTING_FUNCTIONS=off\n"
                "CONFIG_FREERTOS_GENERATE_RUN_TIME_STATS=off\n",
                encoding="utf-8",
            )
            config.write_text(
                "CONFIG_FREERTOS_HZ=1000\n"
                "# CONFIG_FREERTOS_USE_TRACE_FACILITY is not set\n"
                "# CONFIG_FREERTOS_USE_STATS_FORMATTING_FUNCTIONS is not set\n"
                "# CONFIG_FREERTOS_GENERATE_RUN_TIME_STATS is not set\n",
                encoding="utf-8",
            )
            self.assertEqual(
                [],
                MODULE.compare_contract(
                    MODULE.parse_env_file(contract), MODULE.parse_kconfig(config)
                ),
            )

    def test_contract_comparison_reports_mismatch(self) -> None:
        contract = {key: "off" for key in MODULE.CONTRACT_KEYS}
        contract["CONFIG_FREERTOS_HZ"] = "1000"
        config = dict(contract)
        config["CONFIG_FREERTOS_HZ"] = "100"
        mismatches = MODULE.compare_contract(contract, config)
        self.assertEqual(
            ["CONFIG_FREERTOS_HZ: expected 1000, got 100"], mismatches
        )


if __name__ == "__main__":
    unittest.main()
