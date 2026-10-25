namespace MacDow.Core;

public enum KeyRoute { Local, Remote, Swallow, Release, ReleaseLocal, Sequence }
public readonly record struct KeyDecision(KeyRoute Route,int MacCode=-1,(int Code,bool Down)[]? Keys=null);

// Decides where each low-level Windows key event goes while the Mac may own input.
// A key whose press reached the Mac keeps its release away from Windows, so the shell
// never sees a lone Windows key or Alt release and opens Start or a menu behind the stream.
public sealed class KeyRouter
{
    readonly HashSet<int> held=new(),remote=new(),suppressed=new();
    readonly Func<int,bool>? heldOutside;
    // A Windows key tapped alone opens Spotlight, like Start opens search; held with
    // another key it is Control. Its press is held back until we know which.
    int? windowsKey;
    bool windowsCombined;
    static readonly (int,bool)[] Spotlight={(55,true),(49,true),(49,false),(55,false)};
    public KeyRouter(Func<int,bool>? heldOutside=null){this.heldOutside=heldOutside;}
    public void Reset(){held.Clear();remote.Clear();suppressed.Clear();windowsKey=null;windowsCombined=false;}
    public bool OwnsKeys=>remote.Count>0||suppressed.Count>0;
    public void ReleaseControl()
    {
        // The native release clears these keys. Keep their physical ownership
        // only to swallow repeats/releases until the user lets go.
        suppressed.UnionWith(remote);remote.Clear();windowsKey=null;windowsCombined=false;
    }
    bool Held(params int[] keys)=>keys.Any(k=>held.Contains(k)||heldOutside?.Invoke(k)==true);
    bool Ctrl=>Held(0xA2,0xA3,0x11);
    bool Alt=>Held(0xA4,0xA5,0x12);
    bool Shift=>Held(0xA0,0xA1,0x10);
    bool CommandHeld=>Ctrl||Held(0xA4)||held.Contains(0x12);
    bool RemoteShift=>remote.Contains(0xA0)||remote.Contains(0xA1)||remote.Contains(0x10);
    public KeyDecision Route(int vk,bool down,bool allowed)
    {
        if(down)held.Add(vk);else held.Remove(vk);
        // A suppressed press owns its repeats/release even if Ctrl is released
        // first or control is lost, so neither desktop sees an unmatched event.
        if(suppressed.Contains(vk)){if(!down)suppressed.Remove(vk);return new(KeyRoute.Swallow);}
        if(!down&&windowsKey==vk)
        {
            remote.Remove(vk);windowsKey=null;bool combined=windowsCombined;windowsCombined=false;
            if(!allowed)return new(KeyRoute.Swallow);
            return new(KeyRoute.Sequence,Keys:combined?new[]{(KeyMap.MacCode(vk)!.Value,false)}:Spotlight);
        }
        if(!down&&remote.Remove(vk))return allowed&&KeyMap.MacCode(vk) is int code?new(KeyRoute.Remote,code):new(KeyRoute.Swallow);
        // A release whose press Windows saw belongs to Windows, or the key would stay stuck down there.
        if(!allowed||!down&&KeyMap.MacCode(vk)!=null)return new(KeyRoute.Local);
        if(vk==0x08&&Ctrl&&Alt&&Shift)return new(KeyRoute.Release);
        // Ctrl+Alt+Delete belongs to Windows and cannot be intercepted; give control back with it.
        if(vk==0x2E&&Ctrl&&Alt)return new(KeyRoute.ReleaseLocal);
        // Ctrl and left Alt normally map to Command. Do not let their plain
        // Space chord accidentally invoke Spotlight; only a Windows-key tap
        // synthesizes that shortcut. Preserve modified app shortcuts such as
        // Ctrl+Shift+Space and any Space press the Mac already received.
        if(vk==0x20&&CommandHeld&&!RemoteShift&&!remote.Contains(0xA5)&&windowsKey==null)
        {if(!remote.Contains(vk))suppressed.Add(vk);return new(KeyRoute.Swallow);}
        if(down&&vk is 0x5B or 0x5C)
        {
            if(windowsKey==null){windowsKey=vk;windowsCombined=false;remote.Add(vk);}
            return new(KeyRoute.Swallow);
        }
        if(KeyMap.MacCode(vk) is int mac)
        {
            if(down)remote.Add(vk);
            if(down&&windowsKey is int windows&&!windowsCombined)
            {windowsCombined=true;return new(KeyRoute.Sequence,Keys:new[]{(KeyMap.MacCode(windows)!.Value,true),(mac,true)});}
            return new(KeyRoute.Remote,mac);
        }
        // Volume, media and browser keys stay with this PC; every other unmapped key is
        // held back so it cannot reach Windows shortcuts while the Mac is in control.
        return vk is >=0xA6 and <=0xB7?new(KeyRoute.Local):new(KeyRoute.Swallow);
    }
}
