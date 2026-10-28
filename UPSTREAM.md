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

常规 `wireguard` 不启用 FEC。WireGuard 配置错误不再输出含私钥的 IPC 原文。上游发布工作流原样保存在 `.github/upstream-workflows/`，不执行其发布、签名、镜像或 stale 定时任务。客户端子模块仍指向基线锁定的上游工程，当前不声称维护并签名了三套 GUI。

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
