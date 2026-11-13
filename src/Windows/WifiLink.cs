using System.Runtime.InteropServices;
using System.Security.Cryptography;
namespace MacDow.Windows;

// How well this PC's Wi-Fi is connected, read from Windows a few times a minute while the Mac
// is being viewed. A home network with more than one access point can move a PC to a distant
// one; the picture then stutters although nothing in mac-lan changed. The bar says so when the
// signal is weak, and the local diagnostic log keeps the signal, the link rate and a short
// fingerprint of the access point (never its address), so a change of access point shows up.
// Reading uses the documented WLAN API, needs no administrator rights and changes nothing.
internal sealed class WifiLink
{
    [DllImport("wlanapi.dll")] static extern uint WlanOpenHandle(uint version,nint reserved,out uint negotiated,out nint handle);
    [DllImport("wlanapi.dll")] static extern uint WlanCloseHandle(nint handle,nint reserved);
    [DllImport("wlanapi.dll")] static extern uint WlanEnumInterfaces(nint handle,nint reserved,out nint list);
    [DllImport("wlanapi.dll")] static extern void WlanFreeMemory(nint memory);
    [DllImport("wlanapi.dll")] static extern uint WlanQueryInterface(nint handle,ref Guid adapter,int opcode,nint reserved,out uint size,out nint data,out int type);
    const int CurrentConnection=7;
    // WLAN_INTERFACE_INFO_LIST: two counters, then per adapter a GUID, a 256-character name and a state.
    const int ListHeader=8,AdapterSize=16+512+4,Connected=1;
    // WLAN_CONNECTION_ATTRIBUTES: state, mode and a 256-character profile name, then the association.
    const int Association=4+4+512,Bssid=Association+40,Signal=Association+56,RxRate=Association+60,TxRate=Association+64,AttributesSize=Association+68;
    internal const int WeakSignal=50;
    internal readonly record struct State(int Signal,int RxMbps,int TxMbps,string AccessPoint);
    readonly Action<string,object?> report;
    readonly object sync=new();
    State? current,reported;
    long lastReport;
    bool reading,unavailable;
    internal WifiLink(Action<string,object?> report){this.report=report;}
    /// <summary>The newest reading, or null on a wired PC or before the first one.</summary>
    internal State? Current{get{lock(sync)return current;}}
    /// <summary>Text for the bar while the signal is weak; null otherwise.</summary>
    internal string? WeakNote=>Current is State link&&Weak(link.Signal)?$"Weak Wi-Fi on this PC ({link.Signal}%)":null;
    internal static bool Weak(int signal)=>signal is >=0 and <=WeakSignal;
    // Called from the UI thread; the WLAN service is only spoken to off that thread.
    internal void Refresh()
    {
        lock(sync){if(reading||unavailable)return;reading=true;}
        ThreadPool.QueueUserWorkItem(_=>
        {
            State? link=null;bool failed=false;
            try{link=Read();}
            catch(Exception ex)when(ex is DllNotFoundException or EntryPointNotFoundException){failed=true;}
            bool changed;
            lock(sync)
            {
                current=link;reading=false;unavailable=failed;
                // Record a change of access point or a move across the weak mark at once, and
                // otherwise once a minute.
                changed=link is State now&&(reported is not State before||before.AccessPoint!=now.AccessPoint||Weak(before.Signal)!=Weak(now.Signal)||Environment.TickCount64-lastReport>=60000);
                if(changed){reported=link;lastReport=Environment.TickCount64;}
            }
            if(changed&&link is State value)report(Weak(value.Signal)?"weak":"ok",new{signal=value.Signal,rxMbps=value.RxMbps,txMbps=value.TxMbps,accessPoint=value.AccessPoint});
        });
    }
    internal void Forget(){lock(sync){current=null;reported=null;}}
    static State? Read()
    {
        if(WlanOpenHandle(2,0,out _,out nint handle)!=0)return null;
        nint list=0;
        try
        {
            if(WlanEnumInterfaces(handle,0,out list)!=0)return null;
            int count=Math.Min(Marshal.ReadInt32(list),16);
            for(int i=0;i<count;i++)
            {
                nint entry=list+ListHeader+i*AdapterSize;
                if(Marshal.ReadInt32(entry+16+512)!=Connected)continue;
                Guid adapter=Marshal.PtrToStructure<Guid>(entry);
                if(WlanQueryInterface(handle,ref adapter,CurrentConnection,0,out uint size,out nint data,out _)!=0)continue;
                try
                {
                    if(size<AttributesSize)continue;
                    var address=new byte[6];Marshal.Copy(data+Bssid,address,0,6);
                    return new(Marshal.ReadInt32(data+Signal),Marshal.ReadInt32(data+RxRate)/1000,Marshal.ReadInt32(data+TxRate)/1000,Fingerprint(address));
                }
                finally{WlanFreeMemory(data);}
            }
            return null;
        }
        finally{if(list!=0)WlanFreeMemory(list);WlanCloseHandle(handle,0);}
    }
    // Enough to tell one access point from another in the log without recording its address.
    internal static string Fingerprint(byte[] address)=>Convert.ToHexString(SHA256.HashData(address),0,2).ToLowerInvariant();
}
