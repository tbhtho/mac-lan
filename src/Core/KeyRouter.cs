namespace MacDow.Core;

public enum KeyRoute { Local, Remote, Swallow, Release, ReleaseLocal, Sequence }
public readonly record struct KeyDecision(KeyRoute Route,int MacCode=-1,(int Code,bool Down)[]? Keys=null);

// Decides where each low-level Windows event goes while the Mac may own input.
// A key whose press reached the Mac keeps its release away from Windows, so the shell
// never sees a lone Windows key or Alt release and opens Start or a menu behind the stream.
public sealed class KeyRouter
{
    // Keys are tracked by virtual key, except character keys, which are tracked by their
    // physical position (PositionKey + extended scan code) because that is what the Mac receives.
    const int PositionKey=0x1000;
    // Windows sends a synthetic left Ctrl with this scan code ahead of AltGr on European layouts.
    const int AltGrControlScan=0x21D;
    // Auto-repeat of a held key arrives well within this time, even at the slowest repeat delay.
    const uint RepeatWindowMs=1500;
    readonly HashSet<int> held=new(),remote=new(),suppressed=new(),translated=new(),local=new();
    readonly Dictionary<int,int> codes=new();
    readonly Dictionary<int,uint> lastSeen=new();
    readonly Func<int,bool>? heldOutside;
    // A Windows key tapped alone opens Spotlight, like Start opens search; held with
    // another key it is Control. Its press is held back until we know which.
    int? windowsKey;
    bool windowsCombined,altGrControl;
    int? lastDown;
    static readonly (int,bool)[] Spotlight={(55,true),(49,true),(49,false),(55,false)};
    public KeyRouter(Func<int,bool>? heldOutside=null){this.heldOutside=heldOutside;}
    // Translate Windows text-editing chords (Ctrl+Left/Right/Backspace/Delete) to their Mac equivalents.
    public bool WindowsEditingKeys{get;set;}=true;
    public void Reset()
    {
        held.Clear();remote.Clear();suppressed.Clear();translated.Clear();local.Clear();codes.Clear();lastSeen.Clear();
        windowsKey=null;windowsCombined=false;altGrControl=false;lastDown=null;
    }
    // The secure desktop (lock screen, Ctrl+Alt+Delete, UAC) or a session switch hides key
    // releases from this desktop. Nothing we believed held can be trusted afterwards; forget it
    // all so no later Windows keystroke is swallowed. The Mac releases its keys on control release.
    public void ForgetPhysicalState()=>Reset();
    public bool OwnsKeys=>remote.Count>0||suppressed.Count>0||translated.Count>0||altGrControl;
    public void ReleaseControl()
    {
        // The native release clears these keys. Keep their physical ownership
        // only to swallow repeats/releases until the user lets go.
        suppressed.UnionWith(remote);suppressed.UnionWith(translated);remote.Clear();translated.Clear();codes.Clear();windowsKey=null;windowsCombined=false;
    }
    bool Held(params int[] keys)=>keys.Any(k=>held.Contains(k)||heldOutside?.Invoke(k)==true);
    bool Ctrl=>Held(0xA2,0xA3,0x11);
    bool Alt=>Held(0xA4,0xA5,0x12);
    bool Shift=>Held(0xA0,0xA1,0x10);
    bool CommandHeld=>Ctrl||Held(0xA4)||held.Contains(0x12);
    bool RemoteShift=>remote.Contains(0xA0)||remote.Contains(0xA1)||remote.Contains(0x10);
    int[] OwnedCommands()=>remote.Where(k=>k is 0xA2 or 0xA3 or 0x11).Select(k=>KeyMap.MacCode(k)!.Value).Distinct().OrderBy(k=>k).ToArray();
    // Replace the owned Command keys with another modifier for one tap, then restore them, so
    // Ctrl keeps meaning Command for the next shortcut while it stays held.
    static (int,bool)[] Tap(int[] commands,int modifier,int key)
    {
        var keys=commands.Select(k=>(k,false)).ToList();
        keys.AddRange(new[]{(modifier,true),(key,true),(key,false),(modifier,false)});
        keys.AddRange(commands.Select(k=>(k,true)));
        return keys.ToArray();
    }

    // Virtual-key-only form, used where no scan code is known.
    public KeyDecision Route(int vk,bool down,bool allowed)=>Route(vk,0,false,down,allowed,null);

    // scan is KBDLLHOOKSTRUCT.scanCode, extended its LLKHF_EXTENDED flag and time its timestamp in ms.
    public KeyDecision Route(int vk,int scan,bool extended,bool down,bool allowed,uint? time=null)
    {
        // AltGr is right Alt on the Mac (Option). Its synthetic left Ctrl must not add Command.
        if(vk==0xA2&&scan==AltGrControlScan)
        {
            if(altGrControl){if(!down)altGrControl=false;return new(KeyRoute.Swallow);}
            if(down&&allowed){altGrControl=true;return new(KeyRoute.Swallow);}
            return new(KeyRoute.Local);
        }
        bool layoutKey=vk!=0xE7&&KeyMap.IsLayoutKey(scan,extended);
        int id=layoutKey?PositionKey|scan|(extended?0x100:0):vk;
        int? mapped=layoutKey?KeyMap.MacCodeForScan(scan,extended):KeyMap.MacCode(vk);
        // Keys Windows saw are in its async key state; drop any whose release this desktop missed.
        if(heldOutside!=null)foreach(int key in local.Where(k=>k<0x100&&!heldOutside(k)).ToArray()){local.Remove(key);held.Remove(key);}
        // A fresh press of a key we still believe is down means its release happened where this
        // hook could not see it (secure desktop). Auto-repeat only repeats the last key pressed,
        // and does so promptly, so anything else is a new press and must not stay swallowed.
        if(down&&suppressed.Contains(id)&&(lastDown!=id||time is uint now&&lastSeen.TryGetValue(id,out uint seen)&&now-seen>RepeatWindowMs))
            suppressed.Remove(id);
        if(time is uint stamp)lastSeen[id]=stamp;
        if(down){held.Add(id);lastDown=id;}else{held.Remove(id);local.Remove(id);lastSeen.Remove(id);}
        var decision=Decide(vk,id,mapped,down,allowed);
        if(down&&decision.Route is KeyRoute.Local or KeyRoute.ReleaseLocal)local.Add(id);
        return decision;
    }

    KeyDecision Decide(int vk,int id,int? mapped,bool down,bool allowed)
    {
        // A suppressed press owns its repeats/release even if Ctrl is released
        // first or control is lost, so neither desktop sees an unmatched event.
        if(suppressed.Contains(id)){if(!down)suppressed.Remove(id);return new(KeyRoute.Swallow);}
        if(!down&&windowsKey==id)
        {
            remote.Remove(id);windowsKey=null;bool combined=windowsCombined;windowsCombined=false;
            if(!allowed)return new(KeyRoute.Swallow);
            return new(KeyRoute.Sequence,Keys:combined?new[]{(KeyMap.MacCode(vk)!.Value,false)}:Spotlight);
        }
        if(translated.Contains(id))
        {
            if(!down){translated.Remove(id);return new(KeyRoute.Swallow);}
            // Repeats keep translating while the chord is still held, so holding Ctrl+Backspace deletes word after word.
            return allowed&&EditingChord(id)&&mapped is int repeat?new(KeyRoute.Sequence,Keys:Tap(OwnedCommands(),58,repeat)):new(KeyRoute.Swallow);
        }
        if(!down&&remote.Remove(id))
        {
            int? sent=codes.Remove(id,out int code)?code:mapped;
            return allowed&&sent is int release?new(KeyRoute.Remote,release):new(KeyRoute.Swallow);
        }
        // A release whose press Windows saw belongs to Windows, or the key would stay stuck down there.
        if(!allowed||!down&&mapped!=null)return new(KeyRoute.Local);
        if(vk==0x08&&Ctrl&&Alt&&Shift)return new(KeyRoute.Release);
        // Ctrl+Alt+Delete belongs to Windows and cannot be intercepted; give control back with it.
        if(vk==0x2E&&Ctrl&&Alt)return new(KeyRoute.ReleaseLocal);
        // Ctrl normally means Command, but plain Ctrl+Space is the Mac's
        // Control+Space (for example, FX Console). Send one balanced tap and
        // restore the Command keys so Ctrl+C/V still work while Ctrl stays held.
        // Only modifiers we own may be temporarily released and restored.
        if(vk==0x20&&Ctrl&&!Shift&&!Alt&&windowsKey==null&&!remote.Contains(id)&&
            remote.Any(k=>k is 0xA2 or 0xA3 or 0x11))
        {
            suppressed.Add(id);
            return new(KeyRoute.Sequence,Keys:Tap(OwnedCommands(),59,49));
        }
        // Block remaining plain Command+Space chords (left Alt, or Ctrl whose
        // press we did not own) so only a Windows-key tap opens Spotlight.
        // Preserve modified app shortcuts such as
        // Ctrl+Shift+Space and any Space press the Mac already received.
        if(vk==0x20&&CommandHeld&&!RemoteShift&&!remote.Contains(0xA5)&&windowsKey==null)
        {if(!remote.Contains(id))suppressed.Add(id);return new(KeyRoute.Swallow);}
        // Windows word editing: Ctrl+Left/Right moves by word and Ctrl+Backspace/Delete deletes a
        // word. On the Mac those are Option chords; Command would jump to the line start or end,
        // delete the whole line, or move a Finder item to the Trash.
        if(down&&!remote.Contains(id)&&EditingChord(id)&&mapped is int edit)
        {
            translated.Add(id);
            return new(KeyRoute.Sequence,Keys:Tap(OwnedCommands(),58,edit));
        }
        if(down&&vk is 0x5B or 0x5C)
        {
            if(windowsKey==null){windowsKey=id;windowsCombined=false;remote.Add(id);}
            return new(KeyRoute.Swallow);
        }
        if(mapped is int mac)
        {
            if(down){remote.Add(id);codes[id]=mac;}
            if(down&&windowsKey is int windows&&!windowsCombined)
            {windowsCombined=true;return new(KeyRoute.Sequence,Keys:new[]{(KeyMap.MacCode(windows)!.Value,true),(mac,true)});}
            return new(KeyRoute.Remote,mac);
        }
        // Volume, media and browser keys stay with this PC; every other unmapped key is
        // held back so it cannot reach Windows shortcuts while the Mac is in control.
        return vk is >=0xA6 and <=0xB7?new(KeyRoute.Local):new(KeyRoute.Swallow);
    }

    // Plain Ctrl (owned as Command) with Left, Right, Backspace or Delete; Shift may extend a selection.
    // Left Alt+arrow still sends Command+arrow for line start and end.
    bool EditingChord(int id)=>WindowsEditingKeys&&id is 0x25 or 0x27 or 0x08 or 0x2E&&Ctrl&&!Alt&&windowsKey==null&&
        remote.Any(k=>k is 0xA2 or 0xA3 or 0x11);
}
