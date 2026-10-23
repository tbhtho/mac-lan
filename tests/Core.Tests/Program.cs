using MacDow.Core;

int count = 0;
void Check(bool value, string name) { if (!value) throw new Exception(name); count++; Console.WriteLine("PASS " + name); }
var gate = new ControlGate();
Check(!gate.Arm(), "disconnected cannot arm");
gate.SetContext(true, true, true);
Check(!gate.CanRoute, "connection stays view only");
Check(gate.Arm() && gate.CanRoute, "explicit arm");
int releases = 0; gate.Released += () => releases++;
foreach (var context in new[] { (false,true,true), (true,false,true), (true,true,false) }) {
    gate.SetContext(true,true,true); gate.Arm();
    gate.SetContext(context.Item1,context.Item2,context.Item3);
    Check(!gate.CanRoute, "transition revokes input");
    gate.SetContext(true,true,true); Check(!gate.CanRoute,"return requires rearm");
}
for (int i = 0; i < 100; i++) { gate.Arm(); gate.Disarm(); }
Check(releases >= 103, "repeated switches release every time");
Check(KeyMap.MacCode(0xA4) == 55 && KeyMap.MacCode(0x09) == 48, "Alt Tab maps to Command Tab");
Check(KeyMap.MacCode(0xA5) == 61, "right Alt is Option");
Check(KeyMap.MacCode(0x5B) == null, "Windows key not routed");
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
Console.WriteLine($"{count} checks passed; no windows, hooks, capture, or injected input.");
