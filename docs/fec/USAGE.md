# 配置与客户端准备

Android 用户从 [Releases](https://github.com/ZengLineng/sing-box-fec/releases) 下载匹配架构的 FECBox APK，或下载 universal APK。安装后导入私有配置；[client-android.json](../../examples/wireguard-fec/client-android.json) 是完整 TUN 模板，需填入独立身份和服务器信息。应用标识 `dev.zenglineng.fecbox`，可与官方客户端并存。安装不会自动连接 VPN。

CI/CD 会签名并发布 Android App；iOS App 暂不提供，Apple XCFramework 仍作为 SDK。

从 [client.json](../../examples/wireguard-fec/client.json) 和 [server-native.json](../../examples/wireguard-fec/server-native.json) 开始，它们是需填入密钥的模板。

1. 两端分别运行 `fecbox generate wg-keypair`，填入自己的私钥和对端公钥。独立通道使用新身份。
2. 用 `openssl rand -base64 32` 生成独立的 FEC 共享密钥，填入两端 `fec.key`，不要复用 WireGuard 私钥。
3. 填入随机 SOCKS 密码，将客户端文档地址 `203.0.113.10` 换为服务器公网 IP。云和主机防火墙仅需开放通道的 **UDP 51820**；不要向公网开放 TCP 18080。
4. 两端运行 `fecbox check -c config.json`。服务端需要 Linux `/dev/net/tun`、`CAP_NET_ADMIN` 或 root，以及空闲接口名 `fecwg0`、网段 `10.79.0.0/24` 和 UDP 端口。
5. 先启动服务端，再启动客户端：`fecbox run -c config.json`。SOCKS 服务只监听隧道地址 `10.79.0.1:18080`。

```sh
# 仅此请求进入新通道，不设置系统代理或切换 VPN。
curl --proxy socks5h://127.0.0.1:17890 https://www.google.com/generate_204
```

模板不创建客户端 TUN，也不修改默认路由。与已有 VPN 并行时，应显式设置 Endpoint 的物理出口，例如 macOS 的 `"bind_interface": "en0"`，接口名须先确认。自建 Android / Apple 客户端复用上游平台 socket protection，避免把新 UDP 通道套进已有 VPN。不要复用现有 VPN 的私钥、地址或端口。

服务端示例的私网拒绝规则是连接级限制，不能充当严格的逐包 UDP 出站防火墙。多用户或非受信客户端的部署，应另外用主机/cgroup 防火墙限制私网目的地址；不要移除原诊断环境已有的隔离策略。

## 新 Endpoint

`wireguard-fec` 继承[上游 WireGuard Endpoint](https://sing-box.sagernet.org/configuration/endpoint/wireguard/) 的字段。与普通配置相比，修改 `type`，设置适合额外封装的 MTU，增加 `fec`：

```json
{
  "type": "wireguard-fec",
  "tag": "fec-link",
  "mtu": 1280,
  "fec": {
    "key": "BASE64_ENCODED_32_BYTE_SECRET",
    "data_shards": 10,
    "parity_shards": 2,
    "interval": "20ms",
    "mtu": 1400
  }
}
```

此片段省略了必需的 WireGuard 地址、密钥和 peer，完整配置见模板。

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `fec.key` | 必填 | 两端相同的独立 32 字节 Base64 密钥 |
| `fec.data_shards` | 10 | 每组最多 1–32 个数据包 |
| `fec.parity_shards` | 2 | 满组冗余数，1–16 |
| `fec.interval` | 20ms | 非满组补包间隔，1ms–1s；原始包立即发送 |
| `fec.mtu` | 1400 | UDP payload 上限，576–1500；不含 IP/UDP 头 |
| Endpoint `mtu` | 1280 | 隧道内 IP 包上限，启动时校验封装开销 |

两端参数须匹配。满组 10+2 可恢复同组任意 2 个丢失分片。非满组的冗余数为 `ceil(N*2/10)`；单包组发送一个冗余包，所以低流量下冗余比例更高。超过恢复能力后，内层 TCP 仍可重传，普通 UDP 可能丢失。

每个数据包有 50 字节 FEC 开销，另有冗余包和填充。默认 MTU 适合普通 1500 字节路径；路径更小时需同时降低内层 MTU 和 `fec.mtu`。FEC 无法解决带宽不足、持续拥塞或超过冗余能力的突发丢包。

## 各平台

| 平台 | 产物 | 接入 |
| --- | --- | --- |
| Windows / Linux / macOS | amd64、arm64 `fecbox` | 先显式使用本地代理；全局 TUN 另行配置验收 |
| Android | 已签名 FECBox APK，API 24+，四种架构及 universal；另有 AAR | 安装 APK 后导入 Android 配置 |
| iOS / macOS | `Libbox.xcframework`，iOS 15+ / macOS 13+ | 用此核心重建客户端与 Network Extension |

官方 App 仅导入 JSON 不能支持新协议。移动端仍需客户端工程、VPN 权限、签名和安装渠道；Apple Network Extension entitlement 与设备测试需要开发者账号和设备。SDK 编译成功不代表漫游、休眠恢复和耗电已经实测。

FEC 包装保留原 `conn.Bind` 的 Android fd protection 和 Windows interface binding，复用现有出口选择与暂停/恢复，有接口保留测试。

## 构建和测试

```sh
make fec_build
make fec_test
go test ./transport/fec -run '^$' -fuzz FuzzAuthenticatedFrameParser -fuzztime 15s
```

CI 固定 Go 1.26.8；最低版本见 `go.mod`。默认精简构建包含 gVisor、WireGuard/FEC、QUIC、uTLS、Clash API。其余上游功能保留在源码中，可覆盖 `FEC_TAGS` 构建。移动绑定生成器固定为上游 gomobile v0.1.13，命令见 [fec.yml](../../.github/workflows/fec.yml)。不需要 Python/UDPspeeder 运行进程。

集成测试通过回环 UDP relay 丢弃每组第 1、8 个数据分片，以真实 sing-box 实例完成 WireGuard 握手、TCP 下载字节校验及 UDP echo，并覆盖普通 WireGuard。它不访问公网、不修改 VPN 或系统路由。Linux 系统出口、真实 WAN 和手机设备验收须单独记录，旧 UDPspeeder 的测速不能当成本实现的测速。
