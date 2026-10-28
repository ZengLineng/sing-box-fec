"""Build the upstream Apple GUI with the native local backend, using no Apple identity."""
import argparse
import hashlib
import json
import os
import plistlib
import re
import shutil
import subprocess
from pathlib import Path


def run(*args, **kwargs):
    subprocess.run(list(map(str, args)), check=True, **kwargs)


def build(sdk, output, architectures, packages=None):
    root = Path(__file__).resolve().parent.parent
    apple = root / "clients/apple"
    version = (root / "release/FEC_VERSION").read_text().strip()
    assert re.fullmatch(r"\d+\.\d+\.\d+-fec\.\d+", version)
    assert (sdk / "Info.plist").is_file(), "Expected a verified Libbox.xcframework"
    if sdk.resolve() != (apple / "Libbox.xcframework").resolve():
        shutil.copytree(sdk, apple / "Libbox.xcframework", dirs_exist_ok=True)
    output = output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, DISABLE_SWIFTLINT="1")
    args = ["xcodebuild", "build", "-project", apple / "sing-box.xcodeproj", "-scheme", "SFM", "-configuration", "Release",
            "-destination", "generic/platform=macOS", "-derivedDataPath", output / "DerivedData",
            "-skipPackagePluginValidation", "-skipMacroValidation", "CODE_SIGNING_ALLOWED=NO", "CODE_SIGNING_REQUIRED=NO",
            "CODE_SIGN_IDENTITY=-", "CODE_SIGN_ENTITLEMENTS=", "DEVELOPMENT_TEAM=", "ENABLE_APP_SANDBOX=NO",
            "BASE_PACKAGE_IDENTIFIER=dev.zenglineng.fecbox.macos", "APP_GROUP_IDENTIFIER=group.dev.zenglineng.fecbox.macos",
            "SWIFT_ACTIVE_COMPILATION_CONDITIONS=$(inherited) FECBOX_LOCAL", "ONLY_ACTIVE_ARCH=NO", "ARCHS=" + " ".join(architectures)]
    if packages:
        args += ["-clonedSourcePackagesDirPath", packages.resolve()]
    with (output / "xcodebuild.log").open("w") as log:
        result = subprocess.run(list(map(str, args)), env=env, stdout=log, stderr=subprocess.STDOUT)
    if result.returncode:
        print((output / "xcodebuild.log").read_text()[-24000:])
        raise RuntimeError(f"Xcode build failed; see {output / 'xcodebuild.log'}")
    stage = output / "stage"
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir()
    app = stage / "FECBox.app"
    shutil.copytree(output / "DerivedData/Build/Products/Release/FECBox.app", app, symlinks=True)
    assert not list(app.rglob("*.appex")) and not list(app.rglob("*.systemextension")), "The local build must not contain Apple VPN extensions"
    binaries = []
    for arch in architectures:
        target = output / ("fecbox-local-" + arch)
        goenv = dict(os.environ, GOOS="darwin", GOARCH={"x86_64": "amd64", "arm64": "arm64"}[arch], CGO_ENABLED="0")
        run("go", "build", "-trimpath", "-tags", (root / "release/FEC_BUILD_TAGS").read_text().strip(),
            "-ldflags", f"-s -w -X github.com/sagernet/sing-box/constant.Version={version}", "-o", target, "./cmd/fecbox-local", cwd=root, env=goenv)
        binaries.append(target)
    backend = app / "Contents/MacOS/fecbox-local"
    run("lipo", "-create", *binaries, "-output", backend)
    backend.chmod(0o755)
    info_path = app / "Contents/Info.plist"
    info = plistlib.loads(info_path.read_bytes())
    info.update(CFBundleName="FECBox", CFBundleDisplayName="FECBox", FECBoxVersion=version)
    info.pop("NSUbiquitousContainers", None)
    info_path.write_bytes(plistlib.dumps(info))
    resources = app / "Contents/Resources"
    run("xcrun", "swift", root / "clients/macos-proxy/MakeIcon.swift", output / "FECBox.iconset")
    run("iconutil", "-c", "icns", output / "FECBox.iconset", "-o", resources / "AppIcon.icns")
    for name in ["LICENSE", "docs/fec/THIRD_PARTY_NOTICES.txt"]:
        shutil.copyfile(root / name, resources / Path(name).name)
    shutil.copyfile(apple / "LICENSE", resources / "APPLE_GUI_LICENSE")
    for path in sorted(app.rglob("*"), key=lambda p: len(p.parts), reverse=True):
        if path.is_symlink():
            continue
        if path.suffix in [".framework", ".dylib", ".xpc"] or path == backend:
            run("codesign", "--force", "--sign", "-", "--timestamp=none", path)
    run("codesign", "--force", "--sign", "-", "--timestamp=none", app)
    run("codesign", "--verify", "--deep", "--strict", app)
    run("lipo", "-verify_arch", *architectures, app / "Contents/MacOS/FECBox")
    run(backend, "--version")
    run("ln", "-s", "/Applications", stage / "Applications")
    label = "universal" if len(architectures) == 2 else architectures[0]
    name = f"FECBox-{version}-macos-{label}-local.dmg"
    dmg = output / name
    run("hdiutil", "create", "-volname", "FECBox", "-srcfolder", stage, "-format", "UDZO", "-ov", dmg)
    run("hdiutil", "verify", dmg)
    digest = hashlib.sha256(dmg.read_bytes()).hexdigest()
    (output / (name + ".sha256")).write_text(f"{digest}  {name}\n")
    metadata = dict(version=version, core_source=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    apple_source=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=apple, text=True).strip(),
                    signing="ad-hoc", backend="native-utun", network_extension=False, notarized=False)
    (output / (name.replace(".dmg", "-BUILDINFO.json"))).write_text(json.dumps(metadata, indent=2) + "\n")
    print(dmg)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sdk", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--arch", choices=["arm64", "universal"], default="universal")
    parser.add_argument("--packages", type=Path)
    args = parser.parse_args()
    build(args.sdk, args.output, ["arm64", "x86_64"] if args.arch == "universal" else ["arm64"], args.packages)
