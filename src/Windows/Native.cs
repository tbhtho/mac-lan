using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using MacDow.Core;
namespace MacDow.Windows;

internal static class Native
{
    [StructLayout(LayoutKind.Sequential)] struct Rect { public int Left,Top,Right,Bottom; }
    [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)] struct Info { public int Size; public Rect Monitor,Work; public uint Flags; [MarshalAs(UnmanagedType.ByValTStr,SizeConst=32)] public string Device; }
    delegate bool MonitorProc(nint monitor,nint dc,nint rect,nint data);
    [DllImport("user32.dll")] static extern bool EnumDisplayMonitors(nint dc,nint clip,MonitorProc proc,nint data);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern bool GetMonitorInfo(nint monitor,ref Info info);
    [DllImport("shcore.dll")] static extern int GetDpiForMonitor(nint monitor,int type,out uint x,out uint y);
    [DllImport("user32.dll")] internal static extern nint GetForegroundWindow();
    [DllImport("user32.dll")] static extern bool SetWindowPos(nint hwnd,nint after,int x,int y,int cx,int cy,uint flags);
    [DllImport("user32.dll")] internal static extern short GetAsyncKeyState(int key);
    [DllImport("user32.dll",SetLastError=true)] internal static extern bool RegisterHotKey(nint hwnd,int id,uint modifiers,uint key);
    [DllImport("user32.dll")] internal static extern bool UnregisterHotKey(nint hwnd,int id);
    public static IReadOnlyList<MonitorArea> Monitors()
    {
        var result=new List<MonitorArea>();
        EnumDisplayMonitors(0,0,(m,_,__,___)=> { var i=new Info { Size=Marshal.SizeOf<Info>(),Device="" }; if(GetMonitorInfo(m,ref i)){uint dpi=96;try{if(GetDpiForMonitor(m,0,out var x,out var y)==0)dpi=x;}catch(DllNotFoundException){} result.Add(new(i.Device,i.Work.Left,i.Work.Top,i.Work.Right-i.Work.Left,i.Work.Bottom-i.Work.Top,dpi,(i.Flags&1)!=0));}return true;},0);
        return result;
    }
    public static void Position(Window window,MonitorArea m,bool strip)
    {
        var hwnd=new WindowInteropHelper(window).Handle;
        SetWindowPos(hwnd,-1,m.Left,m.Top,m.Width,strip?(int)Math.Ceiling(44*m.Dpi/96.0):m.Height,0x0010|0x0040);
    }
}

internal sealed class KeyboardHook : IDisposable
{
    [StructLayout(LayoutKind.Sequential)] struct KeyInfo { public uint Vk,Scan,Flags,Time;public nint Extra; }
    delegate nint HookProc(int code,nint message,nint data);
    [DllImport("user32.dll",SetLastError=true)] static extern nint SetWindowsHookEx(int id,HookProc callback,nint module,uint thread);
    [DllImport("user32.dll")] static extern bool UnhookWindowsHookEx(nint hook);
    [DllImport("user32.dll")] static extern nint CallNextHookEx(nint hook,int code,nint message,nint data);
    [DllImport("kernel32.dll",CharSet=CharSet.Unicode)] static extern nint GetModuleHandle(string? name);
    readonly HookProc callback;
    readonly Func<bool> allowed;
    readonly Action<int,bool> send;
    readonly Action escape;
    nint hook;
    public KeyboardHook(Func<bool> allowed,Action<int,bool> send,Action escape)
    {this.allowed=allowed;this.send=send;this.escape=escape;callback=Handle;}
    public void Install()
    {
        if(hook!=0)return;hook=SetWindowsHookEx(13,callback,GetModuleHandle(null),0);
        if(hook==0)throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
    }
    nint Handle(int code,nint message,nint data)
    {
        if(code>=0&&allowed())
        {
            var key=Marshal.PtrToStructure<KeyInfo>(data);int vk=(int)key.Vk;bool down=message==0x100||message==0x104;
            bool pressed(int v)=>(Native.GetAsyncKeyState(v)&0x8000)!=0;
            if(vk==8&&pressed(0x11)&&pressed(0x12)&&pressed(0x10)){escape();return 1;}
            // Windows shell/security combinations stay local, and immediately revoke remote ownership.
            if(vk is 0x5B or 0x5C || pressed(0x5B)||pressed(0x5C)||vk==0x2E&&pressed(0x11)&&pressed(0x12))
            {escape();return CallNextHookEx(hook,code,message,data);}
            if(KeyMap.MacCode(vk) is int mac){send(mac,down);return 1;}
        }
        return CallNextHookEx(hook,code,message,data);
    }
    public void Dispose(){if(hook!=0){UnhookWindowsHookEx(hook);hook=0;}}
}

