using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using MacDow.Core;

namespace MacDow.Windows;

internal static class LayoutSmoke
{
    // Draws the top bar to a PNG without docking it or touching the desktop, to review its layout.
    internal static void Preview(Application app,string output)
    {
        app.ShutdownMode=ShutdownMode.OnExplicitShutdown;
        app.Startup+=async(_,_)=>
        {
            TabBar? bar=null;
            try
            {
                var update=new Button{Content="Updates"};
                bar=new TabBar(()=>{},()=>{},()=>{},()=>{},()=>{},_=>{},update,files:()=>{},terminal:()=>{});
                bar.SetMonitors(Native.Monitors(),null);bar.SetConnection(true,false,"Connected to your Mac");bar.SetMode(false);
                bar.WindowStartupLocation=WindowStartupLocation.Manual;bar.Left=-6000;bar.Top=-6000;bar.Width=1280;bar.ShowActivated=false;bar.Show();
                await Task.Delay(300);
                void Save(string path)
                {
                    var bitmap=new System.Windows.Media.Imaging.RenderTargetBitmap((int)bar.ActualWidth*2,(int)bar.ActualHeight*2,192,192,System.Windows.Media.PixelFormats.Pbgra32);
                    bitmap.Render(bar);
                    var encoder=new System.Windows.Media.Imaging.PngBitmapEncoder();encoder.Frames.Add(System.Windows.Media.Imaging.BitmapFrame.Create(bitmap));
                    using var file=File.Create(path);encoder.Save(file);
                }
                Save(output);
                update.Content="Update ready";bar.SetConnection(true,true);await Task.Delay(200);
                Save(Path.ChangeExtension(output,null)+"-update.png");
                bar.Close();app.Shutdown(0);
            }
            catch(Exception e){bar?.Close();File.WriteAllText(output+".error.txt",e.ToString());app.Shutdown(1);}
        };
    }
    internal static void Start(Application app,string output)
    {
        app.ShutdownMode=ShutdownMode.OnExplicitShutdown;
        app.Startup+=async(_,_)=>
        {
            TabBar? bar=null;Window? probe=null;
            var before=DisplayLayout.Select(Native.Monitors());
            try
            {
                bar=new TabBar(()=>{},()=>{},()=>{},()=>{},()=>{},_=>{},new Button{Content="Updates"});
                bar.SetMonitors(Native.Monitors(),before.Id);
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
