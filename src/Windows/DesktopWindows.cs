using System.Runtime.InteropServices;
using System.Text;
using MacDow.Core;

namespace MacDow.Windows;

// No titles or app content are read. Snapshot data and HWND identity markers
// live only for this mac-lan process; foreign windows are never activated to move.
internal sealed class DesktopWindows:IDesktopWindows
{
    [StructLayout(LayoutKind.Sequential)] struct Point {public int X,Y;}
    [StructLayout(LayoutKind.Sequential)] struct Rect {public int Left,Top,Right,Bottom;}
    [StructLayout(LayoutKind.Sequential)] struct Placement
    {public uint Length,Flags,Show;public Point Min,Max;public Rect Normal;}
    sealed class Saved(uint process,Placement placement,MonitorArea source,WindowBounds actual)
    {public readonly uint Process=process;public readonly Placement Placement=placement;public readonly MonitorArea Source=source;public readonly WindowBounds Actual=actual;}
    readonly string marker="mac-lan.workspace."+Guid.NewGuid().ToString("N");
    delegate bool WindowProc(nint window,nint data);
    [DllImport("user32.dll")] static extern bool EnumWindows(WindowProc callback,nint data);
    [DllImport("user32.dll")] static extern bool IsWindow(nint window);
    [DllImport("user32.dll")] static extern bool IsWindowVisible(nint window);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(nint window,out uint process);
    [DllImport("user32.dll",EntryPoint="GetWindowLongPtrW")] static extern nint GetWindowLongPtr(nint window,int index);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(nint window,StringBuilder name,int capacity);
    [DllImport("user32.dll")] static extern bool GetWindowPlacement(nint window,ref Placement placement);
    [DllImport("user32.dll")] static extern bool SetWindowPlacement(nint window,ref Placement placement);
    [DllImport("user32.dll")] static extern bool GetWindowRect(nint window,out Rect rect);
    [DllImport("user32.dll")] static extern bool SetWindowPos(nint window,nint after,int x,int y,int width,int height,uint flags);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern bool SetProp(nint window,string name,nint value);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern nint GetProp(nint window,string name);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern nint RemoveProp(nint window,string name);
    [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(nint window,uint attribute,out int value,int size);

    public IReadOnlyList<DesktopWindow> Capture()
    {
        var monitors=Native.Monitors();var result=new List<DesktopWindow>();
        EnumWindows((window,_)=>
        {
            if(!IsWindowVisible(window))return true;
            GetWindowThreadProcessId(window,out uint process);
            if(process==0||process==(uint)Environment.ProcessId)return true;
            long style=GetWindowLongPtr(window,-16).ToInt64(),extended=GetWindowLongPtr(window,-20).ToInt64();
            if((extended&0x80)!=0||((style&0x00C40000)==0&&(extended&0x40000)==0))return true;
            var name=new StringBuilder(128);GetClassName(window,name,name.Capacity);
            if(name.ToString() is "Progman" or "WorkerW" or "Shell_TrayWnd" or "Shell_SecondaryTrayWnd" or "#32768" or "tooltips_class32")return true;
            if(DwmGetWindowAttribute(window,14,out int cloaked,sizeof(int))==0&&cloaked!=0)return true;
            var placement=new Placement{Length=(uint)Marshal.SizeOf<Placement>()};
            if(!GetWindowPlacement(window,ref placement)||!GetWindowRect(window,out var actual))return true;
            // Let Windows resolve the monitor, including pre-minimize placement.
            string? monitorId=Native.WindowMonitorId(window);
            var monitor=monitors.FirstOrDefault(m=>m.Id==monitorId);
            if(monitor==null)return true;
            var bounds=ToScreen(placement.Normal,monitor);
            bool minimized=placement.Show is 2 or 6 or 7;
            if(bounds.Width>0&&bounds.Height>0)result.Add(new(window,monitor.Id,bounds,new Saved(process,placement,monitor,new(actual.Left,actual.Top,actual.Right-actual.Left,actual.Bottom-actual.Top)),minimized));
            return true;
        },0);
        return result;
    }
    static WindowBounds ToScreen(Rect rect,MonitorArea monitor)
    {
        var screen=Native.ScreenBounds(monitor.Id);
        int x=screen==null?0:monitor.Left-screen.Value.Left;
        int y=screen==null?0:monitor.Top-screen.Value.Top;
        return new(rect.Left+x,rect.Top+y,rect.Right-rect.Left,rect.Bottom-rect.Top);
    }
    static Rect ToPlacement(WindowBounds bounds,MonitorArea monitor)
    {
        var screen=Native.ScreenBounds(monitor.Id);
        int x=screen==null?0:monitor.Left-screen.Value.Left;
        int y=screen==null?0:monitor.Top-screen.Value.Top;
        return new(){Left=bounds.Left-x,Top=bounds.Top-y,Right=bounds.Left-x+bounds.Width,Bottom=bounds.Top-y+bounds.Height};
    }
    public bool Move(DesktopWindow window,MonitorArea destination,WindowBounds bounds)
    {
        if(window.Minimized||window.Placement is not Saved saved||!IsWindow(window.Handle))return false;
        var current=new Placement{Length=(uint)Marshal.SizeOf<Placement>()};
        if(!GetWindowPlacement(window.Handle,ref current)||current.Show is 2 or 6 or 7)return false;
        GetWindowThreadProcessId(window.Handle,out uint process);
        if(process!=saved.Process||!SetProp(window.Handle,marker,1))return false;
        if(current.Show==3)bounds=new(destination.Left,destination.Top,destination.Width,destination.Height);
        bool moved=SetWindowPos(window.Handle,0,bounds.Left,bounds.Top,bounds.Width,bounds.Height,0x4000|0x0010|0x0004|0x0200);
        if(!moved)RemoveProp(window.Handle,marker);
        return moved;
    }
    public bool IsCurrent(DesktopWindow window)
    {
        if(window.Placement is not Saved saved||!IsWindow(window.Handle))return false;
        GetWindowThreadProcessId(window.Handle,out uint process);
        // Windows removes properties when a window closes. A reused HWND cannot
        // receive the old app's saved placement, even within the same process.
        return process==saved.Process&&GetProp(window.Handle,marker)==1;
    }
    public bool Restore(DesktopWindow window)
    {
        if(!IsCurrent(window)||window.Placement is not Saved saved)return true;
        var current=new Placement{Length=(uint)Marshal.SizeOf<Placement>()};
        if(!GetWindowPlacement(window.Handle,ref current))return false;
        // Do not wake a window the user minimized during the Mac session.
        // Retain its snapshot and retry once it is restored while mac-lan runs.
        if(current.Show is 2 or 6 or 7)return false;
        var monitors=Native.Monitors();
        var target=monitors.FirstOrDefault(m=>m.Id==saved.Source.Id)??monitors.FirstOrDefault(m=>m.Primary)??monitors.FirstOrDefault();
        if(target==null)return false;
        WindowBounds normal=target==saved.Source?window.Bounds:DesktopWorkspace.MapBounds(window.Bounds,saved.Source,target);
        var placement=saved.Placement;placement.Flags=4;placement.Show=8;
        placement.Normal=ToPlacement(normal,target);
        // SW_SHOWNA keeps the current show state and does not activate. In
        // particular, never restore with SW_SHOWMAXIMIZED (which steals focus).
        if(!SetWindowPlacement(window.Handle,ref placement))return false;
        WindowBounds actual=current.Show==3
            ?saved.Placement.Show==3&&target==saved.Source?saved.Actual:new(target.Left,target.Top,target.Width,target.Height)
            :saved.Placement.Show==3?normal:target==saved.Source?saved.Actual:DesktopWorkspace.MapBounds(saved.Actual,saved.Source,target);
        if(!SetWindowPos(window.Handle,0,actual.Left,actual.Top,actual.Width,actual.Height,0x4000|0x0010|0x0004|0x0200))return false;
        RemoveProp(window.Handle,marker);return true;
    }
}
