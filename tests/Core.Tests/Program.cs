using MacDow.Core;

int count = 0;
void Check(bool value, string name) { if (!value) throw new Exception(name); count++; Console.WriteLine("PASS " + name); }
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
foreach(int commandKey in new[]{0xA2,0xA3,0x11,0xA4,0x12})
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
