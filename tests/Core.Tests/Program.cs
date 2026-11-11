using MacDow.Core;

int count = 0;
void Check(bool value, string name) { if (!value) throw new Exception(name); count++; Console.WriteLine("PASS " + name); }
{
    Check(MacDisplayFit.For(1920,1080,32)==(1920,1048),"a 1080p display with the 32-pixel bar asks the Mac for 1920 x 1048");
    Check(MacDisplayFit.For(1920,1080,2)==(1920,1078)&&MacDisplayFit.For(2560,1440,48)==(2560,1392)&&MacDisplayFit.For(3840,2160,0)==(3840,2160),"the hidden bar, scaled bars and 4K displays map to even sizes");
    Check(MacDisplayFit.For(1921,1081,32)==(1920,1048),"odd sizes round down to even numbers");
    Check(MacDisplayFit.For(800,600,32)==null&&MacDisplayFit.For(5120,1440,32)==null&&MacDisplayFit.For(1920,600,32)==null&&MacDisplayFit.For(1920,2400,0)==null,"displays outside the Mac host limits are not requested");
    string plain=System.Text.Json.JsonSerializer.Serialize(StreamProfile.DefaultHardware.CreateStart(0)),fitted=System.Text.Json.JsonSerializer.Serialize(StreamProfile.DefaultHardware.CreateStart(0,display:(1920,1048))),window=System.Text.Json.JsonSerializer.Serialize(StreamProfile.DefaultHardware.CreateStart(42,display:(1920,1048)));
    Check(!plain.Contains("display")&&fitted.Contains("\"display\":{\"width\":1920,\"height\":1048}")&&!window.Contains("display"),"only a full-desktop start carries the requested Mac display size");
}
var gate = new ControlGate();
{
    var bar=new TabBarAutoHide();
    Check(bar.Revealed&&!bar.Enabled&&!bar.Update(false,false,false,1000),"the default top bar remains visible without auto-hide enabled");
    bar.Configure(true);bar.Update(false,false,false,1000);
    Check(bar.Revealed&&!bar.Update(false,false,false,1599),"auto-hide gives the pointer time to return before hiding");
    Check(bar.Update(false,false,false,1600)&&!bar.Revealed,"the top bar tucks away after the pointer leaves");
    Check(bar.Update(true,false,false,1601)&&bar.Revealed,"touching the monitor edge reveals the full bar immediately");
    bar.Update(false,true,false,5000);
    Check(bar.Revealed&&!bar.Update(false,true,false,9000),"using buttons inside the revealed bar keeps it open");
    bar.Update(false,false,true,10000);bar.Update(false,false,true,20000);
    Check(bar.Revealed,"an open bar menu cannot hide while its controls are in use");
    bar.Update(false,false,false,20001);bar.Update(false,true,false,20500);bar.Update(false,false,false,21000);
    Check(bar.Revealed&&!bar.Update(false,false,false,21599)&&bar.Update(false,false,false,21600),"returning to the bar cancels the old hide timer and a later departure gets a full delay");
    bar.Configure(false);
    Check(bar.Revealed&&!bar.Enabled&&!bar.Update(false,false,false,30000),"disabling auto-hide restores the always-visible bar");
    var restored=new TabBarAutoHide(true);
    Check(!restored.Revealed&&restored.Enabled,"a saved auto-hide preference starts with the bar tucked away");
    Check(restored.Update(true,false,false,0)&&restored.Revealed,"the hidden startup bar remains recoverable from its edge");
}
Check(!gate.Arm(), "disconnected cannot arm");
gate.SetContext(true, true, true);
Check(!gate.CanRoute, "connection stays view only");
Check(gate.Arm() && gate.CanRoute, "explicit arm");
int releases = 0; gate.Released += _ => releases++;
foreach (var context in new[] { (false,true,true), (true,false,true), (true,true,false) }) {
    gate.SetContext(true,true,true); gate.Arm();
    gate.SetContext(context.Item1,context.Item2,context.Item3);
    Check(!gate.CanRoute, "transition revokes input");
    gate.SetContext(true,true,true); Check(!gate.CanRoute,"return requires rearm");
}
for (int i = 0; i < 100; i++) { gate.Arm(); gate.Disarm(); }
Check(releases >= 103, "repeated switches release every time");

var automaticGate=new ControlGate();
automaticGate.SetContext(false,true,true);automaticGate.RequestAutomaticControl();
Check(!automaticGate.TryAutomaticArm(true,true),"selecting Mac waits for the approved session before activating control");
automaticGate.SetContext(true,true,true);
Check(!automaticGate.TryAutomaticArm(null,true)&&!automaticGate.TryAutomaticArm(false,true),"automatic control waits for confirmed Accessibility permission");
Check(!automaticGate.TryAutomaticArm(true,false),"automatic control waits for a fresh presented frame and native heartbeat");
Check(automaticGate.TryAutomaticArm(true,true)&&automaticGate.CanRoute,"selecting Mac activates ready control without a separate desktop click");
Check(!automaticGate.TryAutomaticArm(true,true),"a ready Mac tab does not repeatedly acquire control or create generations");
automaticGate.SetContext(true,true,false);
Check(!automaticGate.CanRoute&&!automaticGate.TryAutomaticArm(true,true),"focus loss immediately releases automatic control");
automaticGate.SetContext(true,true,true);
Check(automaticGate.TryAutomaticArm(true,true),"returning focus to the selected Mac tab automatically resumes ready control");
automaticGate.Disarm("video-stale");
Check(!automaticGate.TryAutomaticArm(true,false)&&automaticGate.TryAutomaticArm(true,true),"automatic control resumes only after stale video recovers");
automaticGate.Disarm("mac-watchdog");
Check(automaticGate.TryAutomaticArm(true,true),"the selected Mac tab resumes control after a native watchdog pause");
automaticGate.Disarm("manual");
Check(!automaticGate.TryAutomaticArm(true,true),"manual Stop control remains stopped across readiness ticks");
automaticGate.RequestAutomaticControl();Check(automaticGate.TryAutomaticArm(true,true),"selecting Mac again explicitly restores automatic control after Stop");
automaticGate.SetContext(true,false,true);
Check(!automaticGate.CanRoute&&!automaticGate.TryAutomaticArm(true,true),"the Windows tab releases input and cancels automatic control");
automaticGate.SetContext(true,true,true);
Check(!automaticGate.TryAutomaticArm(true,true),"an unrelated context update cannot select the Mac tab for the user");
automaticGate.RequestAutomaticControl();automaticGate.TryAutomaticArm(true,true);
automaticGate.SetContext(false,true,true);automaticGate.SetContext(true,true,true);
Check(!automaticGate.TryAutomaticArm(true,true),"a disconnected session cannot automatically revive in a replacement connection");
automaticGate.RequestAutomaticControl();automaticGate.TryAutomaticArm(true,true);automaticGate.Disarm("keyboard-unavailable");
Check(!automaticGate.TryAutomaticArm(true,true),"keyboard acquisition failure does not trigger a hook retry loop");

var reasons=new List<string?>();var reasonGate=new ControlGate();reasonGate.Released+=reasons.Add;
reasonGate.SetContext(true,true,true);reasonGate.Arm();reasons.Clear();
reasonGate.Disarm("video-stale");
Check(reasons.SequenceEqual(new[]{"video-stale"}),"an active video release retains its exact fixed reason");
reasonGate.Disarm("video-stale");
Check(reasons.Count==2&&reasons[1]==null,"unarmed recovery releases contain no diagnostic reason");
reasonGate.SetContext(true,true,true);
Check(!reasonGate.CanRoute,"fresh context never automatically rearms after video release");
foreach(var transition in new[]{(false,true,true,"disconnected"),(true,false,true,"tab-change"),(true,true,false,"foreground-lost")}){
    reasonGate.SetContext(true,true,true);reasonGate.Arm();reasons.Clear();
    reasonGate.SetContext(transition.Item1,transition.Item2,transition.Item3);
    Check(reasons.SequenceEqual(new[]{transition.Item4})&&!reasonGate.CanRoute,"context loss records "+transition.Item4);
}
reasonGate.SetContext(true,true,true);reasonGate.Arm();reasons.Clear();
reasonGate.SetContext(true,true,false,"window-deactivated");
Check(reasons.SequenceEqual(new[]{"window-deactivated"}),"native deactivation retains its distinct release reason");
reasonGate.SetContext(true,true,true);reasonGate.Arm();reasons.Clear();reasonGate.Disarm("untrusted arbitrary text");
Check(reasons.SequenceEqual(new[]{"manual"}),"unknown release reasons cannot enter diagnostics");
Check(ControlReleaseReasons.All.Count==21&&ControlReleaseReasons.All.All(ControlReleaseReasons.IsKnown),"release reasons use one finite allowlist");

Check(VideoFreshness.BlockReason(true,9001,10000,10000,10000,1000)==null,"fresh video and native heartbeat permit control");
Check(VideoFreshness.BlockReason(true,9000,10000,10000,10000,1000)=="video-stale","one second presentation stall remains blocked");
Check(VideoFreshness.BlockReason(true,10000,8500,10000,10000,1000)=="native-heartbeat-stale","one and a half second native heartbeat delay remains blocked");
Check(VideoFreshness.BlockReason(true,5000,10000,5000,10000,1000)==null,"unchanged static desktop remains controllable with live native heartbeat");
Check(VideoFreshness.BlockReason(true,0,10000,10000,10000,1000)=="video-stale","control waits for its first displayed frame");
Check(VideoFreshness.BlockReason(false,10000,10000,10000,10000,1000)=="mac-permission","denied permission always blocks control");
Check(VideoFreshness.BlockReason(true,10000,0,10000,10000,1000)=="native-heartbeat-stale","control waits for its first native heartbeat");
Check(KeyMap.MacCode(0xA4) == 55 && KeyMap.MacCode(0x09) == 48, "Alt Tab maps to Command Tab");
Check(KeyMap.MacCode(0xA5) == 61, "right Alt is Option");
Check(KeyMap.MacCode(0x5B) == 59 && KeyMap.MacCode(0x5C) == 62, "Windows keys map to Control");
Check(KeyMap.MacCode(0xA2) == 55 && KeyMap.MacCode(0xA3) == 54 && KeyMap.MacCode(0x56) == 9, "Ctrl+V is Command+V");
var router = new KeyRouter();
bool Keys(KeyDecision d,params (int,bool)[] expected)=>d.Route==KeyRoute.Sequence&&d.Keys!.SequenceEqual(expected);
foreach(int ctrl in new[]{0xA2,0xA3,0x11})
{
    var shortcut=new KeyRouter();shortcut.Route(ctrl,true,true);
    int command=ctrl==0xA3?54:55;
    Check(Keys(shortcut.Route(0x20,true,true),(command,false),(59,true),(49,true),(49,false),(59,false),(command,true)),"Ctrl+Space sends balanced Mac Control+Space without Command: "+ctrl);
    Check(shortcut.Route(0x20,true,true).Route==KeyRoute.Swallow,"held Ctrl+Space does not repeatedly reopen effects search");
    Check(shortcut.Route(ctrl,false,true)==new KeyDecision(KeyRoute.Remote,command),"Ctrl released first releases restored Command");
    Check(shortcut.Route(0x20,false,true).Route==KeyRoute.Swallow&&!shortcut.OwnsKeys,"effects-search Space release drains physical ownership");
}
{
    var shortcut=new KeyRouter();shortcut.Route(0xA2,true,true);shortcut.Route(0xA3,true,true);
    Check(Keys(shortcut.Route(0x20,true,true),(54,false),(55,false),(59,true),(49,true),(49,false),(59,false),(54,true),(55,true)),"both Ctrl keys are removed before Mac Control+Space");
    shortcut.ReleaseControl();
    Check(shortcut.Route(0x20,false,true).Route==KeyRoute.Swallow&&shortcut.Route(0xA2,false,true).Route==KeyRoute.Swallow&&shortcut.Route(0xA3,false,true).Route==KeyRoute.Swallow&&!shortcut.OwnsKeys,"focus loss drains the effects-search chord");
}
foreach(int commandKey in new[]{0xA4,0x12})
{
    var shortcut=new KeyRouter();
    Check(shortcut.Route(commandKey,true,true).Route==KeyRoute.Remote,"Command-mapped modifier still reaches the Mac: "+commandKey);
    Check(shortcut.Route(0x20,true,true).Route==KeyRoute.Swallow&&shortcut.Route(0x20,true,true).Route==KeyRoute.Swallow,"Command-mapped Space and its repeat cannot open Spotlight: "+commandKey);
    shortcut.Route(commandKey,false,true);
    Check(shortcut.OwnsKeys&&shortcut.Route(0x20,true,true).Route==KeyRoute.Swallow,"Space remains suppressed after the modifier is released first: "+commandKey);
    Check(shortcut.Route(0x20,false,true).Route==KeyRoute.Swallow&&!shortcut.OwnsKeys,"suppressed Space release stays out of Windows and drains key ownership: "+commandKey);
}
{
    bool outsideControl=true;var shortcut=new KeyRouter(key=>outsideControl&&key==0xA2);
    Check(shortcut.Route(0x20,true,true).Route==KeyRoute.Swallow&&shortcut.OwnsKeys,"Ctrl already held when control begins cannot make Space open Spotlight");
    outsideControl=false;
    Check(shortcut.Route(0x20,true,false).Route==KeyRoute.Swallow&&shortcut.Route(0x20,false,false).Route==KeyRoute.Swallow&&!shortcut.OwnsKeys,"focus loss still drains a suppressed Space without leaking repeats or release");
}
{
    var shortcut=new KeyRouter();shortcut.Route(0xA2,true,true);shortcut.Route(0xA0,true,true);
    Check(shortcut.Route(0x20,true,true)==new KeyDecision(KeyRoute.Remote,49),"Ctrl+Shift+Space still reaches the Mac app instead of being blocked with Spotlight");
    Check(shortcut.Route(0x20,false,true)==new KeyDecision(KeyRoute.Remote,49),"Ctrl+Shift+Space keeps its matching remote key release");
    shortcut.Reset();shortcut.Route(0xA5,true,true);
    Check(shortcut.Route(0x20,true,true)==new KeyDecision(KeyRoute.Remote,49),"right Alt+Space remains Option+Space");
    shortcut.Reset();shortcut.Route(0xA2,true,true);
    Check(shortcut.Route(0x43,true,true)==new KeyDecision(KeyRoute.Remote,8),"blocking Spotlight does not change Ctrl+C routing");
}
{
    var shortcut=new KeyRouter();shortcut.Route(0x20,true,true);shortcut.Route(0xA2,true,true);
    Check(shortcut.Route(0x20,true,true).Route==KeyRoute.Swallow,"a Space repeat after pressing Ctrl cannot open Spotlight");
    Check(shortcut.Route(0x20,false,true)==new KeyDecision(KeyRoute.Remote,49),"a Space originally pressed on the Mac still receives its release");
    shortcut.Route(0xA2,false,true);Check(!shortcut.OwnsKeys,"Space-first ordering cannot leave remote keys held");
}
{
    var shortcut=new KeyRouter();shortcut.Route(0x5C,true,true);
    Check(Keys(shortcut.Route(0x5C,false,true),(55,true),(49,true),(49,false),(55,false)),"the right Windows key alone also keeps Spotlight search");
}
Check(router.Route(0x5B,true,true).Route == KeyRoute.Swallow && router.Route(0x5B,true,true).Route == KeyRoute.Swallow, "Windows key press waits to see whether it is a tap");
foreach(int modifier in new[]{0xA0,0xA1,0x10,0xA5})
{
    var outside=new KeyRouter(key=>key==modifier);outside.Route(0xA2,true,true);
    Check(outside.Route(0x20,true,true).Route==KeyRoute.Swallow,"a modifier held before control cannot disguise plain Command+Space on the Mac: "+modifier);
    var local=new KeyRouter();local.Route(modifier,true,false);local.Route(0xA2,true,true);
    Check(local.Route(0x20,true,true).Route==KeyRoute.Swallow,"a locally pressed modifier cannot bypass Spotlight suppression: "+modifier);
    var resumed=new KeyRouter();resumed.Route(modifier,true,true);resumed.ReleaseControl();resumed.Route(0xA2,true,true);
    Check(resumed.Route(0x20,true,true).Route==KeyRoute.Swallow,"a modifier revoked during a pause cannot bypass Spotlight suppression after resuming: "+modifier);
    resumed.Route(0xA2,false,false);resumed.Route(0x20,false,false);resumed.Route(modifier,false,false);
    Check(!resumed.OwnsKeys,"revoked modifier and suppressed Space releases fully drain ownership: "+modifier);
}
{
    var paused=new KeyRouter();paused.Route(0x5B,true,true);paused.ReleaseControl();
    Check(paused.OwnsKeys&&paused.Route(0x5B,false,true).Route==KeyRoute.Swallow&&!paused.OwnsKeys,"a Windows press interrupted by control loss cannot reopen Spotlight after resuming");
}
Check(Keys(router.Route(0x5B,false,true),(55,true),(49,true),(49,false),(55,false)) && !router.OwnsKeys, "Windows key tapped alone opens Spotlight");
router.Route(0x5B,true,true);
Check(Keys(router.Route(0x52,true,true),(59,true),(15,true)), "Windows key with another key becomes Control");
Check(router.Route(0x52,false,true) == new KeyDecision(KeyRoute.Remote,15), "combined key release goes to the Mac");
Check(Keys(router.Route(0x5B,false,true),(59,false)), "Windows key release after a combination releases Control");
router.Route(0x5B,true,true);
Check(router.Route(0x5B,false,false).Route == KeyRoute.Swallow && !router.OwnsKeys, "Windows key release after control ends never reaches the Start menu");
Check(router.Route(0x5B,false,true).Route == KeyRoute.Local, "unowned Windows key release stays local");
Check(router.Route(0x5B,true,false).Route == KeyRoute.Local, "Windows key is local while not controlling");
router.Reset();
Check(router.Route(0x5D,true,true).Route == KeyRoute.Swallow && router.Route(0x2C,true,true).Route == KeyRoute.Swallow, "context menu and Print Screen keys do not leak while controlling");
Check(router.Route(0xAF,true,true).Route == KeyRoute.Local, "volume keys stay with the PC");
router.Route(0xA2,true,true);router.Route(0xA4,true,true);router.Route(0xA0,true,true);
Check(router.Route(0x08,true,true).Route == KeyRoute.Release, "Ctrl+Alt+Shift+Backspace releases control");
Check(router.Route(0xA2,false,false).Route == KeyRoute.Swallow && router.Route(0xA4,false,false).Route == KeyRoute.Swallow, "modifier releases after the escape do not reach Windows");
router.Reset();router.Route(0xA2,true,true);router.Route(0xA4,true,true);
Check(router.Route(0x2E,true,true).Route == KeyRoute.ReleaseLocal, "Ctrl+Alt+Delete stays with Windows and releases control");
{
    // Physical positions: set-1 scan codes to Mac ANSI-position key codes.
    var letters=new (int Scan,int Mac,string Name)[]{(0x1E,0,"A"),(0x1F,1,"S"),(0x20,2,"D"),(0x21,3,"F"),(0x23,4,"H"),(0x22,5,"G"),(0x2C,6,"Z"),(0x2D,7,"X"),(0x2E,8,"C"),(0x2F,9,"V"),(0x30,11,"B"),
        (0x10,12,"Q"),(0x11,13,"W"),(0x12,14,"E"),(0x13,15,"R"),(0x15,16,"Y"),(0x14,17,"T"),(0x18,31,"O"),(0x16,32,"U"),(0x17,34,"I"),(0x19,35,"P"),(0x26,37,"L"),(0x24,38,"J"),(0x25,40,"K"),(0x31,45,"N"),(0x32,46,"M")};
    foreach(var (scan,mac,name) in letters)Check(KeyMap.MacCodeForScan(scan,false)==mac&&KeyMap.IsLayoutKey(scan,false),"letter position maps by scan code: "+name);
    var digits=new[]{18,19,20,21,23,22,26,28,25,29};
    for(int i=0;i<10;i++)Check(KeyMap.MacCodeForScan(0x02+i,false)==digits[i]&&KeyMap.IsLayoutKey(0x02+i,false),"digit row position maps by scan code: "+(i+1)%10);
    var punctuation=new (int Scan,int Mac,string Name)[]{(0x0C,27,"minus"),(0x0D,24,"equals"),(0x1A,33,"left bracket"),(0x1B,30,"right bracket"),(0x27,41,"semicolon"),(0x28,39,"quote"),
        (0x29,50,"grave"),(0x2B,42,"backslash"),(0x33,43,"comma"),(0x34,47,"period"),(0x35,44,"slash"),(0x56,10,"ISO key beside left Shift"),(0x7D,93,"JIS Yen"),(0x73,94,"JIS Ro")};
    foreach(var (scan,mac,name) in punctuation)Check(KeyMap.MacCodeForScan(scan,false)==mac&&KeyMap.IsLayoutKey(scan,false),"punctuation position maps by scan code: "+name);
    var keypad=new (int Scan,bool Extended,int Mac,string Name)[]{(0x52,false,82,"0"),(0x4F,false,83,"1"),(0x50,false,84,"2"),(0x51,false,85,"3"),(0x4B,false,86,"4"),(0x4C,false,87,"5"),(0x4D,false,88,"6"),
        (0x47,false,89,"7"),(0x48,false,91,"8"),(0x49,false,92,"9"),(0x53,false,65,"decimal"),(0x37,false,67,"multiply"),(0x4E,false,69,"plus"),(0x4A,false,78,"minus"),(0x35,true,75,"divide"),(0x1C,true,76,"Enter"),(0x45,true,71,"Num Lock as Clear")};
    foreach(var (scan,ext,mac,name) in keypad)Check(KeyMap.MacCodeForScan(scan,ext)==mac,"keypad scan code maps: "+name);
    var navigation=new (int Scan,int Mac,string Name)[]{(0x48,126,"Up"),(0x50,125,"Down"),(0x4B,123,"Left"),(0x4D,124,"Right"),(0x47,115,"Home"),(0x4F,119,"End"),(0x49,116,"Page Up"),(0x51,121,"Page Down"),(0x52,114,"Insert"),(0x53,117,"Delete")};
    foreach(var (scan,mac,name) in navigation)Check(KeyMap.MacCodeForScan(scan,true)==mac&&!KeyMap.IsLayoutKey(scan,true),"extended navigation scan code maps and keeps its virtual key: "+name);
    Check(KeyMap.MacCodeForScan(0x1C,false)==36&&KeyMap.MacCodeForScan(0x01,false)==53&&KeyMap.MacCodeForScan(0x0E,false)==51&&KeyMap.MacCodeForScan(0x0F,false)==48&&KeyMap.MacCodeForScan(0x39,false)==49,"Return, Escape, Backspace, Tab and Space scan codes map");
    Check(KeyMap.MacCodeForScan(0x3B,false)==122&&KeyMap.MacCodeForScan(0x58,false)==111&&KeyMap.MacCodeForScan(0x67,false)==106&&KeyMap.MacCodeForScan(0x6A,false)==80,"function key scan codes map through F19");
    Check(KeyMap.MacCode(0x7F)==106&&KeyMap.MacCode(0x80)==64&&KeyMap.MacCode(0x81)==79&&KeyMap.MacCode(0x82)==80,"F16 to F19 virtual keys map");
    Check(KeyMap.MacCodeForScan(0x37,true)==null&&KeyMap.MacCodeForScan(0x00,false)==null&&KeyMap.MacCodeForScan(0x21D,false)==null,"Print Screen, missing and synthetic scan codes have no position");
    foreach(int scan in new[]{0x1D,0x2A,0x36,0x38,0x39,0x3A,0x1C,0x0E,0x0F,0x01,0x47,0x4B,0x52,0x53})
        Check(!KeyMap.IsLayoutKey(scan,false),"modifiers, Space, editing keys and the keypad keep virtual-key routing: 0x"+scan.ToString("X"));
    Check(!KeyMap.IsLayoutKey(0x1D,true)&&!KeyMap.IsLayoutKey(0x38,true)&&!KeyMap.IsLayoutKey(0x5B,true),"right Ctrl, right Alt and Windows keys keep virtual-key routing");
    Check(KeyMap.MacCode(0x41,0x1E,false)==0&&KeyMap.MacCode(0x24,0x47,false)==115&&KeyMap.MacCode(0x67,0x47,false)==89&&KeyMap.MacCode(0x0D,0x1C,true)==76&&KeyMap.MacCode(0x0D,0x1C,false)==36,
        "positions for characters, virtual keys for the keypad (Num Lock off Home, on 7) and keypad Enter apart from Return");
    Check(KeyMap.MacCode(0xE7,0x1E,false)==null,"a Unicode packet is not mistaken for a key position");

    // Layouts: the Windows virtual key follows the PC layout, the Mac code follows the key's position.
    var azerty=new KeyRouter();
    Check(azerty.Route(0x51,0x1E,false,true,true)==new KeyDecision(KeyRoute.Remote,0)&&azerty.Route(0x51,0x1E,false,false,true)==new KeyDecision(KeyRoute.Remote,0),"AZERTY A key (VK_Q) sends the Mac A position so the Mac layout types its own letter");
    azerty.Route(0xA2,0x1D,false,true,true);
    Check(azerty.Route(0x5A,0x11,false,true,true)==new KeyDecision(KeyRoute.Remote,13),"AZERTY Ctrl+Z sends Command plus the W position, which a French Mac reads as Z, not Command+W");
    Check(azerty.Route(0x5A,0x11,false,false,true)==new KeyDecision(KeyRoute.Remote,13),"the AZERTY Z release matches its press");
    var qwertz=new KeyRouter();
    Check(qwertz.Route(0x5A,0x15,false,true,true)==new KeyDecision(KeyRoute.Remote,16)&&qwertz.Route(0x59,0x2C,false,true,true)==new KeyDecision(KeyRoute.Remote,6),"QWERTZ Z and Y keep their positions instead of swapping twice");
    Check(qwertz.Route(0xE2,0x56,false,true,true)==new KeyDecision(KeyRoute.Remote,10)&&qwertz.Route(0xE2,0x56,false,false,true)==new KeyDecision(KeyRoute.Remote,10),"the ISO key beside left Shift reaches the Mac instead of being swallowed");
    var keypadEnter=new KeyRouter();
    Check(keypadEnter.Route(0x0D,0x1C,true,true,true)==new KeyDecision(KeyRoute.Remote,76)&&keypadEnter.Route(0x0D,0x1C,false,true,true)==new KeyDecision(KeyRoute.Remote,36),"keypad Enter and Return are separate Mac keys");
    Check(keypadEnter.Route(0x0D,0x1C,true,false,true)==new KeyDecision(KeyRoute.Remote,76)&&keypadEnter.Route(0x0D,0x1C,false,false,true)==new KeyDecision(KeyRoute.Remote,36)&&!keypadEnter.OwnsKeys,"keypad Enter and Return releases stay balanced");
    var layoutChange=new KeyRouter();layoutChange.Route(0x51,0x1E,false,true,true);
    Check(layoutChange.Route(0x41,0x1E,false,false,true)==new KeyDecision(KeyRoute.Remote,0)&&!layoutChange.OwnsKeys,"a layout change while a key is held still releases the same Mac key");
    var shortcuts=new KeyRouter();shortcuts.Route(0xA2,0x1D,false,true,true);
    Check(Keys(shortcuts.Route(0x20,0x39,false,true,true),(55,false),(59,true),(49,true),(49,false),(59,false),(55,true)),"Ctrl+Space keeps its Mac Control+Space tap with scan codes");
    shortcuts.Reset();shortcuts.Route(0x5B,0x5B,true,true,true);
    Check(Keys(shortcuts.Route(0x52,0x13,false,true,true),(59,true),(15,true)),"Windows key with a positional letter still becomes Control");
    shortcuts.Reset();shortcuts.Route(0xA2,0x1D,false,true,true);shortcuts.Route(0xA4,0x38,false,true,true);shortcuts.Route(0xA0,0x2A,false,true,true);
    Check(shortcuts.Route(0x08,0x0E,false,true,true).Route==KeyRoute.Release,"Ctrl+Alt+Shift+Backspace still releases control with scan codes");
    shortcuts.Reset();shortcuts.Route(0xA2,0x1D,false,true,true);shortcuts.Route(0xA4,0x38,false,true,true);
    Check(shortcuts.Route(0x2E,0x53,true,true,true).Route==KeyRoute.ReleaseLocal,"Ctrl+Alt+Delete still releases control with scan codes");
}
{
    // AltGr arrives as a synthetic left Ctrl (scan 0x21D) plus right Alt; it is Option alone on the Mac.
    var altGr=new KeyRouter();
    Check(altGr.Route(0xA2,0x21D,false,true,true).Route==KeyRoute.Swallow,"AltGr's synthetic Ctrl does not reach the Mac as Command");
    Check(altGr.Route(0xA5,0x38,true,true,true)==new KeyDecision(KeyRoute.Remote,61),"AltGr's right Alt is Option");
    Check(altGr.Route(0xA2,0x21D,false,true,true).Route==KeyRoute.Swallow&&altGr.Route(0xA5,0x38,true,true,true)==new KeyDecision(KeyRoute.Remote,61),"AltGr auto-repeat stays Option only");
    Check(altGr.Route(0x45,0x12,false,true,true)==new KeyDecision(KeyRoute.Remote,14),"AltGr+E sends Option+E, not Command+Option+E");
    Check(altGr.Route(0x20,0x39,false,true,true)==new KeyDecision(KeyRoute.Remote,49),"AltGr+Space is Option+Space, not the Ctrl+Space tap");
    altGr.Route(0x20,0x39,false,false,true);altGr.Route(0x45,0x12,false,false,true);
    Check(altGr.Route(0xA2,0x21D,false,false,true).Route==KeyRoute.Swallow&&altGr.Route(0xA5,0x38,true,false,true)==new KeyDecision(KeyRoute.Remote,61)&&!altGr.OwnsKeys,"AltGr releases are balanced");
    var localAltGr=new KeyRouter();
    Check(localAltGr.Route(0xA2,0x21D,false,true,false).Route==KeyRoute.Local&&localAltGr.Route(0xA2,0x21D,false,false,true).Route==KeyRoute.Local,"AltGr pressed for Windows keeps its synthetic Ctrl with Windows");
    var lostAltGr=new KeyRouter();lostAltGr.Route(0xA2,0x21D,false,true,true);lostAltGr.Route(0xA5,0x38,true,true,true);lostAltGr.ReleaseControl();
    Check(lostAltGr.Route(0xA2,0x21D,false,true,false).Route==KeyRoute.Swallow&&lostAltGr.Route(0xA2,0x21D,false,false,false).Route==KeyRoute.Swallow&&lostAltGr.Route(0xA5,0x38,true,false,false).Route==KeyRoute.Swallow&&!lostAltGr.OwnsKeys,
        "AltGr held across a control release never gives Windows a lone Ctrl or Alt");
    var realCtrl=new KeyRouter();realCtrl.Route(0xA2,0x1D,false,true,true);
    Check(realCtrl.Route(0x43,0x2E,false,true,true)==new KeyDecision(KeyRoute.Remote,8),"a physical left Ctrl is still Command");
}
{
    // Lock screen, Ctrl+Alt+Delete and UAC hide key releases from the hook.
    var locked=new KeyRouter();locked.Route(0xA2,0x1D,false,true,true);locked.Route(0xA4,0x38,false,true,true);
    Check(locked.Route(0x2E,0x53,true,true,true).Route==KeyRoute.ReleaseLocal,"Ctrl+Alt+Delete releases control");
    locked.ReleaseControl();
    Check(locked.OwnsKeys,"the released modifiers wait for releases the secure desktop will hide");
    locked.ForgetPhysicalState();
    Check(!locked.OwnsKeys&&locked.Route(0xA2,0x1D,false,true,false).Route==KeyRoute.Local&&locked.Route(0x43,0x2E,false,true,false).Route==KeyRoute.Local,"after a desktop switch, Ctrl+C goes to Windows intact");

    var missed=new KeyRouter();missed.Route(0xA2,0x1D,false,true,true,1000);missed.Route(0xA4,0x38,false,true,true,1010);missed.Route(0x2E,0x53,true,true,true,1020);missed.ReleaseControl();
    Check(missed.Route(0xA2,0x1D,false,true,false,1100).Route==KeyRoute.Local,"a new Ctrl press after a missed release is not swallowed even without a desktop-switch notice");
    Check(missed.Route(0xA2,0x1D,false,false,false,1150).Route==KeyRoute.Local,"its release also goes to Windows, balancing the press");
    Check(missed.Route(0xA4,0x38,false,true,false,1200).Route==KeyRoute.Local&&missed.Route(0xA4,0x38,false,false,false,1250).Route==KeyRoute.Local&&!missed.OwnsKeys,"each stale key heals on its next press");

    var lastKey=new KeyRouter();lastKey.Route(0x5B,0x5B,true,true,true,1000);lastKey.Route(0x4C,0x26,false,true,true,1010);lastKey.ReleaseControl();
    Check(lastKey.Route(0x4C,0x26,false,true,false,1040).Route==KeyRoute.Swallow,"auto-repeat of the last key held across a release stays swallowed");
    Check(lastKey.Route(0x4C,0x26,false,true,false,9000).Route==KeyRoute.Local,"the same key pressed again long after an unseen release (Win+L, unlock) reaches Windows");
    Check(lastKey.Route(0x5B,0x5B,true,true,false,9100).Route==KeyRoute.Local,"the Windows key pressed after unlock reaches Windows");

    var stillHeld=new KeyRouter();stillHeld.Route(0xA2,0x1D,false,true,true,1000);stillHeld.Route(0x43,0x2E,false,true,true,1010);stillHeld.ReleaseControl();
    Check(stillHeld.Route(0x43,0x2E,false,false,false,1100).Route==KeyRoute.Swallow&&stillHeld.Route(0xA2,0x1D,false,false,false,1200).Route==KeyRoute.Swallow&&!stillHeld.OwnsKeys,"keys still physically held across a release keep their swallowed releases");

    bool physicallyDown=true;var outside=new KeyRouter(k=>physicallyDown&&k==0xA2);
    outside.Route(0xA2,0x1D,false,true,false);physicallyDown=false;outside.Route(0xA4,0x38,false,true,true);
    Check(outside.Route(0x2E,0x53,true,true,true)==new KeyDecision(KeyRoute.Remote,117),"a Windows-side Ctrl whose release this desktop missed cannot fake Ctrl+Alt+Delete");
}
{
    // Windows word-editing chords become their Mac Option equivalents.
    foreach(var (vk,scan,mac,name) in new[]{(0x25,0x4B,123,"Ctrl+Left"),(0x27,0x4D,124,"Ctrl+Right"),(0x08,0x0E,51,"Ctrl+Backspace"),(0x2E,0x53,117,"Ctrl+Delete")})
    {
        var edit=new KeyRouter();edit.Route(0xA2,0x1D,false,true,true);
        bool ext=vk is 0x25 or 0x27 or 0x2E;
        Check(Keys(edit.Route(vk,scan,ext,true,true),(55,false),(58,true),(mac,true),(mac,false),(58,false),(55,true)),name+" sends Option instead of Command");
        Check(Keys(edit.Route(vk,scan,ext,true,true),(55,false),(58,true),(mac,true),(mac,false),(58,false),(55,true)),name+" auto-repeat keeps editing word by word");
        Check(edit.Route(vk,scan,ext,false,true).Route==KeyRoute.Swallow,name+" release stays balanced");
        Check(edit.Route(0xA2,0x1D,false,false,true)==new KeyDecision(KeyRoute.Remote,55)&&!edit.OwnsKeys,name+" leaves no key held");
    }
    var select=new KeyRouter();select.Route(0xA2,0x1D,false,true,true);select.Route(0xA0,0x2A,false,true,true);
    Check(Keys(select.Route(0x27,0x4D,true,true,true),(55,false),(58,true),(124,true),(124,false),(58,false),(55,true)),"Ctrl+Shift+Right selects by word with Shift still held on the Mac");
    var lineEnd=new KeyRouter();lineEnd.Route(0xA4,0x38,false,true,true);
    Check(lineEnd.Route(0x27,0x4D,true,true,true)==new KeyDecision(KeyRoute.Remote,124),"left Alt+Right still sends Command+Right for the line end");
    var spaces=new KeyRouter();spaces.Route(0x5B,0x5B,true,true,true);
    Check(Keys(spaces.Route(0x27,0x4D,true,true,true),(59,true),(124,true)),"Windows key+Right remains Control+Right");
    var plain=new KeyRouter();
    Check(plain.Route(0x24,0x47,true,true,true)==new KeyDecision(KeyRoute.Remote,115)&&plain.Route(0x25,0x4B,true,true,true)==new KeyDecision(KeyRoute.Remote,123),"plain Home and arrows are unchanged");
    var off=new KeyRouter{WindowsEditingKeys=false};off.Route(0xA2,0x1D,false,true,true);
    Check(off.Route(0x08,0x0E,false,true,true)==new KeyDecision(KeyRoute.Remote,51),"editing translation can be turned off");
    var paused=new KeyRouter();paused.Route(0xA2,0x1D,false,true,true);paused.Route(0x08,0x0E,false,true,true);paused.ReleaseControl();
    Check(paused.Route(0x08,0x0E,false,true,false).Route==KeyRoute.Swallow&&paused.Route(0x08,0x0E,false,false,false).Route==KeyRoute.Swallow,"a translated key held across a release stays away from Windows");
    var letGo=new KeyRouter();letGo.Route(0xA2,0x1D,false,true,true);letGo.Route(0x25,0x4B,true,true,true);letGo.Route(0xA2,0x1D,false,false,true);
    Check(letGo.Route(0x25,0x4B,true,true,true).Route==KeyRoute.Swallow&&letGo.Route(0x25,0x4B,true,false,true).Route==KeyRoute.Swallow&&!letGo.OwnsKeys,"releasing Ctrl first stops the word moves without sending a stray arrow");
}
Check(Viewport.Map(100,0,400,300,1920,1080) == null, "letterbox rejected");
var p = Viewport.Map(200,150,400,300,1920,1080);
Check(p != null && Math.Abs(p.Value.X-.5) < .001 && Math.Abs(p.Value.Y-.5)<.001,"aspect fit coordinates");
var primary = new MonitorArea("main",0,0,1920,1040,96,true);
var secondary = new MonitorArea("second",-2560,0,2560,1400,144,false);
Check(DisplayLayout.Select(new[]{primary,secondary}).Id=="second","second monitor selection");
Check(DisplayLayout.Select(new[]{primary}).Id=="main","single monitor fallback");
Check(DisplayLayout.Select(new[]{primary,secondary},"main").Id=="main","explicit monitor choice");
Check(DisplayLayout.Select(new[]{primary},"second").Id=="main","disconnect fallback");
Check(DisplayLayout.Select(new[]{primary,secondary},"second").Id=="second","reconnect identity");
var horizontal=DisplayLayout.Choices(new[]{primary,secondary});
Check(horizontal.Select(c=>c.Label).SequenceEqual(new[]{"Left","Right"})&&horizontal[0].Monitor.Id=="second","display choices use physical left/right order with negative origins and mixed DPI");
var above=primary with{Id="above",Top=-1440,Width=2560,Height=1440,Primary=false};
var vertical=DisplayLayout.Choices(new[]{primary,above});
Check(vertical[0].Monitor.Id=="above"&&vertical.Select(c=>c.Label).SequenceEqual(new[]{"Top","Bottom"}),"stacked displays use physical top/bottom labels");
Check(DisplayLayout.Choices(new[]{primary}).Single().Label=="Display 1 (main)","one display has a useful name without a directional promise");
var multiple=DisplayLayout.Choices(new[]{primary,secondary,above});
Check(multiple.Count==3&&multiple.Select(c=>c.Label).Distinct().Count()==3&&multiple.Select(c=>c.Monitor.Id).Order().SequenceEqual(new[]{"main","second","above"}.Order()),"three displays all remain selectable with unique labels");
Check(DisplayLayout.Choices(new[]{above,secondary,primary}).SequenceEqual(multiple),"display choice order is independent of enumeration order");
Check(Math.Abs(secondary.DipWidth-1706.6667)<.01,"DPI width");
Check(secondary.Left == -2560,"negative origin preserved");
Check(Pairing.Validate("192.168.1.2", "4433", new string('a',64),new string('b',64)),"valid pairing");
Check(!Pairing.Validate("example.com","4433",new string('a',64),new string('b',64)),"DNS rejected");
Check(!Pairing.Validate("8.8.8.8","4433",new string('a',64),new string('b',64)),"public peer rejected");
Check(!Pairing.Validate("127.0.0.1","4433",new string('a',64),new string('b',64)),"loopback rejected");

Check(Pairing.IdentityCode(new string('a',64))=="AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA","pair identity is 128 bits, grouped for comparison");
Check(DiscoveredHost.TryCreate("Mac","192.168.1.2",4433,new string('a',64),out var host)&&host!=null,"valid discovered LAN host");
Check(!DiscoveredHost.TryCreate("Mac","8.8.8.8",4433,new string('a',64),out _),"discovered public endpoint refused");
Check(!DiscoveredHost.TryCreate("Mac\nforged","192.168.1.2",4433,new string('a',64),out _),"discovered control characters refused");
Check(!DiscoveredHost.TryCreate("Mac","192.168.1.2",80,new string('a',64),out _),"discovered privileged endpoint refused");
Check(!DiscoveredHost.TryCreate("Mac","192.168.1.2",4433,"bad",out _),"discovered malformed fingerprint refused");
Check(TabBarLayout.HeightPixels(96)==32&&TabBarLayout.HeightPixels(144)==48,"tab reservation scales with monitor DPI");
Console.WriteLine($"{count} checks passed; no windows, hooks, capture, or injected input.");
