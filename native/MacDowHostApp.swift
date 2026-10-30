import AppKit
import ApplicationServices
import CoreGraphics
import Foundation
import CoreFoundation
import Darwin

// Pure state model used by the app and its non-UI --self-test.
struct HostLifecycle {
    enum Action { case ready, closeWindow, reopen, stop, quit }
    var visible = false
    var hosting = false
    var quitting = false
    mutating func receive(_ action: Action) {
        switch action {
        case .ready: if !quitting { hosting = true }
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
    state.receive(.ready); precondition(!state.hosting && state.quitting)
    precondition(hostMessage(Data("{\"type\":\"status\",\"message\":\"Preparing capture\"}".utf8)) == "Preparing capture")
    precondition(hostMessage(Data("{\"type\":\"ready\",\"token\":\"private\"}".utf8)) == nil)
    precondition(hostMessage(Data("{broken JSON".utf8)) == nil)
    precondition((try? AppLaunchContext.read(["mac-lan"])) == nil)
    precondition((try? AppLaunchContext.read(["mac-lan","--app-bundle","/tmp/mac-lan.app","--launcher-pid","-1"])) == nil)
    print("Host app lifecycle self-tests passed; no NSApplication, permissions, capture, listener or UI started.")
}
// Keep partial pipe writes and protocol payloads out of the menu's status text.
func hostMessage(_ line:Data)->String? {
    guard let text = String(data:line,encoding:.utf8)?.trimmingCharacters(in:.whitespacesAndNewlines), !text.isEmpty else {return nil}
    if text.hasPrefix("{") || text.hasPrefix("[") {
        guard let object = (try? JSONSerialization.jsonObject(with:line)) as? [String:Any],
              let kind = object["type"] as? String, ["status","state","error"].contains(kind),
              let message = object["message"] as? String else {return nil}
        return String(message.prefix(2048))
    }
    return String(text.prefix(2048))
}
if CommandLine.arguments.contains("--self-test") { lifecycleTests(); exit(0) }

// The AppKit executable is cached outside the .app. Bundle.main would point at
// that cache, so all helper/update paths must come from the validated launcher.
struct AppLaunchContext {
    let bundleURL: URL
    let launcherPID: pid_t
    static func read(_ arguments:[String]) throws -> AppLaunchContext {
        guard arguments.count == 5, arguments[1] == "--app-bundle", arguments[3] == "--launcher-pid",
              arguments[2].hasPrefix("/"), let pid = Int32(arguments[4]), pid > 1, pid == getppid() else {
            throw NSError(domain:"mac-lan",code:1,userInfo:[NSLocalizedDescriptionKey:"Open mac-lan.app to start its menu-bar companion."])
        }
        let url = URL(fileURLWithPath:arguments[2],isDirectory:true).standardizedFileURL.resolvingSymlinksInPath()
        let files = FileManager.default
        guard url.pathExtension == "app", let bundle = Bundle(url:url),
              bundle.bundleIdentifier == "com.tbhtho.macdow.host",
              bundle.object(forInfoDictionaryKey:"CFBundleExecutable") as? String == "mac-lan",
              bundle.object(forInfoDictionaryKey:"LSUIElement") as? Bool == true,
              files.isExecutableFile(atPath:url.appendingPathComponent("Contents/MacOS/mac-lan").path),
              files.isExecutableFile(atPath:url.appendingPathComponent("Contents/Helpers/MacDowHost").path) else {
            throw NSError(domain:"mac-lan",code:2,userInfo:[NSLocalizedDescriptionKey:"The mac-lan app bundle is incomplete. Extract the complete app and open it again."])
        }
        return AppLaunchContext(bundleURL:url,launcherPID:pid)
    }
}
let appLaunchContext: AppLaunchContext = {
    do { return try AppLaunchContext.read(CommandLine.arguments) }
    catch { fputs(error.localizedDescription + "\n",stderr); exit(1) }
}()
let appBundleURL = appLaunchContext.bundleURL

// Deferred application termination runs a modal run loop. Deliver completion
// there even when terminate() was called from a main DispatchQueue callback.
func onHostRunLoop(_ block:@escaping()->Void) {
    let loop = CFRunLoopGetMain()
    let modes = [RunLoop.Mode.default.rawValue,RunLoop.Mode.modalPanel.rawValue] as CFArray
    CFRunLoopPerformBlock(loop,modes,block)
    CFRunLoopWakeUp(loop)
}

final class HostApp: NSObject, NSApplicationDelegate, NetServiceDelegate, NSMenuDelegate {
    var state = HostLifecycle()
    var menuBar: NSStatusItem!
    var menuStatus = NSMenuItem(title: "Not sharing", action: nil, keyEquivalent: "")
    var menuStop: NSMenuItem!
    var statusText = "Starting mac-lan" { didSet { updateHeader() } }
    var identityText = "" { didSet { updateHeader() } }
    // Menu header: status dot and title, a short status line, and the Mac identity.
    let headerTitle = NSTextField(labelWithString:"mac-lan")
    let headerStatus = NSTextField(wrappingLabelWithString:"")
    let headerIdentity = NSTextField(wrappingLabelWithString:"")
    var headerView: NSView?
    let screenAccess = NSMenuItem(title:"Allow Screen Recording…",action:#selector(screenPermission),keyEquivalent:"")
    let controlAccess = NSMenuItem(title:"Allow Keyboard & Mouse…",action:#selector(controlPermission),keyEquivalent:"")
    let refreshAccess = NSMenuItem(title:"Refresh Permissions",action:#selector(refreshSetup),keyEquivalent:"")
    var menuStart: NSMenuItem!
    var wantsSharing = true
    var process: Process?
    var input: FileHandle?
    var outputPipe: Pipe?
    var errorPipe: Pipe?
    var buffer = Data()
    var errorBuffer = Data()
    var lastHostError: String?
    var publisher: NetService?
    var consent: NSWindow?
    var consentTimer: Timer?
    var stopTimer: Timer?
    var interfaces: [[String:String]] = []
    var selectedAddress: String?
    var fingerprint = ""
    var updater: HostUpdater!
    var updateMenu: NSMenuItem!
    var automaticUpdates: NSMenuItem!

    var hostURL: URL {
        appBundleURL.appendingPathComponent("Contents/Helpers/MacDowHost")
    }
    func applicationDidFinishLaunching(_ notification: Notification) {
        makeMenu()
        report("startup")
        refreshPermissions(); loadInterfaces(); updater.start()
        startSharing()
        if !CGPreflightScreenCaptureAccess() { report("permissions-missing",code:"screen") }
    }
    func makeMenu() {
        updater = HostUpdater(app:self)
        menuBar = NSStatusBar.system.statusItem(withLength:NSStatusItem.variableLength)
        menuBar.button?.image = NSImage(systemSymbolName:"display.2",accessibilityDescription:"mac-lan")
        menuBar.button?.image?.isTemplate = true
        menuBar.button?.setAccessibilityLabel("mac-lan: not sharing")
        menuBar.button?.toolTip = "mac-lan"
        let menu = NSMenu()
        let header = makeHeader(); headerView = header; menuStatus.view = header
        menu.addItem(menuStatus); menu.addItem(.separator())
        menu.addItem(NSMenuItem.sectionHeader(title:"Permissions"))
        menu.addItem(screenAccess); menu.addItem(controlAccess); menu.addItem(refreshAccess)
        refreshAccess.image = symbol("arrow.clockwise")
        menu.addItem(.separator())
        menuStart = menu.addItem(withTitle:"Start Sharing",action:#selector(toggleSharing),keyEquivalent:"")
        menuStart.image = symbol("play.circle")
        menuStop = menu.addItem(withTitle:"Stop Sharing",action:#selector(stopSharing),keyEquivalent:"")
        menuStop.image = symbol("stop.circle")
        menu.addItem(withTitle:"Send File to Windows…",action:#selector(sendFileToWindows),keyEquivalent:"").image = symbol("doc.badge.arrow.up")
        menu.addItem(withTitle:"Cancel Outgoing File",action:#selector(cancelOutgoingFile),keyEquivalent:"")
        menu.addItem(withTitle:"Network…",action:#selector(chooseNetwork),keyEquivalent:"").image = symbol("network")
        menu.addItem(.separator())
        updateMenu = menu.addItem(withTitle:"Check for Updates…",action:#selector(checkUpdates),keyEquivalent:"")
        updateMenu.image = symbol("arrow.down.circle")
        automaticUpdates = menu.addItem(withTitle:"Download Updates Automatically",action:#selector(toggleAutomaticUpdates),keyEquivalent:"")
        automaticUpdates.state = updater.automatic ? .on : .off
        menu.addItem(.separator()); menu.addItem(withTitle:"Quit mac-lan",action:#selector(quit),keyEquivalent:"q").image = symbol("power")
        for item in menu.items { item.target = self }
        menu.autoenablesItems = false; menuStop.isEnabled = false; menu.delegate = self; menuBar.menu = menu
        updateHeader()
    }
    func symbol(_ name:String,_ color:NSColor?=nil,size:CGFloat=13)->NSImage? {
        guard let base = NSImage(systemSymbolName:name,accessibilityDescription:nil) else { return nil }
        var configuration = NSImage.SymbolConfiguration(pointSize:size,weight:.regular)
        if let color = color { configuration = configuration.applying(NSImage.SymbolConfiguration(paletteColors:[color])) }
        let image = base.withSymbolConfiguration(configuration) ?? base
        image.isTemplate = color == nil
        return image
    }
    func makeHeader()->NSView {
        headerTitle.font = .systemFont(ofSize:13,weight:.semibold)
        headerStatus.font = .systemFont(ofSize:12); headerStatus.textColor = .secondaryLabelColor
        headerStatus.preferredMaxLayoutWidth = 268; headerStatus.maximumNumberOfLines = 3
        headerIdentity.font = .monospacedSystemFont(ofSize:11,weight:.regular); headerIdentity.textColor = .tertiaryLabelColor
        headerIdentity.preferredMaxLayoutWidth = 268
        let stack = NSStackView(views:[headerTitle,headerStatus,headerIdentity])
        stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 3
        stack.edgeInsets = NSEdgeInsets(top:6,left:16,bottom:8,right:16)
        stack.translatesAutoresizingMaskIntoConstraints = false
        let view = NSView(frame:NSRect(x:0,y:0,width:300,height:64))
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo:view.leadingAnchor), stack.trailingAnchor.constraint(equalTo:view.trailingAnchor),
            stack.topAnchor.constraint(equalTo:view.topAnchor), stack.bottomAnchor.constraint(equalTo:view.bottomAnchor),
            stack.widthAnchor.constraint(equalToConstant:300)])
        return view
    }
    func updateHeader() {
        guard let header = headerView else { return }
        let sharing = process != nil
        headerTitle.stringValue = !sharing ? "Not sharing" : fingerprint.isEmpty ? "Starting…" : "Sharing"
        headerStatus.stringValue = statusText; headerStatus.toolTip = statusText
        headerIdentity.stringValue = identityText; headerIdentity.isHidden = identityText.isEmpty
        header.setFrameSize(NSSize(width:300,height:header.fittingSize.height))
        menuStart?.isHidden = sharing; menuStop?.isHidden = !sharing
        menuBar?.button?.appearsDisabled = !sharing
    }
    @objc func sendFileToWindows() {
        guard process != nil else { statusText = "Start sharing and connect Windows first."; return }
        NSApp.activate(ignoringOtherApps:true)
        let picker = NSOpenPanel(); picker.title = "Choose a file to send to Windows"
        picker.canChooseFiles = true; picker.canChooseDirectories = false; picker.allowsMultipleSelection = false
        picker.begin { response in
            if response == .OK, let url = picker.url { self.send(["type":"send-file","path":url.path]) }
        }
    }
    @objc func cancelOutgoingFile() { send(["type":"cancel-file"]) }
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
    func menuWillOpen(_ menu:NSMenu) { refreshPermissions(); updateHeader() }
    @objc func refreshPermissions() {
        let screen = CGPreflightScreenCaptureAccess()
        let control = AXIsProcessTrusted()
        // A granted permission is a checked, inactive status line; only a missing one is actionable.
        func show(_ item:NSMenuItem,granted:Bool,allowed:String,missing:String) {
            item.title = granted ? allowed : missing
            item.state = .off
            item.image = granted ? symbol("checkmark") : symbol("exclamationmark.triangle")
            item.isEnabled = !granted
            item.toolTip = granted ? nil : "Opens System Settings for mac-lan (\(appBundleURL.path))."
        }
        show(screenAccess,granted:screen,allowed:"Screen Recording",missing:"Allow Screen Recording…")
        show(controlAccess,granted:control,allowed:"Keyboard & Mouse",missing:"Allow Keyboard & Mouse…")
        refreshAccess.isHidden = screen && control
        if process == nil { menuStart.isEnabled = !state.quitting }
    }
    @objc func refreshSetup() {
        refreshPermissions();loadInterfaces()
        if wantsSharing && process == nil { startSharing() }
        else if process != nil { statusText = "Permissions refreshed. Stop and start sharing to apply changed viewing access." }
    }
    @objc func screenPermission() {
        // Only an explicit user click reaches a system request/settings UI.
        _ = CGRequestScreenCaptureAccess()
        if let url = URL(string:"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture") {
            NSWorkspace.shared.open(url)
        }
        statusText = "Allow mac-lan (\(appBundleURL.path)) in Screen Recording, then stop and start sharing here. Reopen mac-lan if macOS asks."
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
        guard process == nil, !state.quitting else { return }
        wantsSharing = true
        let child = Process(); child.executableURL = hostURL
        child.arguments = ["--gui","--listen",selectedAddress.map { "\($0):0" } ?? "auto","--no-update-check"]
        let incoming = Pipe(); let outgoing = Pipe(); let errors = Pipe()
        child.standardInput = incoming; child.standardOutput = outgoing; child.standardError = errors
        process = child; input = incoming.fileHandleForWriting; outputPipe = outgoing; errorPipe = errors
        buffer.removeAll(); errorBuffer.removeAll(); lastHostError = nil; fingerprint = ""; identityText = ""
        statusText = "Starting. Nothing is shared until you approve it here."
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
                self?.consumeError(data)
            }
        }
        child.terminationHandler = { [weak self] child in
            onHostRunLoop {
                guard let self = self, self.process === child else { return }
                self.finished(child.terminationStatus)
            }
        }
        do { try child.run() }
        catch { report("fatal",code:"launcher");statusText = "Cannot start bundled host: \(error.localizedDescription)"; finished(1) }
    }
    func consumeError(_ data:Data) {
        guard errorBuffer.count + data.count <= 16384 else { errorBuffer.removeAll(); return }
        errorBuffer.append(data)
        while let end = errorBuffer.firstIndex(of:10) {
            let line = Data(errorBuffer[..<end]); errorBuffer.removeSubrange(...end)
            if let message = hostMessage(line) {
                lastHostError = message
                if !state.hosting { statusText = message }
            }
        }
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
                guard wantsSharing, !state.quitting else { return }
                guard let ip = object["address"] as? String, let port = object["port"] as? Int,
                      (1...65535).contains(port), let fp = object["fingerprint"] as? String,
                      fp.count == 64, fp.allSatisfy({ $0.isHexDigit }) else { stopSharing(); return }
                fingerprint = fp; state.receive(.ready)
                identityText = "Mac ID  \(identityCode(fp))"
                publisher = NetService(domain:"local.",type:"_macdow._tcp.",name:Host.current().localizedName ?? "Mac",port:Int32(port))
                publisher?.delegate = self
                publisher?.setTXTRecord(NetService.data(fromTXTRecord:["v":Data("2".utf8),"fp":Data(fp.utf8),"ip":Data(ip.utf8)]))
                publisher?.publish()
                statusText = CGPreflightScreenCaptureAccess() ? "Ready. Connect from Windows; you approve pairing and viewing here." : "Ready for pairing and files. Allow Screen Recording below for video."
                menuBar.button?.setAccessibilityLabel("mac-lan: ready, approval required")
            case "consent": presentConsent(object)
            case "state": if wantsSharing && !state.quitting { statusText = hostMessage(line) ?? "Connection changed." }
            default: break
            }
        }
    }
    func identityCode(_ fp:String)->String {
        let chars = Array(fp.prefix(32).uppercased())
        return stride(from:0,to:chars.count,by:4).map { String(chars[$0..<min($0+4,chars.count)]) }.joined(separator:"-")
    }
    func presentConsent(_ object:[String:Any]) {
        guard wantsSharing, !state.quitting, consent == nil, let id = object["id"] as? String, id.count == 32,
              let kind = object["kind"] as? String else { return }
        reopen()
        let title: String, detail: String, code: String?
        if kind == "pair" {
            title = "Connect Windows to this Mac?"
            code = object["code"] as? String ?? ""
            detail = "Allow only if Windows shows the same number. Windows can then send files to Downloads/mac-lan and view this screen until sharing stops. Keyboard and mouse stay off until you click the Mac view on Windows."
        } else if kind == "stream" {
            title = "Let Windows view this screen?"
            code = nil
            detail = "\(object["scope"] as? String ?? "Full desktop") · \(object["width"] ?? "") px · \(object["fps"] ?? "") fps\n\nKeyboard and mouse stay off until you click the Mac view on Windows. Stop Sharing ends the session."
        } else { return }
        let panel = consentPanel(title:title,detail:detail,code:code)
        consent = panel
        let expiry = Timer(timeInterval:43,repeats:false) { [weak self,weak panel] _ in
            guard let self=self, let panel=panel, self.consent === panel else {return}
            NSApp.abortModal(); panel.orderOut(nil)
        }
        consentTimer = expiry
        RunLoop.main.add(expiry,forMode:.common)
        RunLoop.main.add(expiry,forMode:.modalPanel)
        let response = NSApp.runModal(for:panel)
        panel.orderOut(nil)
        expiry.invalidate(); consentTimer = nil; consent = nil
        let allowed = response == .OK
        send(["type":"consent","id":id,"allow":allowed])
        statusText = allowed ? "Connected. Windows controls the Mac only after a click there." : "Request denied or expired. The Mac is still available."
    }
    // Dark glass approval window matching the Windows app: symbol, title, optional number, detail, Deny / Allow.
    func consentPanel(title:String,detail:String,code:String?)->NSPanel {
        let panel = NSPanel(contentRect:NSRect(x:0,y:0,width:400,height:220),styleMask:[.titled,.fullSizeContentView],backing:.buffered,defer:false)
        panel.titlebarAppearsTransparent = true; panel.titleVisibility = .hidden
        panel.isMovableByWindowBackground = true; panel.level = .floating
        panel.appearance = NSAppearance(named:.darkAqua)
        let glass = NSVisualEffectView(); glass.material = .hudWindow; glass.blendingMode = .behindWindow; glass.state = .active
        panel.contentView = glass
        let icon = NSImageView(image:symbol("display.2",nil,size:28) ?? NSImage())
        icon.contentTintColor = .labelColor
        let heading = NSTextField(labelWithString:title); heading.font = .systemFont(ofSize:17,weight:.semibold)
        let body = NSTextField(wrappingLabelWithString:detail); body.font = .systemFont(ofSize:13); body.textColor = .secondaryLabelColor
        body.preferredMaxLayoutWidth = 336
        var views: [NSView] = [icon,heading]
        if let code = code {
            let number = NSTextField(labelWithString:code); number.font = .monospacedDigitSystemFont(ofSize:30,weight:.semibold)
            views.append(number)
        }
        views.append(body)
        let deny = NSButton(title:"Deny",target:self,action:#selector(consentDeny)); deny.keyEquivalent = "\u{1b}"
        let allow = NSButton(title:"Allow",target:self,action:#selector(consentAllow)); allow.keyEquivalent = "\r"
        for button in [deny,allow] { button.bezelStyle = .rounded; button.controlSize = .large }
        let spacer = NSView(); spacer.setContentHuggingPriority(.defaultLow,for:.horizontal)
        let buttons = NSStackView(views:[spacer,deny,allow]); buttons.spacing = 10
        buttons.translatesAutoresizingMaskIntoConstraints = false
        views.append(buttons)
        let stack = NSStackView(views:views); stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 10
        stack.setCustomSpacing(20,after:body)
        stack.edgeInsets = NSEdgeInsets(top:30,left:32,bottom:24,right:32)
        stack.translatesAutoresizingMaskIntoConstraints = false
        glass.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo:glass.leadingAnchor), stack.trailingAnchor.constraint(equalTo:glass.trailingAnchor),
            stack.topAnchor.constraint(equalTo:glass.topAnchor), stack.bottomAnchor.constraint(equalTo:glass.bottomAnchor),
            stack.widthAnchor.constraint(equalToConstant:400), buttons.widthAnchor.constraint(equalToConstant:336)])
        panel.setContentSize(stack.fittingSize)
        panel.center()
        return panel
    }
    @objc func consentAllow() { NSApp.stopModal(withCode:.OK) }
    @objc func consentDeny() { NSApp.stopModal(withCode:.cancel) }
    func send(_ value:[String:Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject:value) else { return }
        do { try input?.write(contentsOf:data + Data([10])) }
        catch { statusText = "Host connection ended."; process?.terminate() }
    }
    @objc func stopSharing() {
        wantsSharing = false
        publisher?.stop(); publisher = nil
        if let panel = consent { NSApp.abortModal(); panel.orderOut(nil) }
        state.receive(.stop)
        guard let child = process else { return }
        statusText = "Stopping sharing and releasing control…"
        menuStart.isEnabled = false; menuStop.isEnabled = false
        send(["type":"stop"])
        stopTimer?.invalidate()
        let graceful = Timer(timeInterval:3,repeats:false) { [weak self,weak child] _ in
            guard let self = self, let child = child, self.process === child, child.isRunning else { return }
            child.terminate()
            let forced = Timer(timeInterval:3,repeats:false) { [weak self,weak child] _ in
                guard let self = self, let child = child, self.process === child, child.isRunning else { return }
                Darwin.kill(child.processIdentifier,SIGKILL)
            }
            self.stopTimer = forced
            RunLoop.main.add(forced,forMode:.common)
            RunLoop.main.add(forced,forMode:.modalPanel)
        }
        stopTimer = graceful
        RunLoop.main.add(graceful,forMode:.common)
        RunLoop.main.add(graceful,forMode:.modalPanel)
    }
    func finished(_ code:Int32) {
        stopTimer?.invalidate(); stopTimer = nil; publisher?.stop(); publisher = nil
        outputPipe?.fileHandleForReading.readabilityHandler = nil
        errorPipe?.fileHandleForReading.readabilityHandler = nil
        try? input?.close(); input = nil; process = nil; outputPipe = nil; errorPipe = nil
        if let panel = consent { NSApp.abortModal(); panel.orderOut(nil) }
        if let message = hostMessage(errorBuffer) { lastHostError = message }
        errorBuffer.removeAll()
        state.receive(.stop); fingerprint = ""; identityText = ""
        menuBar.button?.setAccessibilityLabel("mac-lan: not sharing")
        menuStop.isEnabled = false; menuStart.title = "Start Sharing"
        statusText = code == 0 ? "Not sharing. Start again when you want a new connection." : lastHostError ?? "Sharing stopped. Check permissions and the local network, then start again."
        refreshPermissions()
        if state.quitting { NSApp.reply(toApplicationShouldTerminate:true) }
    }
    func netService(_ sender:NetService,didNotPublish errorDict:[String:NSNumber]) {
        statusText = "Local discovery is unavailable. Check that both computers use the same LAN and allow mac-lan Local Network access in System Settings, then restart sharing."
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
