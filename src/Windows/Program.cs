using System.Windows;
namespace MacDow.Windows;
internal static class Program
{
    [STAThread] static void Main(string[] args)
    {
        if(args.Contains("--diagnostics"))
        {
            string runtime;try{runtime=Microsoft.Web.WebView2.Core.CoreWebView2Environment.GetAvailableBrowserVersionString();}catch(Exception e){runtime="Unavailable: "+e.Message;}
            var monitors=Native.Monitors();
            Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(new {dotnet=Environment.Version.ToString(),webview2=runtime,monitors,selected=monitors.Count>0?MacDow.Core.DisplayLayout.Select(monitors):null,uiStarted=false,hooksStarted=false,captureStarted=false}));
            return;
        }
        var app=new Application { ShutdownMode=ShutdownMode.OnMainWindowClose };
        app.DispatcherUnhandledException+=(_,e)=>{MessageBox.Show("MAC-DOW stopped safely. Reconnect and rearm control.\n"+e.Exception.Message,"MAC-DOW");app.Shutdown(1);e.Handled=true;};
        app.Run(new MainWindow());
    }
}

