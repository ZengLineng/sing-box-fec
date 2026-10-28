# 首次验证记录

日期：2026-10-28。实现 commit：`c6ca1d8dc7f6c17014ed02a977d0c41e79e35b0d`，版本 `1.14.3-fec.1`。测试客户端为 macOS arm64，服务端为 Ubuntu 24.04 amd64，两端本地编译使用 Go 1.26.3。后续 CI 的链接参数修正不改变 FEC 代码。

## 独立公网链路

客户端使用全新 WireGuard 身份、FEC 密钥和网段 `10.79.0.0/24`，绑定物理接口 `en0`，仅在 `127.0.0.1:17892` 监听测试代理。服务端暂用已开放的测试 UDP 51820，使用系统接口和隧道内 SOCKS。抓包确认报文从客户端公网地址到达服务器物理接口，未复用现用 VPN 通道。

参数：10 个数据分片、2 个冗余分片、20ms 定时、FEC MTU 1400、WireGuard MTU 1280。客户端隧道内仍使用上游 gVisor；服务端 `ss -tin` 同时观测到 `10.79.0.1:18080` 和测试源 TCP socket 的内核 BBR 状态。

测试源仅监听服务端 loopback，通过临时 SOCKS 规则允许访问该单一端口；没有暴露额外公网 TCP 测试端口。每次读取完整 8,388,608 字节，内容为 `bytes(range(256)) * 32768`。

| 次数 | 总用时 | 吞吐 |
| --- | --- | --- |
| 1 | 3.390743 s | 19.79 Mbps |
| 2 | 3.192525 s | 21.02 Mbps |
| 3 | 3.364632 s | 19.95 Mbps |

三次接收文件均验证 SHA-256 为 `7d212b9c884f5c77896de960ae17cc341cda43b14d6a971f34ca29ebd4badf7f`。总时间包含代理建连开销。这是短时单线路结果，未做同时间窗口的公网 raw/FEC 对照，不能据此量化 FEC 增益或判定运营商限流。

- Google `generate_204`：HTTP 204，1.241 s。
- GitHub 首页：HTTP 200，584,832 字节，2.203 s。
- SOCKS5 UDP → 公网 DNS `1.1.1.1:53` → `google.com A`：事务 ID 与响应验证通过，6 条 answer，1.012 s。

临时客户端、服务端、HTTP 测试源均停止，旧独立 FEC 测试服务恢复。主 VPN PID `1072925` 和启动时间 `2026-10-27 18:00:58 CST` 未变，原始独立 WireGuard 和系统出口服务也未重启。测试身份已弃用，不作为日常配置。

## 自动化范围

FEC 并发逻辑单独执行 race 检查；完整 gVisor 代理链路不启用 race 插桩，避免其性能开销引入额外 UDP 队列丢包和测试超时。真实实例验证普通 WireGuard 与 WireGuard FEC，通过受控 relay 丢弃每组第 1、8 个数据分片，完成约 1.06MiB TCP 字节校验和 UDP echo。畸形帧、认证、乱序、定时器、缓存上限和关闭/重开另有单元及 fuzz 测试。

Linux / Windows / macOS 的原生测试和六种 amd64/arm64 CLI 构建在首轮 CI 通过。移动 SDK 构建结果以 [Actions](https://github.com/ZengLineng/sing-box-fec/actions/workflows/fec.yml) 对应 commit 为准。未完成手机 App 的签名、安装、Network Extension 权限、漫游、休眠恢复和耗电验收。
