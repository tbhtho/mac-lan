import Foundation
import AppKit
import ApplicationServices
import Darwin
import ScreenCaptureKit
import CoreGraphics
import CoreMedia
import CoreImage
import VideoToolbox
import ImageIO
import UniformTypeIdentifiers

// Pure lease model; --self-test never queries permissions, captures, or posts events.
struct Lease {
    var deadline: TimeInterval = 0
    var active = false
    mutating func update(_ active: Bool, now: TimeInterval) { self.active = active; deadline = now + 0.85 }
    func valid(now: TimeInterval) -> Bool { active && now < deadline }
    mutating func release() { active = false; deadline = 0 }
}

func status(_ message: String, paused: Bool = false) {
    let value: [String: Any] = ["type": "status", "message": message, "paused": paused]
    if let b = try? JSONSerialization.data(withJSONObject: value) { try? FileHandle.standardError.write(contentsOf:b + Data([10])) }
}
func jsonStatus(_ value: [String: Any]) {
    if let b = try? JSONSerialization.data(withJSONObject: value) { try? FileHandle.standardError.write(contentsOf:b + Data([10])) }
}
func die(_ message: String) -> Never { status(message, paused: true); exit(1) }
func now() -> TimeInterval { ProcessInfo.processInfo.systemUptime }
func existingPermissions() -> Bool {
    let screen = CGPreflightScreenCaptureAccess(), input = AXIsProcessTrusted()
    status("Existing permissions: Screen Recording \(screen ? "granted" : "missing"); Accessibility \(input ? "granted" : "missing - viewing only"). No permission request made.")
    return screen
}
func shareable() async throws -> SCShareableContent {
    try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true)
}

// At most one scheduled main-queue drain and 32 pending input messages.
// Lease time starts on receipt, never when a delayed main queue processes it.
final class InputMailbox {
    let lock=NSLock()
    var messages:[([String:Any],TimeInterval)]=[]
    var scheduled=false,failed=false
    let receive:([String:Any],TimeInterval)->Void
    let failure:()->Void
    init(receive:@escaping([String:Any],TimeInterval)->Void,failure:@escaping()->Void){self.receive=receive;self.failure=failure}
    func push(_ message:[String:Any],received:TimeInterval) {
        lock.lock()
        if failed { lock.unlock();return }
        if message["type"] as? String == "release" { messages.removeAll() }
        if messages.count>=32 { failed=true;messages.removeAll() } else { messages.append((message,received)) }
        let schedule = !scheduled
        scheduled=true;lock.unlock()
        if schedule { DispatchQueue.main.async { self.drain() } }
    }
    func fail(){lock.lock();failed=true;messages.removeAll();let schedule = !scheduled;scheduled=true;lock.unlock();if schedule { DispatchQueue.main.async { self.drain() } }}
    func drain(){
        lock.lock();let bad=failed;let batch=messages;messages.removeAll();lock.unlock()
        if bad { failure();return }
        for (message,received) in batch { receive(message,received) }
        lock.lock();let again=failed || !messages.isEmpty;if !again { scheduled=false };lock.unlock()
        if again { DispatchQueue.main.async { self.drain() } }
    }
}

final class Engine: NSObject, SCStreamOutput, SCStreamDelegate {
    var stream: SCStream?
    var selected: SCWindow?
    var owner: pid_t = 0
    var windowID: CGWindowID = 0
    var frame = CGRect.zero
    var displayID: CGDirectDisplayID = CGMainDisplayID()
    var lease = Lease()
    var keys = Set<CGKeyCode>(), buttons = Set<Int>()
    var point = CGPoint.zero
    var watchdog: Timer?
    var watchdogGrace = now()
    var paused = false
    var currentGeneration: UInt64 = 0
    var revokedGeneration: UInt64 = 0
    var mailbox: InputMailbox?
    var announcedCodec = ""
    var lastAlive:TimeInterval = 0
    let stateLock = NSLock()
    var view = false
    var inFlight = false
    var encoder: VTCompressionSession?
    var sequence: UInt32 = 0
    var outputLock = NSLock()
    var ci = CIContext(options: [.cacheIntermediates: false])
    var mode = "h264"
    var quality = 0.7
    var encodeStart: TimeInterval = 0
    var width = 0, height = 0, fps = 30, bitrate = 8

    func start(id: CGWindowID, maxWidth: Int, fps: Int, quality: Int, codec: String, bitrate: Int) async throws {
        self.windowID = id; self.fps = fps; self.quality = Double(quality)/100; self.mode = codec; self.bitrate = bitrate
        let content = try await shareable()
        let filter: SCContentFilter
        if id == 0 {
            guard let display = content.displays.first(where: {$0.displayID == displayID}) else { shutdown("Main Mac display unavailable") }
            frame = CGDisplayBounds(display.displayID)
            filter = SCContentFilter(display: display, excludingApplications: [], exceptingWindows: [])
        } else {
            guard let window = content.windows.first(where: {$0.windowID == id && $0.isOnScreen}) else { shutdown("Selected window closed; desktop fallback is prohibited") }
            selected = window; owner = window.owningApplication?.processID ?? 0
            guard owner != 0 else { shutdown("Selected window has no controllable owner") }
            frame = window.frame
            filter = SCContentFilter(desktopIndependentWindow: window)
        }
        let pixels = Double(filter.pointPixelScale)
        let ratio = min(1.0,min(Double(maxWidth)/max(1,frame.width*pixels),2160/max(1,frame.height*pixels)))
        width = max(2,Int(frame.width*pixels*ratio)/2*2); height = max(2,Int(frame.height*pixels*ratio)/2*2)
        guard height <= 2160, width <= 1920 else { shutdown("Capture dimensions exceed resource limit") }
        let config = SCStreamConfiguration()
        config.width = width; config.height = height
        config.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(fps))
        config.queueDepth = 3; config.showsCursor = true
        config.pixelFormat = codec == "h264" ? kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange : kCVPixelFormatType_32BGRA
        config.capturesAudio = false
        config.ignoreShadowsSingleWindow = true
        if codec == "h264" { setupEncoder() }
        else { jsonStatus(["type":"video","codec":"jpeg","width":width,"height":height]) }
        stream = SCStream(filter: filter, configuration: config, delegate: self)
        try stream!.addStreamOutput(self, type: .screen, sampleHandlerQueue: DispatchQueue(label: "MAC-DOW.capture", qos: .userInteractive))
        try await stream!.startCapture()
        // Input state and watchdog remain serialized on the main queue.
        DispatchQueue.main.async(execute: DispatchWorkItem {
            self.watchdog = Timer.scheduledTimer(timeInterval: 0.1, target: self, selector: #selector(self.tick), userInfo: nil, repeats: true)
            self.readInput()
        })
        status("Capture started; view only until Windows explicitly arms control")
    }

    func setupEncoder() {
        let spec: [CFString:Any] = [kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder: true,
                                  kVTVideoEncoderSpecification_EnableLowLatencyRateControl: true]
        let result = VTCompressionSessionCreate(allocator: kCFAllocatorDefault, width: Int32(width), height: Int32(height), codecType: kCMVideoCodecType_H264, encoderSpecification: spec as CFDictionary, imageBufferAttributes: nil, compressedDataAllocator: nil, outputCallback: { ref, _, result, flags, sample in
            guard let ref else { return }
            let engine = Unmanaged<Engine>.fromOpaque(ref).takeUnretainedValue()
            defer { engine.stateLock.lock(); engine.inFlight = false; engine.stateLock.unlock() }
            guard result == noErr, !flags.contains(.frameDropped), let sample else { return }
            engine.encoded(sample)
        }, refcon: Unmanaged.passUnretained(self).toOpaque(), compressionSessionOut: &encoder)
        guard result == noErr, let encoder else { shutdown("Hardware H.264 unavailable (\(result)). Reconnect with JPEG diagnostic; no silent software fallback.") }
        let properties: [CFString:Any] = [
            kVTCompressionPropertyKey_RealTime: true,
            kVTCompressionPropertyKey_AllowFrameReordering: false,
            kVTCompressionPropertyKey_ProfileLevel: kVTProfileLevel_H264_Baseline_AutoLevel,
            kVTCompressionPropertyKey_ExpectedFrameRate: fps,
            kVTCompressionPropertyKey_MaxKeyFrameInterval: max(1,fps/2),
            kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration: 0.5,
            kVTCompressionPropertyKey_AverageBitRate: bitrate*1_000_000,
            kVTCompressionPropertyKey_MaxFrameDelayCount: 1
        ]
        for (key,value) in properties {
            let code = VTSessionSetProperty(encoder, key: key, value: value as CFTypeRef)
            if code != noErr { status("Encoder property \(key) unsupported (\(code))") }
        }
        let ready = VTCompressionSessionPrepareToEncodeFrames(encoder)
        guard ready == noErr else { shutdown("Hardware encoder preparation failed: \(ready)") }
        // This C API returns a retained CF object through void*. Keep ownership explicit.
        var hardware: Unmanaged<CFTypeRef>?
        let hardwareResult = VTSessionCopyProperty(encoder, key: kVTCompressionPropertyKey_UsingHardwareAcceleratedVideoEncoder, allocator: nil, valueOut: &hardware)
        let hardwareValue = hardware?.takeRetainedValue()
        guard hardwareResult == noErr, (hardwareValue as? Bool) == true else { shutdown("Hardware encoder not active; use explicit JPEG diagnostic if needed") }
        status("VideoToolbox hardware H.264; realtime, no B frames; bounded encoder submissions")
    }
    func shutdown(_ message: String) -> Never {
        if Thread.isMainThread { releaseAll(); status(message,paused:true); exit(1) }
        DispatchQueue.main.sync { self.releaseAll();status(message,paused:true) }
        exit(1)
    }
    func stream(_ stream: SCStream, didStopWithError error: Error) { shutdown("Capture stopped: \(error.localizedDescription)") }
    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen, sampleBuffer.isValid, let image = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        stateLock.lock()
        guard view, !inFlight else { stateLock.unlock(); return }
        inFlight = true; encodeStart = now()
        stateLock.unlock()
        if mode == "h264", let encoder {
            let result = VTCompressionSessionEncodeFrame(encoder, imageBuffer: image, presentationTimeStamp: CMSampleBufferGetPresentationTimeStamp(sampleBuffer), duration: .invalid, frameProperties: nil, sourceFrameRefcon: nil, infoFlagsOut: nil)
            if result != noErr { stateLock.lock(); inFlight=false; stateLock.unlock(); status("Video encode failed: \(result)",paused:true) }
        } else {
            defer { stateLock.lock(); inFlight = false; stateLock.unlock() }
            autoreleasepool {
                let ciImage = CIImage(cvPixelBuffer: image)
                guard let cg = ci.createCGImage(ciImage, from: ciImage.extent) else { return }
                let data = NSMutableData()
                guard let dest = CGImageDestinationCreateWithData(data, UTType.jpeg.identifier as CFString, 1, nil) else { return }
                CGImageDestinationAddImage(dest,cg,[kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
                if CGImageDestinationFinalize(dest) { writePacket(kind:1,data:data as Data) }
            }
        }
    }
    func encoded(_ sample: CMSampleBuffer) {
        guard let format = CMSampleBufferGetFormatDescription(sample), let block = CMSampleBufferGetDataBuffer(sample) else { return }
        let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false) as? [[CFString:Any]]
        let key = !(attachments?.first?[kCMSampleAttachmentKey_NotSync] as? Bool ?? false)
        var data = Data()
        if key {
            var count = 0
            for index in 0..<2 {
                var pointer: UnsafePointer<UInt8>?, size = 0, header: Int32 = 0
                let result = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, parameterSetIndex:index, parameterSetPointerOut:&pointer, parameterSetSizeOut:&size, parameterSetCountOut:&count, nalUnitHeaderLengthOut:&header)
                if result == noErr, let pointer {
                    let parameter = Data(bytes:pointer,count:size)
                    if index == 0 && size >= 4 {
                        let codec = String(format:"avc1.%02X%02X%02X",pointer[1],pointer[2],pointer[3])
                        if announcedCodec != codec { announcedCodec = codec; jsonStatus(["type":"video","codec":codec,"width":width,"height":height,"hardware":true]) }
                    }
                    data.append(contentsOf:[0,0,0,1]);data.append(parameter)
                }
            }
        }
        let size = CMBlockBufferGetDataLength(block)
        var avcc = Data(count:size)
        let copied = avcc.withUnsafeMutableBytes { raw in CMBlockBufferCopyDataBytes(block, atOffset:0, dataLength:size, destination:raw.baseAddress!) }
        guard copied == noErr else { return }
        var offset = 0
        while offset+4 <= avcc.count {
            let length = avcc[offset..<offset+4].reduce(0) { ($0 << 8) | Int($1) };offset += 4
            guard length>0,offset+length<=avcc.count else { return }
            data.append(contentsOf:[0,0,0,1]);data.append(avcc[offset..<offset+length]);offset += length
        }
        writePacket(kind:key ? 2 : 3,data:data)
    }
    func writePacket(kind:UInt8,data:Data) {
        guard data.count+9 <= 4*1024*1024 else { shutdown("Encoded frame exceeds transport limit") }
        outputLock.lock();defer { outputLock.unlock() }
        sequence &+= 1
        var packet = Data([kind]);var seq = sequence.bigEndian
        var elapsed = UInt32(min(4_000_000_000,max(0,(now()-encodeStart)*1_000_000))).bigEndian
        withUnsafeBytes(of:&seq) { packet.append(contentsOf:$0) };withUnsafeBytes(of:&elapsed) { packet.append(contentsOf:$0) };packet.append(data)
        var length = UInt32(packet.count).bigEndian
        var framed = Data();withUnsafeBytes(of:&length) { framed.append(contentsOf:$0) };framed.append(packet)
        do { try FileHandle.standardOutput.write(contentsOf:framed) } catch { shutdown("Viewer transport pipe closed") }
    }
    func readInput() {
        let inbox = InputMailbox(receive: { [weak self] message, received in self?.input(message,received:received) }, failure: { [weak self] in self?.shutdown("Native input mailbox overflow/EOF") })
        mailbox = inbox
        DispatchQueue.global(qos:.userInteractive).async {
            while let line = readLine(strippingNewline:true) {
                let received = now()
                if line.utf8.count>4096 { inbox.fail();return }
                guard let data=line.data(using:.utf8),let message=(try? JSONSerialization.jsonObject(with:data)) as? [String:Any] else { inbox.fail();return }
                inbox.push(message,received:received)
            }
            inbox.fail()
        }
    }
    @objc func tick() {
        if now()-lastAlive>0.5 {lastAlive=now();jsonStatus(["type":"captureAlive"])}
        if lease.active && !lease.valid(now:now()) { revokedGeneration=currentGeneration;releaseAll() }
        if now()-watchdogGrace>2 {
            // Stop capture after a dead viewer, even if parent host was killed.
            shutdown("Viewer heartbeat expired")
        }
        if windowID != 0 {
            guard let info = CGWindowListCopyWindowInfo([.optionIncludingWindow],windowID) as? [[String:Any]], let selected=info.first,
                  (selected[kCGWindowIsOnscreen as String] as? Bool)==true,
                  let bounds=selected[kCGWindowBounds as String] as? [String:Any],
                  let rect=CGRect(dictionaryRepresentation:bounds as CFDictionary) else { shutdown("Selected window closed or minimized; no desktop fallback") }
            if abs(frame.width-rect.width)>2 || abs(frame.height-rect.height)>2 { shutdown("Selected Mac window resized; reconnect to renegotiate capture geometry") }
            frame=rect
        }
        if lease.active && !scopeAllowsInput() { revokedGeneration=currentGeneration;releaseAll();if !paused {status("Control paused: selected app/window is not foremost. Return to the selected Mac window and explicitly rearm.",paused:true);paused=true} }
    }
    func scopeAllowsInput() -> Bool {
        if windowID == 0 { return true }
        guard NSWorkspace.shared.frontmostApplication?.processIdentifier == owner else { return false }
        let app=AXUIElementCreateApplication(owner)
        var focused:CFTypeRef?
        guard AXUIElementCopyAttributeValue(app,kAXFocusedWindowAttribute as CFString,&focused) == .success,
              let focused else { return false }
        let focusedWindow=unsafeBitCast(focused,to:AXUIElement.self)
        var position:CFTypeRef?,size:CFTypeRef?
        guard AXUIElementCopyAttributeValue(focusedWindow,kAXPositionAttribute as CFString,&position) == .success,
              AXUIElementCopyAttributeValue(focusedWindow,kAXSizeAttribute as CFString,&size) == .success,
              let position,let size else { return false }
        var p=CGPoint.zero,s=CGSize.zero
        guard AXValueGetValue(unsafeBitCast(position,to:AXValue.self),.cgPoint,&p),
              AXValueGetValue(unsafeBitCast(size,to:AXValue.self),.cgSize,&s),
              abs(p.x-frame.minX)<3,abs(p.y-frame.minY)<3,abs(s.width-frame.width)<3,abs(s.height-frame.height)<3 else { return false }
        var element:CFTypeRef?,role:CFTypeRef?
        if AXUIElementCopyAttributeValue(app,kAXFocusedUIElementAttribute as CFString,&element) == .success,let element {
            AXUIElementCopyAttributeValue(unsafeBitCast(element,to:AXUIElement.self),kAXRoleAttribute as CFString,&role)
            if let text=role as? String,text.contains("Menu") || text.contains("Sheet") { return false }
        }
        guard let list=CGWindowListCopyWindowInfo([.optionOnScreenOnly,.excludeDesktopElements],kCGNullWindowID) as? [[String:Any]] else { return false }
        // Any owner popup/menu/child above the selected window is excluded from capture: refuse control.
        for window in list where (window[kCGWindowOwnerPID as String] as? Int)==Int(owner) {
            if (window[kCGWindowAlpha as String] as? Double ?? 1) <= 0 { continue }
            return (window[kCGWindowNumber as String] as? UInt32)==windowID
        }
        return false
    }
    func pointerAllowsInput(_ point:CGPoint)->Bool {
        if windowID==0{return true}
        guard frame.contains(point),let list=CGWindowListCopyWindowInfo([.optionOnScreenOnly,.excludeDesktopElements],kCGNullWindowID) as? [[String:Any]] else{return false}
        for window in list {
            guard (window[kCGWindowAlpha as String] as? Double ?? 1)>0,
                  let bounds=window[kCGWindowBounds as String] as? [String:Any],
                  let rect=CGRect(dictionaryRepresentation:bounds as CFDictionary),rect.contains(point) else{continue}
            return (window[kCGWindowNumber as String] as? UInt32)==windowID
        }
        return false
    }
    func revoke(_ message:String) { revokedGeneration=currentGeneration;releaseAll();if !paused{status(message,paused:true);paused=true} }
    func input(_ m:[String:Any],received:TimeInterval) {
        guard let type=m["type"] as? String else { return }
        if type == "release" { revokedGeneration=currentGeneration;releaseAll();return }
        if type == "lease" {
            watchdogGrace=received
            stateLock.lock();view=(m["view"] as? Bool)==true;stateLock.unlock()
            let requested=(m["active"] as? Bool)==true
            let generation=(m["generation"] as? UInt64) ?? 0
            if !requested { revokedGeneration=currentGeneration;releaseAll();return }
            guard generation>0,generation != revokedGeneration,received+0.85>now() else { releaseAll();return }
            if generation != currentGeneration {
                currentGeneration=generation
                if windowID != 0 { NSRunningApplication(processIdentifier:owner)?.activate(options:[]) }
            }
            let allowed=AXIsProcessTrusted() && scopeAllowsInput()
            if !allowed {
                revokedGeneration=currentGeneration;releaseAll()
                if !paused {status("Control paused: Accessibility missing or selected app/window not foremost; explicitly rearm",paused:true);paused=true}
            } else { lease.update(true,now:received);paused=false }
            return
        }
        guard received+0.85>now(),lease.valid(now:now()),scopeAllowsInput(),AXIsProcessTrusted() else { revokedGeneration=currentGeneration;releaseAll();return }
        switch type {
        case "key":
            guard let c=m["code"] as? Int,c>=0,c<=126 else { return };let code=CGKeyCode(c),down=(m["down"] as? Bool)==true
            if down { keys.insert(code) } else { keys.remove(code) }
            reportHeld()
            let event=CGEvent(keyboardEventSource:nil,virtualKey:code,keyDown:down);event?.flags=modifiers();event?.post(tap:.cghidEventTap)
        case "move":
            guard let x=m["x"] as? Double,let y=m["y"] as? Double,x>=0,x<1,y>=0,y<1 else { return }
            point=CGPoint(x:frame.minX+x*frame.width,y:frame.minY+y*frame.height)
            guard pointerAllowsInput(point) else{revoke("Control paused: pointer target is outside the captured window or is occluded");return}
            let type:CGEventType=buttons.contains(0) ? .leftMouseDragged : buttons.contains(2) ? .rightMouseDragged : buttons.contains(1) ? .otherMouseDragged : .mouseMoved
            let button:CGMouseButton=buttons.contains(2) ? .right : buttons.contains(1) ? .center : .left
            let event=CGEvent(mouseEventSource:nil,mouseType:type,mouseCursorPosition:point,mouseButton:button);event?.flags=modifiers();event?.post(tap:.cghidEventTap)
        case "button":
            guard let b=m["button"] as? Int,b>=0,b<=2 else { return };let down=(m["down"] as? Bool)==true
            if down && !pointerAllowsInput(point){revoke("Control paused: excluded window under pointer");return}
            if down { buttons.insert(b) } else { buttons.remove(b) }
            reportHeld();postButton(b,down:down)
        case "scroll":
            guard let d=m["delta"] as? Int,abs(d)<=2400 else { return }
            guard pointerAllowsInput(point) else{revoke("Control paused: excluded scroll target");return}
            let e=CGEvent(scrollWheelEvent2Source:nil,units:.pixel,wheelCount:1,wheel1:Int32(d),wheel2:0,wheel3:0);e?.flags=modifiers();e?.post(tap:.cghidEventTap)
        default:break
        }
    }
    func modifiers()->CGEventFlags {
        var flags=CGEventFlags()
        if keys.contains(55)||keys.contains(54){flags.insert(.maskCommand)}
        if keys.contains(56)||keys.contains(60){flags.insert(.maskShift)}
        if keys.contains(58)||keys.contains(61){flags.insert(.maskAlternate)}
        if keys.contains(59)||keys.contains(62){flags.insert(.maskControl)}
        return flags
    }
    func postButton(_ b:Int,down:Bool) {
        let button:CGMouseButton=b==0 ? .left : b==2 ? .right : .center
        let type:CGEventType=b==0 ? (down ? .leftMouseDown:.leftMouseUp) : b==2 ? (down ? .rightMouseDown:.rightMouseUp) : (down ? .otherMouseDown:.otherMouseUp)
        let event=CGEvent(mouseEventSource:nil,mouseType:type,mouseCursorPosition:point,mouseButton:button);event?.flags=modifiers();event?.post(tap:.cghidEventTap)
    }
    func reportHeld() { jsonStatus(["type":"held","keys":keys.map{Int($0)},"buttons":Array(buttons),"x":point.x,"y":point.y]) }
    func releaseAll() {
        lease.release()
        let held=keys;keys.removeAll()
        for code in held { let e=CGEvent(keyboardEventSource:nil,virtualKey:code,keyDown:false);e?.flags=[];e?.post(tap:.cghidEventTap) }
        let heldButtons=buttons;buttons.removeAll()
        for b in heldButtons { postButton(b,down:false) }
        if !held.isEmpty || !heldButtons.isEmpty { reportHeld() }
    }
}

signal(SIGPIPE,SIG_IGN)
let args=CommandLine.arguments
if args.contains("--release-safe") {
    // Parent supervisor provides only previously acknowledged held inputs; no capture/listener.
    guard AXIsProcessTrusted(),let line=readLine(),line.utf8.count<=4096,let data=line.data(using:.utf8),
          let held=(try? JSONSerialization.jsonObject(with:data)) as? [String:Any] else { exit(2) }
    for c in (held["keys"] as? [Int] ?? []) where c>=0 && c<=126 {
        let e=CGEvent(keyboardEventSource:nil,virtualKey:CGKeyCode(c),keyDown:false);e?.flags=[];e?.post(tap:.cghidEventTap)
    }
    let x=held["x"] as? Double ?? 0,y=held["y"] as? Double ?? 0
    for b in (held["buttons"] as? [Int] ?? []) where b>=0 && b<=2 {
        let type:CGEventType=b==0 ? .leftMouseUp:b==2 ? .rightMouseUp:.otherMouseUp
        let button:CGMouseButton=b==0 ? .left:b==2 ? .right:.center
        CGEvent(mouseEventSource:nil,mouseType:type,mouseCursorPosition:CGPoint(x:x,y:y),mouseButton:button)?.post(tap:.cghidEventTap)
    }
    exit(0)
}
if args.contains("--self-test") {
    var l=Lease();precondition(!l.valid(now:0));l.update(true,now:1);precondition(l.valid(now:1.5));precondition(!l.valid(now:2));l.release();precondition(!l.valid(now:1))
    var delivered=0,failed=false
    let mailbox=InputMailbox(receive:{_,_ in delivered += 1},failure:{failed=true})
    for _ in 0..<32 {mailbox.push(["type":"key","code":55],received:1)}
    mailbox.push(["type":"release"],received:2);mailbox.drain();precondition(delivered==1 && !failed)
    let overflowing=InputMailbox(receive:{_,_ in preconditionFailure("overflow must not deliver input")},failure:{failed=true})
    for _ in 0..<33 {overflowing.push(["type":"key"],received:1)};overflowing.drain();precondition(failed)
    print("PASS native lease expiration/release and bounded/urgent mailbox; no permission checks, capture or input")
    exit(0)
}
if args.contains("--check") { exit(existingPermissions() ? 0 : 2) }
guard existingPermissions() else { die("Grant Screen Recording yourself in System Settings before starting; MAC-DOW does not request it") }
if args.contains("--list") {
    Task {
        do { let c=try await shareable();let rows:[[String:Any]]=c.windows.filter{$0.isOnScreen && $0.frame.width>40 && $0.frame.height>40 && !($0.title ?? "").isEmpty}.prefix(200).map { ["id":$0.windowID,"title":String(($0.title ?? "").prefix(160)),"app":String(($0.owningApplication?.applicationName ?? "Mac app").prefix(100))] };let data=try JSONSerialization.data(withJSONObject:rows);try FileHandle.standardOutput.write(contentsOf:data);exit(0) }
        catch { die("Window enumeration failed: \(error)") }
    }
    RunLoop.main.run()
} else if args.count==8 && args[1]=="--stream",let id=UInt32(args[2]),let width=Int(args[3]),let fps=Int(args[4]),let quality=Int(args[5]),let bitrate=Int(args[7]),
          width>=640,width<=1920,fps>=5,fps<=30,quality>=35,quality<=85,bitrate>=2,bitrate<=20,["h264","jpeg"].contains(args[6]) {
    let engine=Engine()
    Task { do { try await engine.start(id:id,maxWidth:width,fps:fps,quality:quality,codec:args[6],bitrate:bitrate) } catch { die("Capture setup failed: \(error)") } }
    RunLoop.main.run()
} else { die("Usage: MacDowCapture --check | --list | --self-test | --stream WINDOW WIDTH FPS QUALITY h264|jpeg MBPS") }
