import AppKit
import Foundation
import SwiftUI
import UniformTypeIdentifiers

@MainActor
final class ProxyModel: ObservableObject {
    static let shared = ProxyModel()
    @Published var profileName = "未导入配置"
    @Published var addresses: [String] = []
    @Published var running = false
    @Published var busy = false
    @Published var status = "等待配置"
    @Published var log = ""
    @Published var error: String?

    private var process: Process?
    private var logPipe: Pipe?
    private let directory: URL
    private var configURL: URL { directory.appendingPathComponent("configuration.json") }
    private var coreURL: URL? { Bundle.main.url(forAuxiliaryExecutable: "fecbox-core") }

    private init() {
        let smoke = ProcessInfo.processInfo.arguments.contains("--smoke-test")
        directory = smoke
            ? FileManager.default.temporaryDirectory.appendingPathComponent("FECBoxSmoke-\(UUID().uuidString)")
            : FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("FECBoxProxy")
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            if !smoke, FileManager.default.fileExists(atPath: configURL.path) {
                addresses = try ProxyConfiguration.parse(Data(contentsOf: configURL)).addresses
                profileName = UserDefaults.standard.string(forKey: "profileName") ?? "已保存的配置"
                status = "已停止"
            }
        } catch { self.error = error.localizedDescription }
    }

    func importProfile(_ url: URL) {
        guard !running, !busy, let core = coreURL else { return }
        busy = true
        let access = url.startAccessingSecurityScopedResource()
        defer { if access { url.stopAccessingSecurityScopedResource() } }
        do {
            let data = try Data(contentsOf: url)
            let info = try ProxyConfiguration.parse(data)
            let candidate = directory.appendingPathComponent("candidate-\(UUID().uuidString).json")
            FileManager.default.createFile(atPath: candidate.path, contents: data, attributes: [.posixPermissions: 0o600])
            Task {
                do {
                    try await Task.detached { try Self.check(core: core, config: candidate) }.value
                    try data.write(to: configURL, options: .atomic)
                    try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: configURL.path)
                    profileName = url.lastPathComponent
                    UserDefaults.standard.set(profileName, forKey: "profileName")
                    addresses = info.addresses
                    status = "已停止"
                    log = "配置校验通过。点击启动以开放本地代理。\n"
                } catch { self.error = error.localizedDescription }
                try? FileManager.default.removeItem(at: candidate)
                busy = false
            }
        } catch {
            self.error = error.localizedDescription
            busy = false
        }
    }

    nonisolated private static func check(core: URL, config: URL) throws {
        let output = config.deletingPathExtension().appendingPathExtension("log")
        FileManager.default.createFile(atPath: output.path, contents: nil, attributes: [.posixPermissions: 0o600])
        defer { try? FileManager.default.removeItem(at: output) }
        let file = try FileHandle(forWritingTo: output)
        defer { try? file.close() }
        let task = Process()
        task.executableURL = core
        task.arguments = ["check", "-c", config.path]
        task.standardOutput = file
        task.standardError = file
        task.standardInput = FileHandle.nullDevice
        try task.run()
        let deadline = Date().addingTimeInterval(20)
        while task.isRunning, Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
        if task.isRunning {
            task.terminate()
            throw ConfigError("配置检查超时。")
        }
        guard task.terminationStatus == 0 else {
            let message = String(data: try Data(contentsOf: output), encoding: .utf8) ?? "配置检查失败"
            throw ConfigError(String(message.suffix(4000)))
        }
    }

    func start() {
        guard process == nil, !addresses.isEmpty, let core = coreURL else { return }
        let task = Process()
        let pipe = Pipe()
        task.executableURL = core
        task.arguments = ["run", "--disable-color", "-c", configURL.path]
        task.currentDirectoryURL = directory
        task.standardOutput = pipe
        task.standardError = pipe
        task.standardInput = FileHandle.nullDevice
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            Task { @MainActor in self?.appendLog(text) }
        }
        task.terminationHandler = { [weak self] stopped in
            let code = stopped.terminationStatus
            Task { @MainActor in
                guard let self, self.process === stopped else { return }
                self.logPipe?.fileHandleForReading.readabilityHandler = nil
                self.logPipe = nil
                self.process = nil
                self.running = false
                self.status = code == 0 ? "已停止" : "启动失败 / 已退出"
                self.appendLog("\n核心退出，状态码 \(code)。\n")
            }
        }
        do {
            try task.run()
            process = task
            logPipe = pipe
            running = true
            status = "运行中"
            appendLog("\n本地代理已启动。系统网络设置保持原状。\n")
        } catch {
            pipe.fileHandleForReading.readabilityHandler = nil
            self.error = error.localizedDescription
        }
    }

    func stop() {
        guard let task = process, task.isRunning else { return }
        status = "正在停止"
        task.terminate()
    }

    private func appendLog(_ text: String) { log = String((log + text).suffix(24000)) }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationWillTerminate(_ notification: Notification) { ProxyModel.shared.stop() }
}

struct ContentView: View {
    @ObservedObject var model = ProxyModel.shared
    @State private var importer = false
    private let green = Color(red: 0.14, green: 0.42, blue: 0.34)

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            HStack(spacing: 14) {
                Text("F").font(.system(size: 30, weight: .bold)).foregroundStyle(.white)
                    .frame(width: 56, height: 56).background(green, in: RoundedRectangle(cornerRadius: 15))
                VStack(alignment: .leading, spacing: 3) {
                    Text("FECBox").font(.largeTitle.bold())
                    Text("macOS 本地代理 · \(Bundle.main.object(forInfoDictionaryKey: "FECBoxVersion") as? String ?? "")")
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Label(model.status, systemImage: model.running ? "circle.fill" : "circle")
                    .foregroundStyle(model.running ? green : .secondary)
            }
            GroupBox {
                HStack {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("配置").foregroundStyle(.secondary)
                        Text(model.profileName).fontWeight(.medium)
                    }
                    Spacer()
                    Button("导入配置…") { importer = true }.disabled(model.running || model.busy)
                    Button(model.running ? "停止代理" : "启动本地代理") {
                        model.running ? model.stop() : model.start()
                    }
                    .buttonStyle(.borderedProminent).tint(green)
                    .disabled(model.busy || model.addresses.isEmpty)
                }.padding(10)
            }
            GroupBox("代理地址") {
                VStack(alignment: .leading, spacing: 8) {
                    if model.addresses.isEmpty {
                        Text("导入 Mac 本地代理配置后显示地址。请在需要代理的应用中使用该地址。")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(model.addresses, id: \.self) { address in
                        HStack {
                            Text(address).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                            Spacer()
                            Button("复制") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(address, forType: .string)
                            }
                        }
                    }
                }.frame(maxWidth: .infinity, alignment: .leading).padding(10)
            }
            Text("启动仅开放本地代理端口。VPN、默认路由和系统代理由你管理。")
                .font(.callout).foregroundStyle(.secondary)
            GroupBox("运行日志") {
                ScrollView {
                    Text(model.log.isEmpty ? "等待启动…" : model.log)
                        .font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading).padding(10)
                }.frame(minHeight: 170)
            }
        }
        .padding(24).frame(minWidth: 650, minHeight: 560)
        .fileImporter(isPresented: $importer, allowedContentTypes: [.json, .data]) { result in
            switch result {
            case let .success(url): model.importProfile(url)
            case let .failure(error): model.error = error.localizedDescription
            }
        }
        .alert("操作未完成", isPresented: Binding(get: { model.error != nil }, set: { if !$0 { model.error = nil } })) {
            Button("好") { model.error = nil }
        } message: { Text(model.error ?? "") }
        .onAppear {
            if ProcessInfo.processInfo.arguments.contains("--smoke-test") {
                print("FECBox Proxy UI ready; engine not started")
                DispatchQueue.main.asyncAfter(deadline: .now() + 2) { NSApp.terminate(nil) }
            }
        }
    }
}

@main
struct FECBoxApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate
    var body: some Scene {
        WindowGroup("FECBox Proxy") { ContentView() }
    }
}
