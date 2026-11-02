import AppKit
import Foundation
import Darwin

func updateCheckInterval(uptime:TimeInterval)->TimeInterval { uptime < 30 ? 5 : 30 }
func verifiedUpdateStage(_ result:[String:Any]?,expectedTag:String?=nil)->[String:Any]? {
    guard result?["ok"] as? Bool == true,let stage=result?["stage"] as? [String:Any],stage["canApply"] as? Bool == true,
          let plan=stage["plan"] as? [String:Any],let tag=plan["tag"] as? String,!tag.isEmpty,
          expectedTag == nil || tag == expectedTag else {return nil}
    return stage
}
func updaterPolicyTests() {
    precondition(updateCheckInterval(uptime:0)==5 && updateCheckInterval(uptime:29)==5)
    precondition(updateCheckInterval(uptime:30)==30 && updateCheckInterval(uptime:3600)==30)
    let plan:[String:Any] = ["tag":"v0.2.0"]
    var stage:[String:Any] = ["canApply":true,"plan":plan]
    let ready:[String:Any] = ["ok":true,"stage":stage]
    precondition(verifiedUpdateStage(ready,expectedTag:"v0.2.0") != nil)
    precondition(verifiedUpdateStage(ready,expectedTag:"v0.3.0") == nil)
    precondition(verifiedUpdateStage(["ok":false,"stage":stage]) == nil)
    stage["canApply"]=false
    precondition(verifiedUpdateStage(["ok":true,"stage":stage]) == nil)
    precondition(verifiedUpdateStage(nil) == nil)
}

// Updater failures must never stop LAN sharing or appear as permission errors.
final class HostUpdater {
    weak var app: HostApp?
    // The executable lives in a versioned cache. Keep the established settings
    // domain independent of Bundle.main so update preferences survive rebuilds.
    let defaults = UserDefaults(suiteName:"com.tbhtho.macdow.host")!
    var timer: Timer?
    var busy = false
    let started = ProcessInfo.processInfo.systemUptime
    var stage: [String:Any]?
    var lastMessage = "Updates have not been checked."
    var automatic: Bool { defaults.object(forKey:"automaticUpdates") as? Bool ?? true }
    init(app:HostApp) { self.app = app }
    func start() {
        if let path=defaults.string(forKey:"updateResult"),let data=try? Data(contentsOf:URL(fileURLWithPath:path)),
           let result=(try? JSONSerialization.jsonObject(with:data)) as? [String:Any] {
            lastMessage="Last update: "+(result["message"] as? String ?? "finished")
        }
        scheduleNextCheck()
        if automatic { check(manual:false) }
    }
    func scheduleNextCheck() {
        timer?.invalidate()
        let delay = updateCheckInterval(uptime:ProcessInfo.processInfo.systemUptime-started)
        let next = Timer(timeInterval:delay,repeats:false) { [weak self] _ in
            guard let self=self else {return}
            self.scheduleNextCheck();self.check(manual:false)
        }
        timer=next
        RunLoop.main.add(next,forMode:.common)
        RunLoop.main.add(next,forMode:.modalPanel)
    }
    func toggle() {
        defaults.set(!automatic,forKey:"automaticUpdates")
        app?.automaticUpdates.state = automatic ? .on : .off
        if automatic { check(manual:false) }
    }
    func run(_ arguments:[String], completion:@escaping([String:Any]?,String?)->Void) {
        guard let executable = app?.hostURL else { completion(nil,"Host helper unavailable.");return }
        DispatchQueue.global(qos:.utility).async {
            let child = Process(); child.executableURL = executable; child.arguments = ["--updater"] + arguments
            let output = Pipe(); child.standardOutput = output; child.standardError = FileHandle.nullDevice
            do {
                try child.run()
                DispatchQueue.global(qos:.utility).asyncAfter(deadline:.now()+310) { if child.isRunning { child.terminate() } }
                let data = output.fileHandleForReading.readDataToEndOfFile(); child.waitUntilExit()
                let result = data.count <= 2*1024*1024 ? (try? JSONSerialization.jsonObject(with:data)) as? [String:Any] : nil
                DispatchQueue.main.async { completion(result,result == nil ? "Update service did not return a valid response." : nil) }
            } catch { DispatchQueue.main.async { completion(nil,error.localizedDescription) } }
        }
    }
    func check(manual:Bool) {
        guard manual || automatic, !busy, app?.state.quitting != true else { return }; busy = true
        stage = nil
        app?.updateMenu.title = "Check for Updates…"
        run(["check","--channel","preview"]) { [weak self] result,error in
            guard let self = self else { return }
            self.lastMessage = result?["message"] as? String ?? error ?? "Update check unavailable."
            guard result?["status"] as? String == "available" else { self.busy=false;if manual {self.present()};return }
            if let path=self.defaults.string(forKey:"updateStage"),
               let data=try? Data(contentsOf:URL(fileURLWithPath:path).appendingPathComponent("stage.json")),
               let saved=(try? JSONSerialization.jsonObject(with:data)) as? [String:Any],
               let plan=saved["plan"] as? [String:Any], let fresh=result?["plan"] as? [String:Any],
               plan["tag"] as? String == fresh["tag"] as? String {
                self.stage=saved;self.finish(manual:manual);return
            }
            self.run(["stage","--channel","preview"]) { [weak self] result,error in
                guard let self=self else {return}
                self.lastMessage=result?["message"] as? String ?? error ?? "Update download unavailable."
                if result?["ok"] as? Bool == true, let staged=result?["stage"] as? [String:Any],let path=staged["directory"] as? String {
                    self.stage=staged;self.defaults.set(path,forKey:"updateStage")
                }
                self.finish(manual:manual)
            }
        }
    }
    func finish(manual:Bool) {
        guard let path=stage?["directory"] as? String else {
            busy=false;app?.updateMenu.toolTip=lastMessage
            if manual {present()};return
        }
        // Persisted canApply is display state, never evidence. Revalidate cached
        // downloads against the signed feed and this exact installation first.
        run(["preflight","--stage",path,"--install",appBundleURL.path]) { [weak self] result,error in
            guard let self=self else {return};self.busy=false
            if let fresh=verifiedUpdateStage(result) {
                self.stage=fresh
                self.app?.updateMenu.title="Restart to Update…"
                self.lastMessage="A verified update is ready. Automatic updates install and reopen mac-lan, briefly disconnecting sharing."
            } else {
                self.stage=nil
                self.lastMessage=result?["message"] as? String ?? error ?? "Downloaded update could not be revalidated."
            }
            self.app?.updateMenu.toolTip=self.lastMessage
            if self.stage?["canApply"] as? Bool == true && self.automatic {self.apply(manual:false)}
            else if manual {self.present()}
        }
    }
    func present() {
        guard let app=app else {return}
        app.reopen()
        let alert=NSAlert();alert.messageText="mac-lan Updates";alert.informativeText=lastMessage
        if stage?["canApply"] as? Bool == true {alert.addButton(withTitle:"Restart to update");alert.addButton(withTitle:"Later");if alert.runModal() == .alertFirstButtonReturn {apply()}}
        else {alert.addButton(withTitle:"Done");alert.runModal()}
    }
    func apply(manual:Bool=true) {
        guard !busy,manual || automatic,let app=app,!app.state.quitting,let stage=stage,stage["canApply"] as? Bool == true,let path=stage["directory"] as? String,
              let plan=stage["plan"] as? [String:Any],let tag=plan["tag"] as? String else {return}
        busy=true
        let bundle=appBundleURL.path
        run(["preflight","--stage",path,"--install",bundle]) { [weak self] result,error in
            guard let self=self else {return}
            guard !app.state.quitting,manual || self.automatic else {self.busy=false;return}
            guard verifiedUpdateStage(result,expectedTag:tag) != nil else {
                self.busy=false;self.lastMessage=result?["message"] as? String ?? error ?? "Update could not be verified.";self.stage=nil
                self.app?.updateMenu.toolTip=self.lastMessage;if manual {self.present()};return
            }
            do {
                let cache=try FileManager.default.url(for:.cachesDirectory,in:.userDomainMask,appropriateFor:nil,create:true)
                let runner=cache.appendingPathComponent("MAC-DOW/updates/runner-"+UUID().uuidString)
                try FileManager.default.createDirectory(at:runner,withIntermediateDirectories:true)
                let worker=runner.appendingPathComponent("MacDowHost")
                try FileManager.default.copyItem(at:app.hostURL,to:worker)
                try FileManager.default.setAttributes([.posixPermissions:0o700],ofItemAtPath:worker.path)
                let child=Process();child.executableURL=worker;child.currentDirectoryURL=runner
                // The Go launcher remains inside .app while this cached AppKit
                // child runs. Wait for its exit before replacing the bundle.
                guard appLaunchContext.launcherPID == getppid() else {
                    throw NSError(domain:"mac-lan",code:3,userInfo:[NSLocalizedDescriptionKey:"App launcher ended; reopen mac-lan before updating."])
                }
                child.arguments=["--updater","apply","--stage",path,"--install",bundle,"--wait-pid",String(appLaunchContext.launcherPID),"--confirm","APPLY "+tag,"--relaunch","--result-file",URL(fileURLWithPath:path).appendingPathComponent("worker-result.json").path]
                child.standardInput=FileHandle.nullDevice;child.standardOutput=FileHandle.nullDevice;child.standardError=FileHandle.nullDevice
                try child.run();self.defaults.set(URL(fileURLWithPath:path).appendingPathComponent("worker-result.json").path,forKey:"updateResult")
                NSApp.terminate(nil) // HostApp waits for sharing shutdown before allowing app exit.
            } catch {
                self.busy=false;self.lastMessage="Could not start updater: "+error.localizedDescription
                self.app?.updateMenu.toolTip=self.lastMessage;if manual {self.present()}
            }
        }
    }
}
