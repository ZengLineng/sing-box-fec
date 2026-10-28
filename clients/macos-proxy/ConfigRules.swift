import Foundation

struct ProxyConfiguration {
    let addresses: [String]

    static func parse(_ data: Data) throws -> ProxyConfiguration {
        guard data.count <= 2 * 1024 * 1024,
              let config = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let inbounds = config["inbounds"] as? [[String: Any]], !inbounds.isEmpty else {
            throw ConfigError("请选择不超过 2MiB 的本地代理 JSON 配置。")
        }
        if let services = config["services"] as? [Any], !services.isEmpty {
            throw ConfigError("本地代理版不运行系统服务。")
        }
        for endpoint in config["endpoints"] as? [[String: Any]] ?? [] {
            if endpoint["system"] as? Bool == true {
                throw ConfigError("本地代理版要求 Endpoint 的 system 为 false。")
            }
        }
        var addresses: [String] = []
        for inbound in inbounds {
            guard let kind = inbound["type"] as? String, ["mixed", "http", "socks"].contains(kind),
                  let host = inbound["listen"] as? String, ["127.0.0.1", "::1", "localhost"].contains(host),
                  let port = inbound["listen_port"] as? Int, (1...65535).contains(port) else {
                throw ConfigError("请导入仅监听 loopback 地址的 mixed、HTTP 或 SOCKS 配置；此版本不创建 TUN。")
            }
            guard inbound["set_system_proxy"] as? Bool != true else {
                throw ConfigError("请关闭配置中的 set_system_proxy，本地代理版保留系统网络设置。")
            }
            let address = host.contains(":") ? "[\(host)]:\(port)" : "\(host):\(port)"
            if kind != "http" { addresses.append("socks5h://\(address)") }
            if kind != "socks" { addresses.append("http://\(address)") }
        }
        return ProxyConfiguration(addresses: addresses)
    }
}

struct ConfigError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
