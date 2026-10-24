using System.Windows;
namespace MacDow.Windows;
internal static class Program
{
    [STAThread] static void Main(string[] args)
    {
        if(args.Contains("--package-diagnostics"))
        {
            var probeApp=new Application();
            try{Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(Payloads.Diagnose()));}
            finally{probeApp.Shutdown();}
            return;
        }
        if(args.Contains("--update-check")){Console.WriteLine(UpdateProcess.Call("check","--channel","preview").GetAwaiter().GetResult().GetRawText());return;}
        if(args.Contains("--diagnostics"))
        {
            string runtime;try{runtime=Microsoft.Web.WebView2.Core.CoreWebView2Environment.GetAvailableBrowserVersionString();}catch(Exception e){runtime="Unavailable: "+e.Message;}
            var monitors=Native.Monitors();
            Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(new {dotnet=Environment.Version.ToString(),webview2=runtime,monitors,selected=monitors.Count>0?MacDow.Core.DisplayLayout.Select(monitors):null,uiStarted=false,hooksStarted=false,captureStarted=false}));
            return;
        }
        var app=new Application { ShutdownMode=ShutdownMode.OnMainWindowClose };
        app.Resources.MergedDictionaries.Add(new ResourceDictionary{Source=new Uri("/mac-lan;component/Theme.xaml",UriKind.Relative)});
        if(args.Length==2&&args[0]=="--layout-smoke"){LayoutSmoke.Start(app,args[1]);app.Run();return;}
        Telemetry.Report("startup");
        Telemetry.Log.Record("environment",state:new{os=Environment.OSVersion.VersionString,dotnet=Environment.Version.ToString(),arch=System.Runtime.InteropServices.RuntimeInformation.ProcessArchitecture.ToString(),buildId=typeof(Program).Assembly.ManifestModule.ModuleVersionId});
        AppDomain.CurrentDomain.UnhandledException+=(_,e)=>{Telemetry.Log.Record("fatal","process-unhandled",error:e.ExceptionObject as Exception);Telemetry.Flush().Wait(TimeSpan.FromSeconds(2));};
        TaskScheduler.UnobservedTaskException+=(_,e)=>Telemetry.Log.Record("task-failed","unobserved",error:e.Exception);
        app.DispatcherUnhandledException+=(_,e)=>{Telemetry.Log.Record("fatal","ui-unhandled",error:e.Exception);MessageBox.Show("mac-lan stopped safely. Reopen it to reconnect. Logs are saved on this PC in "+Telemetry.DirectoryPath,"mac-lan");app.Shutdown(1);e.Handled=true;};
        var main=new MainWindow();
        app.SessionEnding+=(_,_)=>main.Quit();
        try{app.Run(main);}finally{Telemetry.Flush().Wait(TimeSpan.FromSeconds(2));}
    }
}
