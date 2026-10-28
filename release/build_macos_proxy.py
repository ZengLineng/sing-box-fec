import argparse
import hashlib
import json
import plistlib
import re
import shutil
import subprocess
from pathlib import Path


def run(*args, **kwargs):
    subprocess.run(list(map(str, args)), check=True, **kwargs)


def build(core, output, version):
    root = Path(__file__).resolve().parent.parent
    source = root / "clients/macos-proxy"
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)-fec\.(\d+)", version)
    if not match:
        raise ValueError("Invalid FECBox version")
    numbers = list(map(int, match.groups()))
    build_number = numbers[0] * 1000000 + numbers[1] * 10000 + numbers[2] * 100 + numbers[3]
    output.mkdir(parents=True, exist_ok=True)
    stage = output / "stage"
    if stage.exists():
        shutil.rmtree(stage)
    app = stage / "FECBox Proxy.app"
    macos = app / "Contents/MacOS"
    resources = app / "Contents/Resources"
    macos.mkdir(parents=True)
    resources.mkdir()
    sdk = subprocess.check_output(["xcrun", "--sdk", "macosx", "--show-sdk-path"], text=True).strip()
    binaries = []
    core_files = []
    for arch in ["arm64", "x86_64"]:
        go_arch = "amd64" if arch == "x86_64" else arch
        engine = core / f"fecbox-darwin-{go_arch}"
        if not engine.is_file():
            raise ValueError(f"Missing core: {engine}")
        target = output / f"FECBox-{arch}"
        run("xcrun", "swiftc", "-parse-as-library", "-swift-version", "5", "-O", "-target", f"{arch}-apple-macos13.0", "-sdk", sdk,
            "-framework", "SwiftUI", "-framework", "AppKit", source / "ConfigRules.swift", source / "App.swift", "-o", target)
        binaries.append(target)
        core_files.append(engine)
    run("lipo", "-create", *binaries, "-output", macos / "FECBox")
    run("lipo", "-create", *core_files, "-output", macos / "fecbox-core")
    (macos / "FECBox").chmod(0o755)
    (macos / "fecbox-core").chmod(0o755)
    iconset = output / "AppIcon.iconset"
    run("xcrun", "swift", source / "MakeIcon.swift", iconset)
    run("iconutil", "-c", "icns", iconset, "-o", resources / "AppIcon.icns")
    info = {
        "CFBundleIdentifier": "dev.zenglineng.fecbox.macos.proxy",
        "CFBundleExecutable": "FECBox",
        "CFBundleName": "FECBox Proxy",
        "CFBundleDisplayName": "FECBox Proxy",
        "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": ".".join(match.groups()[:3]),
        "CFBundleVersion": str(build_number),
        "FECBoxVersion": version,
        "CFBundleIconFile": "AppIcon",
        "LSMinimumSystemVersion": "13.0",
        "NSHighResolutionCapable": True,
        "NSPrincipalClass": "NSApplication",
    }
    with (app / "Contents/Info.plist").open("wb") as target:
        plistlib.dump(info, target)
    for name in ["LICENSE", "docs/fec/THIRD_PARTY_NOTICES.txt"]:
        shutil.copyfile(root / name, resources / Path(name).name)
    run("codesign", "--force", "--sign", "-", "--options", "runtime", "--timestamp=none", macos / "fecbox-core")
    run("codesign", "--force", "--sign", "-", "--options", "runtime", "--timestamp=none", app)
    run("codesign", "--verify", "--deep", "--strict", app)
    # Smoke launch opens the UI only and never starts the engine or changes networking.
    run(macos / "FECBox", "--smoke-test", timeout=20)
    run(macos / "fecbox-core", "version")
    schema = subprocess.check_output([str(macos / "fecbox-core"), "schema"], text=True)
    if "wireguard-fec" not in schema:
        raise ValueError("Bundled core has no wireguard-fec registration")
    (stage / "Applications").symlink_to("/Applications")
    (stage / "使用说明.txt").write_text(
        "FECBox Proxy for macOS 13+\n\n"
        "将 FECBox Proxy.app 拖到 Applications，然后导入 Mac 本地代理 JSON 配置。\n"
        "点击启动仅开放本地 HTTP/SOCKS 代理。应用不会修改系统代理、默认路由或当前 VPN。\n"
        "本地代理版不运行 TUN；请在需要代理的应用中填写显示的代理地址。\n"
        "关闭窗口/退出应用会停止此应用启动的核心。\n\n"
        "此版本使用 ad-hoc 签名，未经过 Apple 公证。首次打开可能需要在系统设置的隐私与安全性中允许。\n"
        "配置含私钥，应使用该 Mac 专属身份；不要与 Android 共用身份。\n"
    )
    dmg = output / f"FECBox-Proxy-{version}-macos-universal.dmg"
    run("hdiutil", "create", "-volname", "FECBox Proxy", "-srcfolder", stage, "-format", "UDZO", "-ov", "-o", dmg)
    run("hdiutil", "verify", dmg)
    with dmg.open("rb") as data:
        digest = hashlib.file_digest(data, "sha256").hexdigest()
    dmg.with_suffix(".dmg.sha256").write_text(f"{digest}  {dmg.name}\n")
    manifest = {"version": version, "minimum_macos": "13.0", "architectures": ["arm64", "x86_64"],
                "signing": "ad-hoc", "notarized": False, "mode": "local-proxy", "dmg_sha256": digest}
    (output / f"FECBox-Proxy-{version}-BUILDINFO.json").write_text(json.dumps(manifest, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--core-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    build(args.core_dir.resolve(), args.output.resolve(), args.version)
