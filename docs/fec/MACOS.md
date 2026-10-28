# macOS 本机自签名客户端

FECBox 复用 SFM 的 SwiftUI 界面，包括配置管理、仪表盘、出站选择、连接和日志页面。应用标识为 `dev.zenglineng.fecbox.macos`，数据目录为 `~/Library/Application Support/FECBox`，与原版 SFM 分开。

本机版使用 ad-hoc 签名，不需要 Apple Developer ID，也不做公证。它不加载 Apple VPN 扩展，而是启动同包内的 `fecbox-local` 后端，直接使用 sing-box 核心的原生 macOS utun。导入 mixed/SOCKS 配置时以当前用户运行；导入 TUN 或 `system: true` Endpoint 配置时，点击启动会显示 macOS 管理员授权对话框。

## 使用

1. 下载名称含 `macos-universal-local.dmg` 的包并校验旁边的 SHA-256，将 FECBox.app 拖到 Applications。
2. 首次启动如被 Gatekeeper 拦截，在系统设置的“隐私与安全性”中对这个应用选择“仍要打开”。只允许这个本机应用，无需关闭全局安全检查。
3. 在原界面的配置页面导入自己的 FEC JSON，选择配置后点击启动。FEC 服务器必须具有对应的独立 WireGuard 公钥和地址。
4. 测试期间使用独立 loopback mixed 端口、独立客户端身份和 `auto_route: false`，通过显式代理验证。全局 TUN 配置会按 JSON 改变路由，只有准备好后再由用户启动。

本机版不提供 Apple VPN 的按需连接、系统 VPN 路由覆写或 iCloud 配置共享。这些页面在本机编译中隐藏；网络路由由 sing-box JSON 中的 TUN 选项控制。关闭应用会停止它自己的后端；异常退出后后端也会检测父进程退出并关闭。

## 构建

原界面源码锁定在 `clients/apple` 子模块的自维护分支，核心和本机后端位于本仓库。可手动运行 GitHub Actions 的 **macOS upstream GUI (local self-signing)**。默认只上传构建产物，启用 publish 才会向当前 Release 添加自签名包；已有资产不覆盖。

本机构建需要完整 Xcode、Go 和已校验的 FEC Apple SDK：

```sh
git submodule update --init --recursive clients/apple
python3 release/build_macos_local.py --sdk /absolute/path/Libbox.xcframework --output /absolute/path/macos-build
```

如果 `xcode-select` 指向 Command Line Tools，给这条命令设置 `DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer`，无需改动全局 Xcode 选择。构建脚本只生成应用和 DMG，不启动隧道或修改本机路由。

本机签名不提供 Apple 公证；原版 SFM 的 Network Extension 仍不能仅靠这种签名运行。本机 TUN 后端正是为避免依赖该扩展而适配。
