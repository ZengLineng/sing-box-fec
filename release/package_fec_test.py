import hashlib
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

from package_fec import package


class PackagingTest(unittest.TestCase):
    def test_all_platforms_and_checksums(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifacts = root / "artifacts"
            for system in ("linux", "darwin", "windows"):
                for arch in ("amd64", "arm64"):
                    name = f"fecbox-{system}-{arch}"
                    folder = artifacts / name
                    folder.mkdir(parents=True)
                    suffix = ".exe" if system == "windows" else ""
                    (folder / (name + suffix)).write_bytes(b"wireguard-fec test binary")
                    (folder / "LICENSE").write_text("license")
            android = artifacts / "fecbox-android-libbox"
            android.mkdir()
            (android / "libbox.aar").write_bytes(b"aar")
            apple = artifacts / "fecbox-apple-libbox"
            apple.mkdir()
            framework = root / "Libbox.xcframework"
            framework.mkdir()
            (framework / "Info.plist").write_text("plist")
            with tarfile.open(apple / "Libbox.xcframework.tar.gz", "w:gz") as archive:
                archive.add(framework, arcname=framework.name)
            app = artifacts / "fecbox-android-app"
            app.mkdir()
            (app / "FECBox-test-universal.apk").write_bytes(b"signed apk fixture")
            (app / "FECBox-version-metadata.json").write_text(json.dumps({"version_code": 1140301}))
            for name in ("ANDROID_LICENSE", "ANDROID_SOURCE_COMMIT"):
                (app / name).write_text(name)
            macos_proxy = artifacts / "fecbox-macos-proxy"
            macos_proxy.mkdir()
            dmg_name = "FECBox-Proxy-1.14.3-fec.1-macos-universal.dmg"
            (macos_proxy / dmg_name).write_bytes(b"verified dmg fixture")
            output = root / "output"
            package(artifacts, output, "1.14.3-fec.1")
            self.assertEqual(len(list(output.glob("*.zip"))), 3)
            self.assertEqual(len(list(output.glob("*.tar.gz"))), 5)
            self.assertEqual((output / dmg_name).read_bytes(), b"verified dmg fixture")
            for line in (output / "SHA256SUMS").read_text().splitlines():
                digest, name = line.split("  ", 1)
                self.assertEqual(digest, hashlib.sha256((output / name).read_bytes()).hexdigest())
            with tarfile.open(output / "fecbox-1.14.3-fec.1-linux-amd64.tar.gz") as archive:
                info = archive.getmember("fecbox-1.14.3-fec.1-linux-amd64/fecbox")
                self.assertTrue(info.mode & 0o111)


if __name__ == "__main__":
    unittest.main()
