import AppKit
import ApplicationServices
import CoreGraphics
import Foundation
import Darwin

// Pure state model used by the app and its non-UI --self-test.
struct HostLifecycle {
    enum Action { case ready, closeWindow, reopen, stop, quit }
    var visible = false
    var hosting = false
    var quitting = false
    mutating func receive(_ action: Action) {
        switch action {
        case .ready: hosting = true
        case .closeWindow: visible = false
        case .reopen: visible = false
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
    state.receive(.reopen); precondition(!state.visible && state.hosting)
    state.receive(.stop); precondition(!state.hosting)
    state.receive(.ready); state.receive(.quit)
    precondition(!state.hosting && state.quitting)
    print("Host app lifecycle self-tests passed; no NSApplication, permissions, capture, listener or UI started.")
}
if CommandLine.arguments.contains("--self-test") { lifecycleTests(); exit(0) }

final class HostApp: NSObject, NSApplicationDelegate, NetServiceDelegate {
    var state = HostLifecycle()
    var menuBar: NSStatusItem!
    var menuStatus = NSMenuItem(title: "Not sharing", action: nil, keyEquivalent: "")
    var menuStop: NSMenuItem!
    var statusText = "Starting MAC-DOW" { didSet { menuStatus.title = String(statusText.prefix(72)); menuStatus.toolTip = statusText } }
    let permissionMenu = NSMenuItem(title:"Checking viewing access",action:nil,keyEquivalent:"")
    let identity = NSMenuItem(title:"Mac identity available after sharing starts",action:nil,keyEquivalent:"")
    var menuStart: NSMenuItem!
    var wantsSharing = true
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
        report("startup")
        refreshPermissions(); loadInterfaces(); updater.start()
        if CGPreflightScreenCaptureAccess() { startSharing() }
        else { statusText = "Screen Recording access needed - open this menu";report("permissions-missing",code:"screen") }
    }
    func makeMenu() {
        updater = HostUpdater(app:self)
        menuBar = NSStatusBar.system.statusItem(withLength:NSStatusItem.variableLength)
        menuBar.button?.title = " MAC-DOW"
        menuBar.button?.image = NSImage(systemSymbolName:"display.2",accessibilityDescription:"MAC-DOW")
        menuBar.button?.setAccessibilityLabel("MAC-DOW: not sharing")
        let menu = NSMenu(); menu.addItem(menuStatus); menu.addItem(.separator())
        menu.addItem(permissionMenu)
        menu.addItem(identity)
        menu.addItem(.separator())
        menuStart = menu.addItem(withTitle:"Start Sharing",action:#selector(toggleSharing),keyEquivalent:"")
        menu.addItem(withTitle:"Screen Recording Access…",action:#selector(screenPermission),keyEquivalent:"")
        menu.addItem(withTitle:"Keyboard and Mouse Access…",action:#selector(controlPermission),keyEquivalent:"")
        menu.addItem(withTitle:"Refresh Permissions",action:#selector(refreshSetup),keyEquivalent:"")
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
        NSApp.activate(ignoringOtherApps:true)
        refreshPermissions()
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender:NSApplication)->Bool { false }
    func applicationShouldHandleReopen(_ sender:NSApplication,hasVisibleWindows:Bool)->Bool {menuBar.button?.performClick(nil);return false}
    func applicationShouldTerminate(_ sender:NSApplication)->NSApplication.TerminateReply {
        state.receive(.quit)
        if process != nil { stopSharing(); return .terminateLater }
        return .terminateNow
    }
    @objc func quit() { NSApp.terminate(nil) }
    func report(_ event:String,code:String="ok") {
        let executable=hostURL
        DispatchQueue.global(qos:.utility).async {
            let child=Process();child.executableURL=executable;child.arguments=["--diagnose",event,code]
            child.standardInput=FileHandle.nullDevice;child.standardOutput=FileHandle.nullDevice;child.standardError=FileHandle.nullDevice
            try? child.run()
        }
    }
    @objc func refreshPermissions() {
        let screen = CGPreflightScreenCaptureAccess()
        let control = AXIsProcessTrusted()
        permissionMenu.title = "Viewing: \(screen ? "allowed" : "needs Screen Recording")    Control: \(control ? "allowed" : "optional Accessibility")"
        if process == nil { menuStart.isEnabled = screen }
    }
    @objc func refreshSetup() {refreshPermissions();loadInterfaces();if wantsSharing && process == nil && CGPreflightScreenCaptureAccess() {startSharing()}}
    @objc func screenPermission() {
        // Only an explicit user click reaches a system request/settings UI.
        _ = CGRequestScreenCaptureAccess()
        if let url = URL(string:"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture") {
            NSWorkspace.shared.open(url)
        }
        statusText = "Allow MAC-DOW Host in Screen Recording, then choose Refresh Permissions here. Reopen if macOS asks."
        refreshPermissions()
    }
    @objc func controlPermission() {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String:true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
        if let url = URL(string:"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility") {
            NSWorkspace.shared.open(url)
        }
        statusText = "Accessibility is optional for control. Viewing alone does not need it. Windows control still requires an explicit click."
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
                if list.isEmpty { self.statusText = "Connect this Mac to the same Wi-Fi or Ethernet as Windows, then Refresh." }
            } else { self.statusText = error ?? "Cannot read local interfaces." }
        }
    }
    @objc func chooseNetwork() {
        guard process == nil else { statusText = "Stop sharing before changing networks."; reopen(); return }
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
        wantsSharing = true
        guard CGPreflightScreenCaptureAccess() else { statusText = "Choose Screen Recording first."; return }
        let child = Process(); child.executableURL = hostURL
        child.arguments = ["--gui","--listen",selectedAddress.map { "\($0):0" } ?? "auto","--no-update-check"]
        let incoming = Pipe(); let outgoing = Pipe(); let errors = Pipe()
        child.standardInput = incoming; child.standardOutput = outgoing; child.standardError = errors
        process = child; input = incoming.fileHandleForWriting; outputPipe = outgoing; errorPipe = errors
        buffer.removeAll(); fingerprint = ""; manualDetails = ""; identity.title = "Mac identity available after sharing starts"
        statusText = "Starting a private LAN host. No screen is shared until you approve."
        menuStart.title = "Start Sharing"; menuStart.isEnabled = false; menuStop.isEnabled = true
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
                self?.statusText = String(data:data.prefix(2048),encoding:.utf8) ?? "Host stopped. Reopen setup and check permissions."
            }
        }
        child.terminationHandler = { [weak self] child in
            DispatchQueue.main.async {
                guard let self = self, self.process === child else { return }
                self.finished(child.terminationStatus)
            }
        }
        do { try child.run() }
        catch { report("fatal",code:"launcher");statusText = "Cannot start bundled host: \(error.localizedDescription)"; finished(1) }
    }
    func consume(_ data:Data) {
        guard buffer.count + data.count <= 16384 else { statusText = "Invalid host response. Stopped safely."; stopSharing(); return }
        buffer.append(data)
        while let end = buffer.firstIndex(of:10) {
            let line = Data(buffer[..<end]); buffer.removeSubrange(...end)
            guard let object = try? JSONSerialization.jsonObject(with:line) as? [String:Any],
                  let type = object["type"] as? String else { statusText = "Invalid host message."; stopSharing(); return }
            switch type {
            case "ready":
                guard let ip = object["address"] as? String, let port = object["port"] as? Int,
                      let fp = object["fingerprint"] as? String, fp.count == 64 else { stopSharing(); return }
                fingerprint = fp; state.receive(.ready)
                identity.title = "Mac identity: \(identityCode(fp))"
                manualDetails = "Address: \(ip):\(port)\nSHA-256 fingerprint: \(fp)\nTemporary token: \(object["token"] as? String ?? "")\n\nLegacy manual pairing only. Never share these beyond your Windows PC."
                publisher = NetService(domain:"local.",type:"_macdow._tcp.",name:Host.current().localizedName ?? "Mac",port:Int32(port))
                publisher?.delegate = self
                publisher?.setTXTRecord(NetService.data(fromTXTRecord:["v":Data("2".utf8),"fp":Data(fp.utf8),"ip":Data(ip.utf8)]))
                publisher?.publish()
                statusText = "Waiting for Windows. Choose Find my Mac there, compare the identity, and approve its pairing number here."
                menuBar.button?.title = "● MAC-DOW"; menuBar.button?.contentTintColor = .systemOrange
                menuBar.button?.setAccessibilityLabel("MAC-DOW: hosting active")
                menuStatus.title = "Hosting active · approval required"
            case "consent": presentConsent(object)
            case "state": statusText = object["message"] as? String ?? "Connection changed."
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
            alert.informativeText = "Allow only if Windows shows this pairing number:\n\n\(object["code"] as? String ?? "")\n\nWindows must also match this Mac's identity above. Pairing lets this PC send files to Downloads/MAC-DOW. Screen sharing needs another approval."
        } else if kind == "stream" {
            alert.messageText = "Allow this screen stream?"
            alert.informativeText = "\(object["scope"] as? String ?? "Unknown scope")\n\(object["width"] ?? "") px · \(object["fps"] ?? "") fps · \(object["codec"] ?? "")\n\nKeyboard/mouse remain off until explicitly armed on focused Windows. Stop Sharing or Quit ends the session."
        } else { return }
        alert.addButton(withTitle:"Allow"); alert.addButton(withTitle:"Deny")
        consent = alert
        let expiry = Timer(timeInterval:43,repeats:false) { [weak self,weak alert] _ in
            guard let self=self, let alert=alert, self.consent === alert else {return}
            NSApp.abortModal(); alert.window.orderOut(nil)
        }
        consentTimer = expiry
        RunLoop.main.add(expiry,forMode:.common)
        RunLoop.main.add(expiry,forMode:.modalPanel)
        let response = alert.runModal()
        expiry.invalidate(); consentTimer = nil; consent = nil
        send(["type":"consent","id":id,"allow":response == .alertFirstButtonReturn])
        statusText = response == .alertFirstButtonReturn ? "Approved - Windows control remains explicit" : "Request denied or expired - Mac is still available"
    }
    func send(_ value:[String:Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject:value) else { return }
        do { try input?.write(contentsOf:data + Data([10])) }
        catch { statusText = "Host connection ended."; process?.terminate() }
    }
    @objc func stopSharing() {
        wantsSharing = false
        publisher?.stop(); publisher = nil
        if let alert = consent { NSApp.abortModal(); alert.window.orderOut(nil) }
        state.receive(.stop)
        guard let child = process else { return }
        statusText = "Stopping sharing and releasing control…"
        menuStart.isEnabled = false; menuStop.isEnabled = false
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
        if let alert = consent { NSApp.abortModal(); alert.window.orderOut(nil) }
        state.receive(.stop); manualDetails = ""; fingerprint = ""; identity.title = "Mac identity available after sharing starts"
        menuBar.button?.title = "MAC-DOW"; menuBar.button?.contentTintColor = nil
        menuBar.button?.setAccessibilityLabel("MAC-DOW: not sharing")
        menuStatus.title = "Not sharing"; menuStop.isEnabled = false; menuStart.title = "Start sharing"
        if code == 0 { statusText = "Not sharing. Start again when you want a new connection." }
        refreshPermissions()
        if state.quitting { NSApp.reply(toApplicationShouldTerminate:true) }
    }
    func netService(_ sender:NetService,didNotPublish errorDict:[String:NSNumber]) {
        statusText = "Sharing host is ready, but automatic discovery is unavailable. Use Advanced connection details, or check local network access yourself. No firewall rule was changed."
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
