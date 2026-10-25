using MacDow.Core;
using MacDow.Windows;

int checks=0;
void Check(bool condition,string message){if(!condition)throw new Exception(message);checks++;Console.WriteLine("PASS "+message);}
var source=new MonitorArea("mac",-1920,32,1920,1048,96,false);
var target=new MonitorArea("pc",0,0,1920,1080,96,true);
var monitors=new[]{source,target};
DesktopWindow Window(int handle,string monitor,int x=-1800,int y=100,int width=800,int height=600,string state="normal")=>new(handle,monitor,new(x,y,width,height),new SyntheticPlacement(state),state=="minimized");
{
    var desktop=new SyntheticDesktop(Window(1,"mac"),Window(2,"pc",100,200));
    var original=desktop.Current[1];var other=desktop.Current[2];var layout=new DesktopWorkspace(desktop);
    layout.Activate("mac",monitors);
    Check(desktop.Current[1].MonitorId=="pc"&&desktop.Current[1].Bounds==new WindowBounds(120,68,800,600),"MacBook moves the covered monitor's app onto the available Windows monitor");
    Check(desktop.Current[2]==other,"apps already on the available monitor keep their layout");
    layout.Activate("mac",monitors);layout.Restore();
    Check(desktop.Current[1]==original,"returning to Windows restores the original placement after repeated Mac selection");
    desktop.Current[1]=Window(1,"mac",-1700,120);var changed=desktop.Current[1];layout.Restore();
    Check(desktop.Current[1]==changed,"repeated restoration cannot replay an already restored snapshot");
}
{
    var desktop=new SyntheticDesktop(Window(1,"mac",state:"maximized"),Window(2,"mac",state:"minimized"));
    var original=desktop.Current.Values.ToArray();var layout=new DesktopWorkspace(desktop);
    layout.Activate("mac",monitors);
    Check(desktop.Current[1].Placement is SyntheticPlacement{State:"maximized"}&&desktop.Current[2].Placement is SyntheticPlacement{State:"minimized"},"relocation preserves each window's native state data");
    Check(desktop.Current[2].MonitorId=="mac","already minimized apps stay minimized in place until the user restores them");
    desktop.Current[3]=Window(3,"mac");layout.Activate("mac",monitors);
    Check(desktop.Current[3].MonitorId=="pc","new windows appearing behind an open MacBook tab move too");
    desktop.Current.Remove(3);layout.Restore();
    Check(original.All(w=>desktop.Current[w.Handle]==w),"restoration preserves normal bounds and minimized/maximized placement data");
}
{
    var smaller=new MonitorArea("pc",0,40,1000,700,144,true);
    var desktop=new SyntheticDesktop(Window(1,"mac",-1800,100,800,600));var layout=new DesktopWorkspace(desktop);
    layout.Activate("mac",new[]{source,smaller});
    Check(desktop.Current[1].Bounds==new WindowBounds(0,40,1000,700),"DPI-scaled windows are clamped inside a smaller destination work area");
    layout.Restore();Check(desktop.Current[1].Bounds==new WindowBounds(-1800,100,800,600),"mixed-DPI migration restores exact original negative coordinates");
}
{
    var large=new MonitorArea("pc",-3000,-500,3000,2000,192,true);
    var desktop=new SyntheticDesktop(Window(1,"mac"));var layout=new DesktopWorkspace(desktop);
    layout.Activate("mac",new[]{source,large});
    Check(desktop.Current[1].Bounds==new WindowBounds(-2760,-364,1600,1200),"logical app size and offset scale onto a negative-origin high-DPI monitor");
}
{
    var third=new MonitorArea("other",2000,0,1200,900,96,false);
    var desktop=new SyntheticDesktop(Window(1,"mac"));var layout=new DesktopWorkspace(desktop);
    layout.Activate("mac",new[]{source,third,target});
    Check(desktop.Current[1].MonitorId=="pc","the other primary monitor is preferred when multiple destinations are available");
    layout.Activate("pc",new[]{source,target});
    Check(desktop.Current[1].MonitorId=="mac","changing the Mac display restores the old workspace before relocating the new one");
    layout.Restore();Check(desktop.Current[1].MonitorId=="mac","display changes never overwrite a window's original restoration record");
}
{
    var desktop=new SyntheticDesktop(Window(1,"mac"));var layout=new DesktopWorkspace(desktop);var original=desktop.Current[1];
    layout.Activate("mac",new[]{source});layout.Activate("missing",monitors);layout.Restore();
    Check(desktop.Current[1]==original,"single-monitor and unavailable-source layouts leave windows untouched");
    desktop.RefuseMove=true;layout.Activate("mac",monitors);desktop.RefuseMove=false;
    desktop.Current[1]=Window(1,"mac",-1600);var manual=desktop.Current[1];layout.Restore();
    Check(desktop.Current[1]==manual,"a refused move cannot create a restoration record that later undoes a user's placement");
    layout.Activate("mac",monitors);desktop.RefuseRestore=true;layout.Restore();
    Check(desktop.Current[1].MonitorId=="pc","a refused restore does not pretend to have moved a window");
    desktop.RefuseRestore=false;layout.Restore();
    Check(desktop.Current[1]==manual,"a later restore retries retained placement after a transient failure");
}
{
    var desktop=new SyntheticDesktop(Window(1,"mac"));var layout=new DesktopWorkspace(desktop);
    layout.Activate("mac",monitors);desktop.Current.Remove(1);
    var replacement=Window(1,"mac",-1500,150);desktop.Current[1]=replacement;
    layout.Activate("mac",monitors);
    Check(desktop.Current[1].MonitorId=="pc","a new source window reusing a closed HWND is relocated while MacBook stays open");
    layout.Restore();Check(desktop.Current[1]==replacement,"a reused HWND restores the replacement's own placement, never the closed window's");
}
{
    string directory=Path.Combine(Path.GetTempPath(),"mac-lan-bar-test-"+Guid.NewGuid().ToString("N"));
    string path=Path.Combine(directory,"bar-preferences.json"),neighbor=Path.Combine(directory,"user-settings.json");
    var store=new BarPreferencesStore(path);
    try
    {
        Check(!store.LoadAutoHide(),"missing bar preferences keep the existing always-visible behavior");
        store.SaveAutoHide(true);
        Check(new BarPreferencesStore(path).LoadAutoHide(),"auto-hide remains enabled after recreating the preferences store");
        File.WriteAllText(neighbor,"preserve");store.SaveAutoHide(false);
        Check(!new BarPreferencesStore(path).LoadAutoHide()&&File.ReadAllText(neighbor)=="preserve","turning auto-hide off persists without changing neighboring settings");
        File.WriteAllText(path,"not json");Check(!store.LoadAutoHide(),"damaged bar preferences fall back to a visible recoverable bar");
    }
    finally{if(File.Exists(path))File.Delete(path);if(File.Exists(neighbor))File.Delete(neighbor);if(Directory.Exists(directory))Directory.Delete(directory);}
}
Console.WriteLine(checks+" desktop workspace/preferences checks passed; synthetic windows only.");

sealed class SyntheticDesktop(params DesktopWindow[] windows):IDesktopWindows
{
    public readonly Dictionary<nint,DesktopWindow> Current=windows.ToDictionary(w=>w.Handle);
    public bool RefuseMove,RefuseRestore;
    public bool IsCurrent(DesktopWindow window)=>Current.TryGetValue(window.Handle,out var current)&&ReferenceEquals(current.Placement,window.Placement);
    public IReadOnlyList<DesktopWindow> Capture()=>Current.Values.ToArray();
    public bool Move(DesktopWindow window,MonitorArea destination,WindowBounds bounds)
    {if(RefuseMove)return false;Current[window.Handle]=window with{MonitorId=destination.Id,Bounds=bounds};return true;}
    public bool Restore(DesktopWindow window)
    {if(RefuseRestore)return false;if(Current.ContainsKey(window.Handle))Current[window.Handle]=window;return true;}
}
sealed record SyntheticPlacement(string State);
