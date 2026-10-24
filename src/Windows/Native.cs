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
    [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(nint hwnd,uint attribute,out Rect bounds,uint size);
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
        SetWindowPos(hwnd,0,m.Left,m.Top,m.Width,strip?TabBarLayout.HeightPixels(m.Dpi):m.Height,0x0010|0x0040|0x0004);
    }
    // The Mac view fills the whole monitor below the mac-lan bar, over the Windows taskbar.
    public static void PositionCovering(Window window,MonitorArea m)
    {
        if(ScreenBounds(m.Id) is not var (left,top,width,height)){Position(window,m,false);return;}
        int bar=TabBarLayout.HeightPixels(m.Dpi);
        SetWindowPos(new WindowInteropHelper(window).Handle,0,left,top+bar,width,height-bar,0x0010|0x0040|0x0004);
    }
    public static (int Left,int Top,int Width,int Height)? ScreenBounds(string id)
    {
        (int,int,int,int)? result=null;
        EnumDisplayMonitors(0,0,(m,_,__,___)=>{var i=new Info{Size=Marshal.SizeOf<Info>(),Device=""};if(GetMonitorInfo(m,ref i)&&i.Device==id)result=(i.Monitor.Left,i.Monitor.Top,i.Monitor.Right-i.Monitor.Left,i.Monitor.Bottom-i.Monitor.Top);return true;},0);
        return result;
    }
    public static int VisibleWindowTop(Window window)
    {
        if(DwmGetWindowAttribute(new WindowInteropHelper(window).Handle,9,out var rect,(uint)Marshal.SizeOf<Rect>())!=0)throw new InvalidOperationException("Cannot read visible window frame.");
        return rect.Top;
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
    readonly KeyRouter router=new(v=>(Native.GetAsyncKeyState(v)&0x8000)!=0);
    nint hook;
    bool releasing;
    public KeyboardHook(Func<bool> allowed,Action<int,bool> send,Action escape)
    {this.allowed=allowed;this.send=send;this.escape=escape;callback=Handle;}
    public void Install()
    {
        releasing=false;
        if(hook!=0)return;router.Reset();hook=SetWindowsHookEx(13,callback,GetModuleHandle(null),0);
        if(hook==0)throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
    }
    nint Handle(int code,nint message,nint data)
    {
        if(code<0)return CallNextHookEx(hook,code,message,data);
        var key=Marshal.PtrToStructure<KeyInfo>(data);bool down=message==0x100||message==0x104;
        var decision=router.Route((int)key.Vk,down,!releasing&&allowed());
        if(releasing&&!router.OwnsKeys)System.Windows.Threading.Dispatcher.CurrentDispatcher.BeginInvoke(()=>{if(releasing)Dispose();});
        switch(decision.Route)
        {
            case KeyRoute.Remote:send(decision.MacCode,down);return 1;
            case KeyRoute.Sequence:foreach(var (mac,press) in decision.Keys!)send(mac,press);return 1;
            case KeyRoute.Swallow:return 1;
            case KeyRoute.Release:escape();return 1;
            case KeyRoute.ReleaseLocal:escape();break;
        }
        return CallNextHookEx(hook,code,message,data);
    }
    // Control ended: keep swallowing releases of keys the Mac received, so Windows never
    // sees a stray Windows-key or Alt release, then remove the hook once they are all up.
    public void Release(){if(hook==0)return;if(router.OwnsKeys)releasing=true;else Dispose();}
    public void Dispose(){releasing=false;if(hook!=0){UnhookWindowsHookEx(hook);hook=0;}}
}
