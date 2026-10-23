namespace MacDow.Core;

public enum KeyRoute { Local, Remote, Swallow, Release, ReleaseLocal }
public readonly record struct KeyDecision(KeyRoute Route,int MacCode=-1);

// Decides where each low-level Windows key event goes while the Mac may own input.
// A key whose press reached the Mac keeps its release away from Windows, so the shell
// never sees a lone Windows key or Alt release and opens Start or a menu behind the stream.
public sealed class KeyRouter
{
    readonly HashSet<int> held=new(),remote=new();
    readonly Func<int,bool>? heldOutside;
    public KeyRouter(Func<int,bool>? heldOutside=null){this.heldOutside=heldOutside;}
    public void Reset(){held.Clear();remote.Clear();}
    public bool OwnsKeys=>remote.Count>0;
    bool Held(params int[] keys)=>keys.Any(k=>held.Contains(k)||heldOutside?.Invoke(k)==true);
    bool Ctrl=>Held(0xA2,0xA3,0x11);
    bool Alt=>Held(0xA4,0xA5,0x12);
    bool Shift=>Held(0xA0,0xA1,0x10);
    public KeyDecision Route(int vk,bool down,bool allowed)
    {
        if(down)held.Add(vk);else held.Remove(vk);
        if(!down&&remote.Remove(vk))return allowed&&KeyMap.MacCode(vk) is int code?new(KeyRoute.Remote,code):new(KeyRoute.Swallow);
        // A release whose press Windows saw belongs to Windows, or the key would stay stuck down there.
        if(!allowed||!down&&KeyMap.MacCode(vk)!=null)return new(KeyRoute.Local);
        if(vk==0x08&&Ctrl&&Alt&&Shift)return new(KeyRoute.Release);
        // Ctrl+Alt+Delete belongs to Windows and cannot be intercepted; give control back with it.
        if(vk==0x2E&&Ctrl&&Alt)return new(KeyRoute.ReleaseLocal);
        if(KeyMap.MacCode(vk) is int mac){if(down)remote.Add(vk);return new(KeyRoute.Remote,mac);}
        // Volume, media and browser keys stay with this PC; every other unmapped key is
        // held back so it cannot reach Windows shortcuts while the Mac is in control.
        return vk is >=0xA6 and <=0xB7?new(KeyRoute.Local):new(KeyRoute.Swallow);
    }
}
