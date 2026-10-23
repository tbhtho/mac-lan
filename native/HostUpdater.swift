import AppKit
import Foundation

// Updater failures must never stop LAN sharing or appear as permission errors.
final class HostUpdater {
    weak var app: HostApp?
    let defaults = UserDefaults.standard
    var timer: Timer?
    var busy = false
    var stage: [String:Any]?
    var lastMessage = "Updates have not been checked."
    var automatic: Bool { defaults.object(forKey:"automaticUpdates") as? Bool ?? true }
    init(app:HostApp) { self.app = app }
    func start() {
        if let path=defaults.string(forKey:"updateResult"),let data=try? Data(contentsOf:URL(fileURLWithPath:path)),
           let result=(try? JSONSerialization.jsonObject(with:data)) as? [String:Any] {
            lastMessage="Last update: "+(result["message"] as? String ?? "finished")
        }
        timer = Timer.scheduledTimer(withTimeInterval:6*60*60,repeats:true) { [weak self] _ in self?.check(manual:false) }
        if automatic { check(manual:false) }
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
        guard manual || automatic, !busy else { return }; busy = true
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
        busy=false
        if let message=stage?["message"] as? String {lastMessage=message}
        if stage?["canApply"] as? Bool == true {app?.updateMenu.title="Restart to Update…";lastMessage="An update is downloaded. Restart when you are ready; sharing continues until then."}
        app?.updateMenu.toolTip=lastMessage
        if manual {present()}
    }
    func present() {
        guard let app=app else {return}
        app.reopen()
        let alert=NSAlert();alert.messageText="MAC-DOW Updates";alert.informativeText=lastMessage
        if stage?["canApply"] as? Bool == true {alert.addButton(withTitle:"Restart to update");alert.addButton(withTitle:"Later");if alert.runModal() == .alertFirstButtonReturn {apply()}}
        else {alert.addButton(withTitle:"Done");alert.runModal()}
    }
    func apply() {
        guard !busy,let app=app,let stage=stage,let path=stage["directory"] as? String,
              let plan=stage["plan"] as? [String:Any],let tag=plan["tag"] as? String else {return}
        busy=true
        let bundle=Bundle.main.bundleURL.path
        run(["preflight","--stage",path,"--install",bundle]) { [weak self] result,error in
            guard let self=self else {return};self.busy=false
            guard result?["ok"] as? Bool == true else {self.lastMessage=result?["message"] as? String ?? error ?? "Update could not be verified.";self.stage=nil;self.present();return}
            do {
                let cache=try FileManager.default.url(for:.cachesDirectory,in:.userDomainMask,appropriateFor:nil,create:true)
                let runner=cache.appendingPathComponent("MAC-DOW/updates/runner-"+UUID().uuidString)
                try FileManager.default.createDirectory(at:runner,withIntermediateDirectories:true)
                let worker=runner.appendingPathComponent("MacDowHost")
                try FileManager.default.copyItem(at:app.hostURL,to:worker)
                try FileManager.default.setAttributes([.posixPermissions:0o700],ofItemAtPath:worker.path)
                let child=Process();child.executableURL=worker;child.currentDirectoryURL=runner
                child.arguments=["--updater","apply","--stage",path,"--install",bundle,"--wait-pid",String(ProcessInfo.processInfo.processIdentifier),"--confirm","APPLY "+tag,"--relaunch","--result-file",URL(fileURLWithPath:path).appendingPathComponent("worker-result.json").path]
                child.standardInput=FileHandle.nullDevice;child.standardOutput=FileHandle.nullDevice;child.standardError=FileHandle.nullDevice
                try child.run();self.defaults.set(URL(fileURLWithPath:path).appendingPathComponent("worker-result.json").path,forKey:"updateResult")
                NSApp.terminate(nil) // HostApp waits for sharing shutdown before allowing app exit.
            } catch {self.lastMessage="Could not start updater: "+error.localizedDescription;self.present()}
        }
    }
}
