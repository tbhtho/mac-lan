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
            if(window.Minimized||window.MonitorId!=sourceId||saved.ContainsKey(window.Handle))continue;
            if(windows.Move(window,target,MapBounds(window.Bounds,from,target)))saved.Add(window.Handle,window);
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
