using System.Windows;
namespace MacDow.Windows;
internal static class Program
{
    [STAThread] static void Main(string[] args)
    {
        if(args.Contains("--package-diagnostics")){Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(Payloads.Diagnose()));return;}
        if(args.Contains("--update-check")){Console.WriteLine(UpdateProcess.Call("check","--channel","preview").GetAwaiter().GetResult().GetRawText());return;}
        if(args.Contains("--diagnostics"))
        {
            string runtime;try{runtime=Microsoft.Web.WebView2.Core.CoreWebView2Environment.GetAvailableBrowserVersionString();}catch(Exception e){runtime="Unavailable: "+e.Message;}
            var monitors=Native.Monitors();
            Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(new {dotnet=Environment.Version.ToString(),webview2=runtime,monitors,selected=monitors.Count>0?MacDow.Core.DisplayLayout.Select(monitors):null,uiStarted=false,hooksStarted=false,captureStarted=false}));
            return;
        }
        var app=new Application { ShutdownMode=ShutdownMode.OnMainWindowClose };
        app.Resources.MergedDictionaries.Add(new ResourceDictionary{Source=new Uri("/MAC-DOW;component/Theme.xaml",UriKind.Relative)});
        if(args.Length==2&&args[0]=="--layout-smoke"){LayoutSmoke.Start(app,args[1]);app.Run();return;}
        Telemetry.Report("startup");
        app.DispatcherUnhandledException+=(_,e)=>{Telemetry.Report("fatal",e.Exception.GetType().Name);MessageBox.Show("MAC-DOW stopped safely. Reopen it to reconnect. A diagnostic report is queued.","MAC-DOW");app.Shutdown(1);e.Handled=true;};
        app.Run(new MainWindow());
    }
}
