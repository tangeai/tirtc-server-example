from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("create_esp32_project.py")
SPEC = importlib.util.spec_from_file_location("create_esp32_project", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class CreateEsp32ProjectTest(unittest.TestCase):
    def test_rejects_unsafe_project_name(self) -> None:
        for name in ("BadName", "../escape", "has-dash", "", "1device"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                MODULE.validate_project_name(name)

    def test_generates_focused_self_contained_project(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output = Path(temp_dir) / "camera_device"
            _, _, sdk = MODULE.repository_paths()
            MODULE.create_project(output, "camera_device", sdk)

            self.assertTrue((output / "CMakeLists.txt").is_file())
            self.assertTrue((output / "third_party/tirtc/lib/libTiRTC.a").is_file())
            self.assertFalse((output / "media").exists())
            self.assertTrue((output / "components/starter_runtime").is_dir())
            self.assertFalse((output / "components/session_runtime").exists())
            self.assertFalse((output / "components/device_core").exists())
            self.assertFalse((output / "components/starter_media_file").exists())

            platform_header = (
                output / "components/platform_client/include/platform_client.h"
            ).read_text(encoding="utf-8")
            platform_source = (
                output / "components/platform_client/src/platform_client.c"
            ).read_text(encoding="utf-8")
            self.assertNotIn("PLATFORM_SERVICE_VOIP", platform_header)
            self.assertNotIn("PLATFORM_SERVICE_CALL", platform_header)
            self.assertNotIn('"voip-srv"', platform_source)
            self.assertNotIn('"call-srv"', platform_source)

            cmake = (output / "CMakeLists.txt").read_text(encoding="utf-8")
            readme = (output / "README.md").read_text(encoding="utf-8")
            self.assertIn("project(camera_device)", cmake)
            self.assertNotIn("spiffs_create_partition_image", cmake)
            self.assertNotIn("audio.g711a", readme)
            self.assertNotIn("video.h264", readme)
            self.assertNotIn("@PROJECT_NAME@", readme)
            self.assertNotIn("VoIP", readme)
            self.assertNotIn("设备互呼", readme)

    def test_does_not_overwrite_existing_output(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output = Path(temp_dir) / "existing"
            output.mkdir()
            _, _, sdk = MODULE.repository_paths()
            with self.assertRaises(FileExistsError):
                MODULE.create_project(output, "existing", sdk)


if __name__ == "__main__":
    unittest.main()
