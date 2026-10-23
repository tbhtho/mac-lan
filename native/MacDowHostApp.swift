import AppKit
import ApplicationServices
import CoreGraphics
import Foundation
import Darwin

// Pure state model used by the app and its non-UI --self-test.
struct HostLifecycle {
    enum Action { case ready, closeWindow, reopen, stop, quit }
    var visible = true
    var hosting = false
    var quitting = false
    mutating func receive(_ action: Action) {
        switch action {
        case .ready: hosting = true
        case .closeWindow: visible = false
        case .reopen: visible = true
        case .stop: hosting = false
        case .quit: hosting = false; quitting = true
        }
    }
}
func lifecycleTests() {
    var state = HostLifecycle()
    precondition(!state.hosting)
    state.receive(.ready); state.receive(.closeWindow)
    precondition(state.hosting && !state.visible && !state.quitting)
    state.receive(.reopen); precondition(state.visible && state.hosting)
    state.receive(.stop); precondition(!state.hosting)
    state.receive(.ready); state.receive(.quit)
    precondition(!state.hosting && state.quitting)
    print("Host app lifecycle self-tests passed; no NSApplication, permissions, capture, listener or UI started.")
}
if CommandLine.arguments.contains("--self-test") { lifecycleTests(); exit(0) }

final class HostApp: NSObject, NSApplicationDelegate, NSWindowDelegate, NetServiceDelegate {
    var state = HostLifecycle()
    var window: NSWindow!
    var menuBar: NSStatusItem!
    var menuStatus = NSMenuItem(title: "Not sharing", action: nil, keyEquivalent: "")
    var menuStop: NSMenuItem!
    let status = NSTextField(wrappingLabelWithString: "Ready to set up. Nothing is being shared.")
    let permissions = NSTextField(wrappingLabelWithString: "")
    let identity = NSTextField(wrappingLabelWithString: "")
    let startButton = NSButton(title: "Start sharing", target: nil, action: nil)
    var process: Process?
    var input: FileHandle?
    var outputPipe: Pipe?
    var errorPipe: Pipe?
    var buffer = Data()
    var publisher: NetService?
    var consent: NSAlert?
    var consentTimer: Timer?
    var stopTimer: Timer?
    var interfaces: [[String:String]] = []
    var selectedAddress: String?
    var manualDetails = ""
    var fingerprint = ""
    var updater: HostUpdater!
    var updateMenu: NSMenuItem!
    var automaticUpdates: NSMenuItem!

    var hostURL: URL {
        Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/MacDowHost")
    }
    func applicationDidFinishLaunching(_ notification: Notification) {
        makeMenu()
        window = NSWindow(contentRect: NSRect(x:0,y:0,width:540,height:360),
            styleMask:[.titled,.closable,.miniaturizable], backing:.buffered, defer:false)
        window.title = "MAC-DOW Host"
        window.delegate = self
        window.isReleasedWhenClosed = false
        let stack = NSStackView()
        stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 14
        stack.translatesAutoresizingMaskIntoConstraints = false
        window.contentView!.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo:window.contentView!.leadingAnchor,constant:24),
            stack.trailingAnchor.constraint(equalTo:window.contentView!.trailingAnchor,constant:-24),
            stack.topAnchor.constraint(equalTo:window.contentView!.topAnchor,constant:24),
            stack.bottomAnchor.constraint(lessThanOrEqualTo:window.contentView!.bottomAnchor,constant:-20)
        ])
        let title = NSTextField(labelWithString:"Your Mac, on Windows")
        title.font = .systemFont(ofSize:23,weight:.semibold); stack.addArrangedSubview(title)
        let hint = NSTextField(wrappingLabelWithString:"Open MAC-DOW on Windows and choose Find my Mac. Closing this window keeps the menu-bar controls available.")
        hint.textColor = .secondaryLabelColor; stack.addArrangedSubview(hint)
        stack.addArrangedSubview(permissions)
        let buttons = NSStackView(); buttons.orientation = .horizontal; buttons.spacing = 8
        buttons.addArrangedSubview(NSButton(title:"Screen Recording…",target:self,action:#selector(screenPermission)))
        buttons.addArrangedSubview(NSButton(title:"Control Access…",target:self,action:#selector(controlPermission)))
        buttons.addArrangedSubview(NSButton(title:"Refresh",target:self,action:#selector(refreshSetup)))
        stack.addArrangedSubview(buttons)
        startButton.target = self; startButton.action = #selector(toggleSharing)
        startButton.bezelStyle = .rounded; stack.addArrangedSubview(startButton)
        status.textColor = .secondaryLabelColor; stack.addArrangedSubview(status)
        identity.font = .monospacedSystemFont(ofSize:12,weight:.medium)
        identity.isSelectable = true; stack.addArrangedSubview(identity)
        refreshPermissions(); loadInterfaces(); reopen(); updater.start()
    }
    func makeMenu() {
        updater = HostUpdater(app:self)
        menuBar = NSStatusBar.system.statusItem(withLength:NSStatusItem.variableLength)
        menuBar.button?.title = "MAC-DOW"
        menuBar.button?.setAccessibilityLabel("MAC-DOW: not sharing")
        let menu = NSMenu(); menu.addItem(menuStatus); menu.addItem(.separator())
        menu.addItem(withTitle:"Reopen",action:#selector(reopen),keyEquivalent:"")
        menuStop = menu.addItem(withTitle:"Stop Sharing / Disconnect",action:#selector(stopSharing),keyEquivalent:"")
        menu.addItem(withTitle:"Network…",action:#selector(chooseNetwork),keyEquivalent:"")
        menu.addItem(withTitle:"Advanced connection details…",action:#selector(showDetails),keyEquivalent:"")
        updateMenu = menu.addItem(withTitle:"Check for Updates…",action:#selector(checkUpdates),keyEquivalent:"")
        automaticUpdates = menu.addItem(withTitle:"Download Updates Automatically",action:#selector(toggleAutomaticUpdates),keyEquivalent:"")
        automaticUpdates.state = updater.automatic ? .on : .off
        menu.addItem(.separator()); menu.addItem(withTitle:"Quit MAC-DOW",action:#selector(quit),keyEquivalent:"q")
        for item in menu.items { item.target = self }
        menu.autoenablesItems = false; menuStop.isEnabled = false; menuBar.menu = menu
    }
    @objc func reopen() {
        state.receive(.reopen)
        window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps:true)
        refreshPermissions()
    }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        state.receive(.closeWindow); sender.orderOut(nil); return false
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender:NSApplication)->Bool { false }
    func applicationShouldHandleReopen(_ sender:NSApplication,hasVisibleWindows:Bool)->Bool {reopen();return false}
    func applicationShouldTerminate(_ sender:NSApplication)->NSApplication.TerminateReply {
        state.receive(.quit)
        if process != nil { stopSharing(); return .terminateLater }
        return .terminateNow
    }
    @objc func quit() { NSApp.terminate(nil) }
    @objc func refreshPermissions() {
        let screen = CGPreflightScreenCaptureAccess()
        let control = AXIsProcessTrusted()
        permissions.stringValue = "Viewing: \(screen ? "allowed" : "needs Screen Recording")    Control: \(control ? "allowed" : "optional Accessibility")"
        if process == nil { startButton.isEnabled = screen }
    }
    @objc func refreshSetup() {refreshPermissions();loadInterfaces()}
    @objc func screenPermission() {
        // Only an explicit user click reaches a system request/settings UI.
        _ = CGRequestScreenCaptureAccess()
        if let url = URL(string:"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture") {
            NSWorkspace.shared.open(url)
        }
        status.stringValue = "Allow MAC-DOW Host in Screen Recording, then quit and reopen it if macOS asks. Nothing starts automatically."
        refreshPermissions()
    }
    @objc func controlPermission() {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String:true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
        if let url = URL(string:"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility") {
            NSWorkspace.shared.open(url)
        }
        status.stringValue = "Accessibility is optional for control. Viewing alone does not need it. Windows control still requires an explicit click."
        refreshPermissions()
    }
    func readOnly(_ arguments:[String],completion:@escaping(Data?,String?)->Void) {
        let executable = hostURL
        DispatchQueue.global(qos:.utility).async {
            let child = Process(); child.executableURL = executable; child.arguments = arguments
            let pipe = Pipe(); let error = Pipe()
            child.standardOutput = pipe; child.standardError = error
            do {
                try child.run()
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                let failure = error.fileHandleForReading.readDataToEndOfFile()
                child.waitUntilExit()
                DispatchQueue.main.async {
                    if child.terminationStatus == 0 && data.count <= 2*1024*1024 {
                        completion(data,nil)
                    } else {
                        completion(nil,String(data:failure.prefix(2048),encoding:.utf8) ?? "Operation unavailable.")
                    }
                }
            } catch { DispatchQueue.main.async { completion(nil,error.localizedDescription) } }
        }
    }
    func loadInterfaces() {
        readOnly(["--interfaces"]) { [weak self] data,error in
            guard let self = self else { return }
            if let data = data, let list = try? JSONSerialization.jsonObject(with:data) as? [[String:String]] {
                self.interfaces = list
                if list.isEmpty { self.status.stringValue = "Connect this Mac to the same Wi-Fi or Ethernet as Windows, then Refresh." }
            } else { self.status.stringValue = error ?? "Cannot read local interfaces." }
        }
    }
    @objc func chooseNetwork() {
        guard process == nil else { status.stringValue = "Stop sharing before changing networks."; reopen(); return }
        loadInterfaces()
        let alert = NSAlert(); alert.messageText = "Local network"
        alert.informativeText = "Automatic selects an active private LAN interface. No public or wildcard listener is used."
        let choices = NSPopUpButton(frame:NSRect(x:0,y:0,width:350,height:28))
        choices.addItem(withTitle:"Automatic")
        for item in interfaces { choices.addItem(withTitle:"\(item["name"] ?? "LAN") · \(item["address"] ?? "")") }
        alert.accessoryView = choices; alert.addButton(withTitle:"Use network"); alert.addButton(withTitle:"Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            selectedAddress = choices.indexOfSelectedItem > 0 ? interfaces[choices.indexOfSelectedItem-1]["address"] : nil
        }
    }
    @objc func toggleSharing() {
        if process != nil { stopSharing() } else { startSharing() }
    }
    func startSharing() {
        guard process == nil else { return }
        guard CGPreflightScreenCaptureAccess() else { status.stringValue = "Choose Screen Recording first."; return }
        let child = Process(); child.executableURL = hostURL
        child.arguments = ["--gui","--listen",selectedAddress.map { "\($0):0" } ?? "auto","--no-update-check"]
        let incoming = Pipe(); let outgoing = Pipe(); let errors = Pipe()
        child.standardInput = incoming; child.standardOutput = outgoing; child.standardError = errors
        process = child; input = incoming.fileHandleForWriting; outputPipe = outgoing; errorPipe = errors
        buffer.removeAll(); fingerprint = ""; manualDetails = ""; identity.stringValue = ""
        status.stringValue = "Starting a private LAN host. No screen is shared until you approve."
        startButton.title = "Stop sharing"; startButton.isEnabled = true; menuStop.isEnabled = true
        outgoing.fileHandleForReading.readabilityHandler = { [weak self,weak child] file in
            let data = file.availableData
            guard !data.isEmpty else { file.readabilityHandler = nil; return }
            DispatchQueue.main.async { guard let child = child, self?.process === child else { return }; self?.consume(data) }
        }
        errors.fileHandleForReading.readabilityHandler = { [weak self,weak child] file in
            let data = file.availableData
            guard !data.isEmpty else { file.readabilityHandler = nil; return }
            DispatchQueue.main.async {
                guard let child = child, self?.process === child else { return }
                self?.status.stringValue = String(data:data.prefix(2048),encoding:.utf8) ?? "Host stopped. Reopen setup and check permissions."
            }
        }
        child.terminationHandler = { [weak self] child in
            DispatchQueue.main.async {
                guard let self = self, self.process === child else { return }
                self.finished(child.terminationStatus)
            }
        }
        do { try child.run() }
        catch { status.stringValue = "Cannot start bundled host: \(error.localizedDescription)"; finished(1) }
    }
    func consume(_ data:Data) {
        guard buffer.count + data.count <= 16384 else { status.stringValue = "Invalid host response. Stopped safely."; stopSharing(); return }
        buffer.append(data)
        while let end = buffer.firstIndex(of:10) {
            let line = Data(buffer[..<end]); buffer.removeSubrange(...end)
            guard let object = try? JSONSerialization.jsonObject(with:line) as? [String:Any],
                  let type = object["type"] as? String else { status.stringValue = "Invalid host message."; stopSharing(); return }
            switch type {
            case "ready":
                guard let ip = object["address"] as? String, let port = object["port"] as? Int,
                      let fp = object["fingerprint"] as? String, fp.count == 64 else { stopSharing(); return }
                fingerprint = fp; state.receive(.ready)
                identity.stringValue = "Identity to compare in Windows:\n\(identityCode(fp))"
                manualDetails = "Address: \(ip):\(port)\nFull SHA-256: \(fp)\nTemporary token: \(object["token"] as? String ?? "")\n\nLegacy manual pairing only. Never share these beyond your Windows PC."
                publisher = NetService(domain:"local.",type:"_macdow._tcp.",name:Host.current().localizedName ?? "Mac",port:Int32(port))
                publisher?.delegate = self
                publisher?.setTXTRecord(NetService.data(fromTXTRecord:["v":Data("2".utf8),"fp":Data(fp.utf8),"ip":Data(ip.utf8)]))
                publisher?.publish()
                status.stringValue = "Waiting for Windows. Choose Find my Mac there, compare the identity, and approve its pairing number here."
                menuBar.button?.title = "● MAC-DOW"; menuBar.button?.contentTintColor = .systemOrange
                menuBar.button?.setAccessibilityLabel("MAC-DOW: hosting active")
                menuStatus.title = "Hosting active · approval required"
            case "consent": presentConsent(object)
            case "state": status.stringValue = object["message"] as? String ?? "Connection changed."
            default: break
            }
        }
    }
    func identityCode(_ fp:String)->String {
        let chars = Array(fp.prefix(32).uppercased())
        return stride(from:0,to:chars.count,by:4).map { String(chars[$0..<min($0+4,chars.count)]) }.joined(separator:"-")
    }
    func presentConsent(_ object:[String:Any]) {
        guard consent == nil, let id = object["id"] as? String, id.count == 32,
              let kind = object["kind"] as? String else { return }
        reopen()
        let alert = NSAlert(); alert.alertStyle = .warning
        if kind == "pair" {
            alert.messageText = "Pair Windows \(object["peer"] as? String ?? "")?"
            alert.informativeText = "Allow only if Windows shows this pairing number:\n\n\(object["code"] as? String ?? "")\n\nWindows must also match this Mac's identity above. Pairing permits window selection; screen sharing needs another approval."
        } else if kind == "stream" {
            alert.messageText = "Allow this screen stream?"
            alert.informativeText = "\(object["scope"] as? String ?? "Unknown scope")\n\(object["width"] ?? "") px · \(object["fps"] ?? "") fps · \(object["codec"] ?? "")\n\nKeyboard/mouse remain off until explicitly armed on focused Windows. Stop Sharing or Quit ends the session."
        } else { return }
        alert.addButton(withTitle:"Allow"); alert.addButton(withTitle:"Deny")
        consent = alert
        consentTimer = Timer.scheduledTimer(withTimeInterval:43,repeats:false) { [weak self,weak alert] _ in
            guard let self = self, let alert = alert, self.consent === alert else { return }
            self.window.endSheet(alert.window,returnCode:.abort)
        }
        alert.beginSheetModal(for:window) { [weak self,weak alert] response in
            guard let self = self, let alert = alert, self.consent === alert else { return }
            self.consentTimer?.invalidate(); self.consentTimer = nil; self.consent = nil
            self.send(["type":"consent","id":id,"allow":response == .alertFirstButtonReturn])
            self.status.stringValue = response == .alertFirstButtonReturn ? "Approved. Control remains explicit on Windows." : "Request denied or expired. Nothing new was shared."
        }
    }
    func send(_ value:[String:Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject:value) else { return }
        do { try input?.write(contentsOf:data + Data([10])) }
        catch { status.stringValue = "Host connection ended."; process?.terminate() }
    }
    @objc func stopSharing() {
        publisher?.stop(); publisher = nil
        if let alert = consent { window.endSheet(alert.window,returnCode:.abort) }
        state.receive(.stop)
        guard let child = process else { return }
        status.stringValue = "Stopping sharing and releasing control…"
        startButton.isEnabled = false; menuStop.isEnabled = false
        send(["type":"stop"])
        stopTimer?.invalidate()
        stopTimer = Timer.scheduledTimer(withTimeInterval:3,repeats:false) { [weak self,weak child] _ in
            guard let self = self, let child = child, self.process === child, child.isRunning else { return }
            child.terminate()
            DispatchQueue.main.asyncAfter(deadline:.now()+3) { [weak self,weak child] in
                guard let self = self, let child = child, self.process === child, child.isRunning else { return }
                Darwin.kill(child.processIdentifier,SIGKILL)
            }
        }
    }
    func finished(_ code:Int32) {
        stopTimer?.invalidate(); stopTimer = nil; publisher?.stop(); publisher = nil
        outputPipe?.fileHandleForReading.readabilityHandler = nil
        errorPipe?.fileHandleForReading.readabilityHandler = nil
        try? input?.close(); input = nil; process = nil; outputPipe = nil; errorPipe = nil
        if let alert = consent { window.endSheet(alert.window,returnCode:.abort) }
        state.receive(.stop); manualDetails = ""; fingerprint = ""; identity.stringValue = ""
        menuBar.button?.title = "MAC-DOW"; menuBar.button?.contentTintColor = nil
        menuBar.button?.setAccessibilityLabel("MAC-DOW: not sharing")
        menuStatus.title = "Not sharing"; menuStop.isEnabled = false; startButton.title = "Start sharing"
        if code == 0 { status.stringValue = "Not sharing. Start again when you want a new connection." }
        refreshPermissions()
        if state.quitting { NSApp.reply(toApplicationShouldTerminate:true) }
    }
    func netService(_ sender:NetService,didNotPublish errorDict:[String:NSNumber]) {
        status.stringValue = "Sharing host is ready, but automatic discovery is unavailable. Use Advanced connection details, or check local network access yourself. No firewall rule was changed."
    }
    @objc func showDetails() {
        let alert = NSAlert(); alert.messageText = "Advanced connection details"
        alert.informativeText = manualDetails.isEmpty ? "Start sharing first. Normal setup uses Find my Mac in Windows." : manualDetails
        alert.addButton(withTitle:"Done"); alert.runModal()
    }
    @objc func checkUpdates() {
        if updater.stage?["canApply"] as? Bool == true {updater.present()} else {updater.check(manual:true)}
    }
    @objc func toggleAutomaticUpdates() {updater.toggle()}
}
let application = NSApplication.shared
let delegate = HostApp()
application.delegate = delegate
application.setActivationPolicy(.accessory)
application.run()
