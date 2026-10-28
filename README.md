# FECBox

基于 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 的独立维护分支，增加与 `wireguard` 并列的 `wireguard-fec` Endpoint，把 WireGuard 加密和 Reed–Solomon UDP 丢包恢复编译进同一个核心。

这不是 SagerNet 官方发行版。上游基线 **v1.14.3**，分支版本见 [release/FEC_VERSION](release/FEC_VERSION)。保留上游 Go 模块路径及 libbox API，发行程序名为 `fecbox`。

```text
应用 → 本地 mixed/TUN → SOCKS outbound（detour: fec-link）
     → wireguard-fec → UDP 网络 → wireguard-fec
     → Linux 隧道地址上的 SOCKS 入口 → 系统 TCP/UDP socket → 网站
```

服务端的两段 TCP 连接使用 Linux 内核网络栈，FEC 修复外层 UDP 丢包；二者可以叠加。客户端 `system: false` 的隧道连接仍使用上游 gVisor。FEC 不替代拥塞控制，也不保证任意线路必然提速。

- [配置与客户端准备](docs/fec/USAGE.md)
- [线格式和实现边界](docs/fec/PROTOCOL.md)
- [上游同步与验证范围](UPSTREAM.md)
- [首次独立公网验证记录](docs/fec/VALIDATION.md)
- [客户端模板](examples/wireguard-fec/client.json)、[Linux 系统出口模板](examples/wireguard-fec/server-native.json)

```sh
make fec_build
./dist/fecbox version
make fec_test
```

[GitHub Actions](https://github.com/ZengLineng/sing-box-fec/actions/workflows/fec.yml) 生成 Linux / Windows / macOS 的 amd64、arm64 CLI，以及 Android `libbox.aar` 和 Apple `Libbox.xcframework`。[Release 流水线](.github/workflows/release-fec.yml) 同时构建并签名独立 Android APK，发布到 [GitHub Releases](https://github.com/ZengLineng/sing-box-fec/releases)。Android 源码维护在 [fecbox-android](https://github.com/ZengLineng/fecbox-android)，由本仓库子模块锁定。iOS App 暂不提供。

两端均须使用此分支；普通 WireGuard、官方 App、UDPspeeder 不识别新线格式。原有 `wireguard` 类型保持上游协议。

## License

Upstream copyright: Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>.

继承上游 [LICENSE](LICENSE) 中的 GPL-3.0-or-later 和附加名称条款。上游版权属于原作者，本分支不暗示官方关联。Reed–Solomon 来自 MIT 授权的 [klauspost/reedsolomon](https://github.com/klauspost/reedsolomon)。分发二进制时应提供对应源码、构建方式及版权说明。
