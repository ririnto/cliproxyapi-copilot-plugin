import hashlib
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path


SCRIPT = Path(__file__).with_name("package-release.sh")
PLUGIN_ID = "cliproxyapi-copilot"
VERSION = "0.3.3"


class PackageReleaseTest(unittest.TestCase):
    def test_supported_targets_create_named_archives_and_checksums(self):
        targets = (
            ("linux", "amd64", f"{PLUGIN_ID}.so"),
            ("darwin", "arm64", f"{PLUGIN_ID}.dylib"),
        )
        for goos, goarch, filename in targets:
            with self.subTest(goos=goos, goarch=goarch):
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    script = root / "scripts" / SCRIPT.name
                    script.parent.mkdir()
                    script.write_bytes(SCRIPT.read_bytes())
                    plugin = root / "build" / "plugins" / goos / goarch / filename
                    plugin.parent.mkdir(parents=True)
                    plugin.write_bytes(b"release fixture")
                    result = subprocess.run(
                        ["sh", str(script), VERSION, goos, goarch],
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                    archive_name = f"{PLUGIN_ID}_{VERSION}_{goos}_{goarch}.zip"
                    archive = root / "dist" / archive_name
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertTrue(archive.is_file())
                    with zipfile.ZipFile(archive) as package:
                        self.assertEqual(package.namelist(), [filename])
                        self.assertEqual(package.read(filename), b"release fixture")
                    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
                    self.assertEqual(
                        (root / "dist" / "checksums.txt").read_text(),
                        f"{digest}  {archive_name}\n",
                    )

    def test_unsupported_target_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            script = root / "scripts" / SCRIPT.name
            script.parent.mkdir()
            script.write_bytes(SCRIPT.read_bytes())
            result = subprocess.run(
                ["sh", str(script), VERSION, "windows", "amd64"],
                check=False,
                capture_output=True,
                text=True,
            )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsupported target", result.stderr)


if __name__ == "__main__":
    unittest.main()
