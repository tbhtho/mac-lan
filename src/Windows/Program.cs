using System.Windows;
namespace MacDow.Windows;
internal static class Program
{
    [STAThread] static void Main(string[] args)
    {
        // An AI agent or a terminal starts mac-lan.exe with one of these to reach the running app's
        // link to the Mac. The bundled helper does the work on this process's own stdin and stdout.
        if(args.Length>0&&args[0] is "--mcp" or "--remote-shell")
        {
            // A windowed program does not hand its redirected stdin and stdout to a child by itself, so copy both ways.
            var start=new System.Diagnostics.ProcessStartInfo(Payloads.Helper){UseShellExecute=false,CreateNoWindow=true,RedirectStandardInput=true,RedirectStandardOutput=true};
            start.ArgumentList.Add(args[0]=="--mcp"?"mcp":"remote-shell");
            foreach(string argument in args.Skip(1))start.ArgumentList.Add(argument);
            try
            {
                using var helper=System.Diagnostics.Process.Start(start);
                if(helper==null){Environment.Exit(1);return;}
                static void Pump(System.IO.Stream from,System.IO.Stream to)
                {
                    var buffer=new byte[64*1024];
                    try{int count;while((count=from.Read(buffer,0,buffer.Length))>0){to.Write(buffer,0,count);to.Flush();}}
                    catch(Exception e)when(e is System.IO.IOException or ObjectDisposedException or NotSupportedException){}
                }
                var toHelper=new Thread(()=>{Pump(Console.OpenStandardInput(),helper.StandardInput.BaseStream);try{helper.StandardInput.Close();}catch(Exception e)when(e is System.IO.IOException or ObjectDisposedException){}}){IsBackground=true};
                toHelper.Start();
                Pump(helper.StandardOutput.BaseStream,Console.OpenStandardOutput());
                helper.WaitForExit();Environment.Exit(helper.ExitCode);
            }
            catch(Exception e)when(e is System.ComponentModel.Win32Exception or System.IO.IOException or InvalidOperationException){Environment.Exit(1);}
            return;
        }
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
        // One mac-lan per Windows account: a second launch (Start menu, shortcut) asks the
        // running copy to open its Connection page instead of creating a second top bar.
        bool layoutSmoke=args.Length==2&&args[0] is "--layout-smoke" or "--bar-preview";
        using var instance=new Mutex(true,@"Local\mac-lan-single-instance",out bool firstInstance);
        using var activate=new EventWaitHandle(false,EventResetMode.AutoReset,@"Local\mac-lan-activate");
        if(!firstInstance&&!layoutSmoke)
        {
            // An updater relaunch can overlap a closing copy briefly; wait for it before giving up.
            try{firstInstance=instance.WaitOne(TimeSpan.FromSeconds(5));}catch(AbandonedMutexException){firstInstance=true;}
            if(!firstInstance){activate.Set();return;}
        }
        var app=new Application { ShutdownMode=ShutdownMode.OnMainWindowClose };
        app.Resources.MergedDictionaries.Add(new ResourceDictionary{Source=new Uri("/mac-lan;component/Theme.xaml",UriKind.Relative)});
        if(args.Length==2&&args[0]=="--layout-smoke"){LayoutSmoke.Start(app,args[1]);app.Run();return;}
        if(args.Length==2&&args[0]=="--bar-preview"){LayoutSmoke.Preview(app,args[1]);app.Run();return;}
        Telemetry.Report("startup");
        Telemetry.Log.Record("environment",state:new{os=Environment.OSVersion.VersionString,dotnet=Environment.Version.ToString(),arch=System.Runtime.InteropServices.RuntimeInformation.ProcessArchitecture.ToString(),buildId=typeof(Program).Assembly.ManifestModule.ModuleVersionId});
        AppDomain.CurrentDomain.UnhandledException+=(_,e)=>{Telemetry.Log.Record("fatal","process-unhandled",error:e.ExceptionObject as Exception);Telemetry.Flush().Wait(TimeSpan.FromSeconds(2));};
        TaskScheduler.UnobservedTaskException+=(_,e)=>Telemetry.Log.Record("task-failed","unobserved",error:e.Exception);
        app.DispatcherUnhandledException+=(_,e)=>{Telemetry.Log.Record("fatal","ui-unhandled",error:e.Exception);MessageBox.Show("mac-lan stopped safely. Reopen it to reconnect. Logs are saved on this PC in "+Telemetry.DirectoryPath,"mac-lan");app.Shutdown(1);e.Handled=true;};
        var main=new MainWindow();
        var watcher=new Thread(()=>{while(activate.WaitOne()){try{app.Dispatcher.BeginInvoke(main.ActivateFromSecondLaunch);}catch(TaskCanceledException){return;}}}){IsBackground=true,Name="mac-lan activation"};
        if(!layoutSmoke)watcher.Start();
        app.SessionEnding+=(_,_)=>main.Quit();
        try{app.Run(main);}finally{Telemetry.Flush().Wait(TimeSpan.FromSeconds(2));}
    }
}
