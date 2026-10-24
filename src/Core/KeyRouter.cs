namespace MacDow.Core;

public enum KeyRoute { Local, Remote, Swallow, Release, ReleaseLocal, Sequence }
public readonly record struct KeyDecision(KeyRoute Route,int MacCode=-1,(int Code,bool Down)[]? Keys=null);

// Decides where each low-level Windows key event goes while the Mac may own input.
// A key whose press reached the Mac keeps its release away from Windows, so the shell
// never sees a lone Windows key or Alt release and opens Start or a menu behind the stream.
public sealed class KeyRouter
{
    readonly HashSet<int> held=new(),remote=new();
    readonly Func<int,bool>? heldOutside;
    // A Windows key tapped alone opens Spotlight, like Start opens search; held with
    // another key it is Option. Its press is held back until we know which.
    int? windowsKey;
    bool windowsCombined;
    static readonly (int,bool)[] Spotlight={(55,true),(49,true),(49,false),(55,false)};
    public KeyRouter(Func<int,bool>? heldOutside=null){this.heldOutside=heldOutside;}
    public void Reset(){held.Clear();remote.Clear();windowsKey=null;windowsCombined=false;}
    public bool OwnsKeys=>remote.Count>0;
    bool Held(params int[] keys)=>keys.Any(k=>held.Contains(k)||heldOutside?.Invoke(k)==true);
    bool Ctrl=>Held(0xA2,0xA3,0x11);
    bool Alt=>Held(0xA4,0xA5,0x12);
    bool Shift=>Held(0xA0,0xA1,0x10);
    public KeyDecision Route(int vk,bool down,bool allowed)
    {
        if(down)held.Add(vk);else held.Remove(vk);
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
