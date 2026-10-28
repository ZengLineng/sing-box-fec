import Foundation

@main
struct ConfigRulesTests {
    static func main() throws {
        let valid = """
        {"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":17893}],"endpoints":[{"type":"wireguard-fec","system":false}]}
        """
        let result = try ProxyConfiguration.parse(Data(valid.utf8))
        precondition(result.addresses == ["socks5h://127.0.0.1:17893", "http://127.0.0.1:17893"])
        let invalid = [
            valid.replacingOccurrences(of: "mixed", with: "tun"),
            valid.replacingOccurrences(of: "127.0.0.1", with: "0.0.0.0"),
            valid.replacingOccurrences(of: "17893", with: "0"),
            valid.replacingOccurrences(of: "\"system\":false", with: "\"system\":true"),
            valid.replacingOccurrences(of: "\"type\":\"mixed\"", with: "\"type\":\"mixed\",\"set_system_proxy\":true"),
            valid.dropLast() + ",\"services\":[{\"type\":\"api\"}]}",
            "{}",
            "{\"inbounds\":[]}",
        ]
        for config in invalid {
            do {
                _ = try ProxyConfiguration.parse(Data(config.utf8))
                fatalError("Unsafe/non-proxy configuration accepted")
            } catch is ConfigError {} catch { throw error }
        }
        print("Config rules passed: loopback-only, no TUN, no system proxy changes")
    }
}
