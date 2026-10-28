# Linux FEC 服务部署与升级

当前服务端模板适用于 Linux x86_64。客户端可使用 Android 或其他已构建平台；Linux ARM64 核心也会发布，但此模板的 BPF 启动守卫须先适配 ARM64 ABI。

原版服务为 `sing-box.service`，新服务为 `fecbox.service`。新版本只更新 `/opt/fecbox/`、`/usr/local/bin/fecbox` 和 `fecbox.service`；服务器私有配置保留在 `/etc/fecbox/server.json`，通过 systemd credential 读取。

## 首次安装

1. 从 GitHub Releases 下载对应 Linux 包及 `SHA256SUMS`，校验后解压。当前服务器使用 `linux-amd64`。
2. 将 `fecbox` 安装到 `/opt/fecbox/版本/fecbox`，以 `/usr/local/bin/fecbox` 符号链接指向它。
3. 根据 `examples/wireguard-fec/server-native.json` 生成新的服务器和客户端身份、FEC 密钥、SOCKS 密码；服务器配置设为 root:root、0600。
4. 安装 `require-cgroup-ip-filter.py` 到 `/usr/local/libexec/fecbox/`，安装 `fecbox.service` 到 `/etc/systemd/system/`。按实际客户端地址同步 unit 的 `IPAddressAllow`。
5. 放行新通道的 UDP 端口，检查 `/dev/net/tun` 与网段、接口名、端口无冲突。`systemctl daemon-reload` 后启动、验收并启用 `fecbox.service`。

模板在非特权 DynamicUser 下运行，授予创建系统 TUN 所需的 CAP_NET_ADMIN。双向 cgroup IP 过滤器缺失时启动失败。使用公开 DoH 和 IPv4 解析，私网目的地址由逐包 IP 策略拦截；隧道客户端地址是回包所需的显式例外。

## 后续升级

从 Release 选择目标版本，下载并校验对应 Linux 包，安装到新的版本目录。先运行 `fecbox check -c /etc/fecbox/server.json`，再切换符号链接并仅重启 `fecbox.service`。保留上个版本目录，回滚时将链接切回并重启同一服务。

更新 credential 文件后需要重启新服务，SIGHUP 不会刷新 systemd credential 的快照。新增设备应分配独立 WireGuard 身份和隧道地址，并同步 peer 与 `IPAddressAllow`，避免多台设备复用身份导致端点抢占。

## 验收

查看服务启动日志中的有效 ingress/egress IP filter 检查结果，以及 FEC Endpoint 和隧道内 SOCKS 监听状态。通过独立客户端测试 HTTPS、公网 UDP DNS 和私网 UDP canary。确认原服务 PID、启动时间及监听端口保持一致。

发布支持两条路径：**FECBox Release** 完成构建、APK 签名和发布；**Publish verified builds** 复用已成功且源码 commit 相同的核心与 APK 构建。复用历史构建前，维护者先为已验收源码 commit 创建版本标签；CI 核验标签与源码一致后上传并发布，不覆盖已经发布的版本。
