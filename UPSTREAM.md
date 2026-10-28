# 维护说明

上游 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 基线为 `v1.14.3`，commit `7054cac5124465ffdb145870d7c01885566711b1`。自维护主线 `main`，版本唯一来源为 `release/FEC_VERSION`。保留 Go module 路径以减少上游合并和客户端 API 的改动；必须克隆本仓库构建，`go install` 上游模块不会得到此功能。

| 位置 | 职责 |
| --- | --- |
| `constant/proxy.go`, `option/wireguard.go` | 新类型和配置，Schema 沿用注册表生成 |
| `protocol/wireguard`, `include/wireguard_stub.go` | 注册 Endpoint，无构建标签时给出明确错误 |
| `transport/wireguard/endpoint*.go` | 复用 WireGuard 生命周期，包装 Bind，校验 MTU |
| `transport/fec` | 唯一的认证、编解码、缓存、定时补包实现 |
| `internal/fecintegration` | 配置加载、真实握手、丢包下 TCP/UDP |
| `cmd/sing-box/cmd*.go` | CLI 展示名称改为 FECBox |
| `release/FEC_*`, `.github/workflows/fec.yml` | 版本及跨平台 CLI/SDK 构建 |

常规 `wireguard` 不启用 FEC。WireGuard 配置错误不再输出含私钥的 IPC 原文。上游发布工作流原样保存在 `.github/upstream-workflows/`，不执行其发布、签名、镜像或 stale 定时任务。Android 子模块指向 `ZengLineng/fecbox-android`，保留上游 namespace 与 libbox API，以独立 applicationId 和持久签名构建 APK。Apple 子模块指向 `ZengLineng/fecbox-apple`，`FECBOX_LOCAL` 编译条件下保留原 SFM 界面并替换本机服务生命周期；desktop 子模块保持上游基线，不发布 iOS App。

## 更新流程

```sh
git remote add upstream https://github.com/SagerNet/sing-box.git # 已有则跳过
git fetch upstream --tags
git switch -c update/upstream-VERSION main
git merge --no-ff vVERSION
```

在更新分支检查 Bind/ReceiveFunc、batch offset、Endpoint 生命周期、libbox API 和平台 socket 保护。上游重新带入的发布工作流移入参考目录。更新基线、分支版本，运行 `make fec_test` 和跨平台 CI，通过后合并。不要自动定时合并上游。

CI 在 Linux / Windows / macOS 上执行认证、畸形帧、恢复、内存边界、定时器、关闭/重开、竞态、接口保留和完整代理链路测试；CLI 交叉编译 amd64/arm64，移动 SDK 构建 Android AAR 和 Apple XCFramework。

成功 SDK 只证明编译与绑定通过。手机发布仍需重建 App、签名、VPN 权限、设备联网、切网、休眠恢复、MTU、耗电验收。首次实测保持独立端口/身份/网段，保留原 VPN，记录相同时段、目标、字节数、丢包率、CPU 的对照；旧 UDPspeeder 结果不属于新实现。

分发使用 `fecbox` 名称，保留 GPL 和第三方授权，提供与二进制 commit 对应的源码。Actions 仅上传产物和 SHA-256，不使用官方发布凭据与签名身份。

## Release 和 Android 签名

在 GitHub Actions 手动运行 **FECBox Release**，或推送与 `release/FEC_VERSION` 一致的 `v*-fec.*` 标签。流水线复用核心构建，在 Android AAR 完成后立即构建 APK，待全部检查成功后发布 Release。已经发布的版本不可覆盖；更新时修改版本号。

签名密钥以仓库 Secrets `FECBOX_ANDROID_KEYSTORE_B64` 和 `FECBOX_ANDROID_KEYSTORE_PASSWORD` 提供。私钥只在签名任务中落盘，任务结束删除；保持同一密钥才能原地升级 Android App。Android APK 的 applicationId 是 `dev.zenglineng.fecbox`，使用自己的更新源和签名，可与官方客户端并存。

Linux x86_64 的长期服务模板见 `deploy/fecbox.service`，读取 `/etc/fecbox/server.json` credential。启动前检查实际挂载的双向 cgroup IP 过滤器；模板保留隧道客户端地址作为回包例外，保护私网目的地址。其他服务器架构须先适配 BPF 查询 ABI。原版 VPN 和 FEC 版采用独立服务、端口、身份和网段。

## macOS 自签名 DMG

正式构建复用 `clients/apple` 的原 SFM 界面，入口为 `macos-local.yml` 和 `release/build_macos_local.py`。`cmd/fecbox-local` 通过原 libbox gRPC 协议提供配置重载、日志、分组和连接管理；未注册 Apple 平台接口，因此 TUN 使用核心已有的 macOS utun 实现。界面通过私有 Unix socket 控制自己启动的进程，TUN 配置在用户点击启动时请求本机管理员权限。进程随界面退出或失去运行配置而关闭，不安装持久系统服务。

CI 校验对应 FEC SDK、测试私有配置和本地生命周期，在隔离的 GitHub macOS runner 上测试真实 utun 的启动/重载/关闭，然后构建 Apple Silicon/Intel Universal App、ad-hoc 签名并验证 DMG。此构建没有 Network Extension、App Group 或 iCloud entitlement，不使用 Apple 开发者身份，也未做公证。升级上游时检查 `LocalExtensionProfile` 的生命周期 API、gRPC API 版本和 SFM target 的扩展依赖。早期的 `macos-proxy.yml` 小型代理界面保留为手动构建工具，不再接入默认 Release。
