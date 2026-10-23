using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using MacDow.Core;

namespace MacDow.Windows;

internal static class LayoutSmoke
{
    internal static void Start(Application app,string output)
    {
        app.ShutdownMode=ShutdownMode.OnExplicitShutdown;
        app.Startup+=async(_,_)=>
        {
            TabBar? bar=null;Window? probe=null;
            var before=DisplayLayout.Select(Native.Monitors());
            try
            {
                bar=new TabBar(()=>{},()=>{},()=>{},()=>{},()=>{},new ComboBox(),new Button{Content="Updates"});
                bar.DockTo(before);await Task.Delay(350);
                var during=DisplayLayout.Select(Native.Monitors(),before.Id);
                probe=new Window{Title="mac-lan title-bar layout check",Width=700,Height=400,Left=(during.Left+100)*96.0/during.Dpi,Top=(during.Top+100)*96.0/during.Dpi,Content=new TextBlock{Text="Checking that a maximized app starts below the mac-lan tabs.",Margin=new Thickness(24)}};
                probe.Show();probe.WindowState=WindowState.Maximized;await Task.Delay(350);
                int titleTop=Native.VisibleWindowTop(probe);
                bool reserved=during.Top-before.Top==TabBarLayout.HeightPixels(before.Dpi),uncovered=titleTop>=during.Top;
                probe.Close();probe=null;bar.Dispose();bar=null;await Task.Delay(250);
                var after=DisplayLayout.Select(Native.Monitors(),before.Id);
                bool restored=before.Top==after.Top&&before.Height==after.Height;
                File.WriteAllText(output,JsonSerializer.Serialize(new{reserved,uncovered,restored,beforeTop=before.Top,duringTop=during.Top,maximizedVisibleTop=titleTop,afterTop=after.Top,uiStarted=true,captureStarted=false,hooksStarted=false}));
                app.Shutdown(reserved&&uncovered&&restored?0:1);
            }
            catch(Exception e){probe?.Close();bar?.Dispose();File.WriteAllText(output,JsonSerializer.Serialize(new{error=e.GetType().Name}));app.Shutdown(1);}
        };
    }
}
