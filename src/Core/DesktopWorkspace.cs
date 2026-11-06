namespace MacDow.Core;

public readonly record struct WindowBounds(int Left,int Top,int Width,int Height);
public sealed record DesktopWindow(nint Handle,string MonitorId,WindowBounds Bounds,object Placement,bool Minimized=false);
public interface IDesktopWindows
{
    IReadOnlyList<DesktopWindow> Capture();
    bool IsCurrent(DesktopWindow window);
    bool Move(DesktopWindow window,MonitorArea destination,WindowBounds bounds);
    bool Restore(DesktopWindow window);
}
public static class DesktopWindowRules
{
    const long Child=0x40000000,Caption=0x00C00000,ThickFrame=0x00040000;
    const long ToolWindow=0x80,AppWindow=0x40000,NoActivate=0x08000000;
    /// <summary>
    /// Whether a visible top-level window is an app window that should leave the Mac display.
    /// Framed windows (caption or sizing border) and explicit app windows qualify, as before.
    /// Unowned windows qualify too, as in Alt+Tab, so frameless app windows such as game
    /// launchers or custom-drawn clients are not left behind the Mac picture. Tool windows,
    /// no-activate overlays, child-style windows and owned frameless popups do not.
    /// </summary>
    public static bool IsAppWindow(long style,long extended,bool owned)
    {
        if((extended&ToolWindow)!=0||(style&Child)!=0)return false;
        if((extended&AppWindow)!=0||(style&(Caption|ThickFrame))!=0)return true;
        return !owned&&(extended&NoActivate)==0;
    }
}
public sealed class DesktopWorkspace(IDesktopWindows windows)
{
    readonly Dictionary<nint,DesktopWindow> saved=new();
    string? source;
    public void Activate(string sourceId,IReadOnlyList<MonitorArea> monitors)
    {
        if(source!=sourceId)Restore();
        var from=monitors.FirstOrDefault(m=>m.Id==sourceId);
        var target=monitors.Where(m=>m.Id!=sourceId).OrderByDescending(m=>m.Primary).FirstOrDefault();
        if(from==null||target==null||from.Width<=0||from.Height<=0||target.Width<=0||target.Height<=0)return;
        source=sourceId;
        foreach(var entry in saved.ToArray())
            if(!windows.IsCurrent(entry.Value))saved.Remove(entry.Key);
        foreach(var window in windows.Capture())
        {
            if(window.Minimized||window.MonitorId!=sourceId)continue;
            // An app may move itself back (re-centering on the primary display, for example).
            // Move it off again but keep the first snapshot, which holds its real position.
            if(saved.TryGetValue(window.Handle,out var first)&&windows.IsCurrent(first))
            {windows.Move(first with{Bounds=window.Bounds},target,MapBounds(window.Bounds,from,target));continue;}
            if(windows.Move(window,target,MapBounds(window.Bounds,from,target)))saved[window.Handle]=window;
        }
    }
    public static WindowBounds MapBounds(WindowBounds bounds,MonitorArea from,MonitorArea target)
    {
        double scale=(double)Math.Max(96,target.Dpi)/Math.Max(96,from.Dpi);
        int width=Math.Clamp((int)Math.Round(bounds.Width*scale),1,target.Width);
        int height=Math.Clamp((int)Math.Round(bounds.Height*scale),1,target.Height);
        int x=target.Left+Math.Clamp((int)Math.Round((bounds.Left-from.Left)*scale),0,target.Width-width);
        int y=target.Top+Math.Clamp((int)Math.Round((bounds.Top-from.Top)*scale),0,target.Height-height);
        return new(x,y,width,height);
    }
    public void Restore()
    {
        source=null;
        foreach(var entry in saved.ToArray())
            if(windows.Restore(entry.Value))saved.Remove(entry.Key);
    }
}
