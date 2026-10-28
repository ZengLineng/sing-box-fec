import argparse
import hashlib
import shutil
import tarfile
import tempfile
import zipfile
from pathlib import Path


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def package(artifacts, output, version):
    output.mkdir(parents=True, exist_ok=True)
    names = [f"fecbox-{system}-{arch}" for system in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")]
    names += ["fecbox-android-libbox", "fecbox-apple-libbox"]
    for name in names:
        source = artifacts / name
        if not source.is_dir():
            raise ValueError(f"Missing verified artifact: {name}")
        label = name.replace("fecbox-", f"fecbox-{version}-", 1)
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp) / label
            shutil.copytree(source, folder)
            (folder / "SHA256SUMS").unlink(missing_ok=True)
            if name == "fecbox-apple-libbox":
                with tarfile.open(folder / "Libbox.xcframework.tar.gz") as archive:
                    archive.extractall(folder, filter="data")
                (folder / "Libbox.xcframework.tar.gz").unlink()
            elif "libbox" not in name:
                extension = ".exe" if "windows" in name else ""
                binary = folder / (name + extension)
                binary.rename(folder / ("fecbox" + extension))
                (folder / "fecbox").chmod(0o755) if not extension else None
            sums = []
            for path in sorted(folder.rglob("*")):
                if path.is_file() and not path.is_symlink():
                    sums.append(f"{digest(path)}  {path.relative_to(folder)}")
            (folder / "SHA256SUMS").write_text("\n".join(sums) + "\n")
            if "windows" in name or "android" in name:
                shutil.make_archive(str(output / label), "zip", temp, label)
            else:
                with tarfile.open(output / (label + ".tar.gz"), "w:gz") as archive:
                    archive.add(folder, arcname=label)

    app = artifacts / "fecbox-android-app"
    apks = sorted(app.glob("*.apk"))
    if not apks or not any(p.name.endswith("-universal.apk") for p in apks):
        raise ValueError("Missing signed Android APKs")
    for path in apks + [app / "FECBox-version-metadata.json", app / "ANDROID_LICENSE", app / "ANDROID_SOURCE_COMMIT"]:
        shutil.copyfile(path, output / path.name)
    sums = [f"{digest(p)}  {p.name}" for p in sorted(output.iterdir()) if p.is_file()]
    (output / "SHA256SUMS").write_text("\n".join(sums) + "\n")
    (output / "RELEASE_NOTES.md").write_text(f"""FECBox {version}: authenticated WireGuard FEC with native Linux SOCKS egress.

Android users: install the matching arm64-v8a APK, or the universal APK. Application ID: `dev.zenglineng.fecbox`; it coexists with the official client. Import your private Android configuration before connecting. APKs use a persistent FECBox signing key. Android 7.0+ is required.

Desktop/server: download the matching OS/architecture archive. Android AAR and Apple XCFramework are SDKs; this release does not include an iOS App.

Both peers require this fork. The FEC format is incompatible with raw WireGuard and UDPspeeder. See the repository documentation for configuration, validation and upstream maintenance. Verify downloads with SHA256SUMS. Server and client credentials are supplied separately.
""")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version")
    args = parser.parse_args()
    version = args.version or (Path(__file__).parent / "FEC_VERSION").read_text().strip()
    package(args.artifacts, args.output, version)
