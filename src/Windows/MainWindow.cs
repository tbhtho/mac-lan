using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Threading;
using MacDow.Core;
using Microsoft.Web.WebView2.Core;
using Microsoft.Web.WebView2.Wpf;
namespace MacDow.Windows;

internal sealed class MainWindow : Window
{
    readonly DockPanel root=new();
    readonly Border body=new(){Background=new SolidColorBrush(Color.FromRgb(22,26,31))};
    readonly StackPanel setup=new(){Margin=new Thickness(28),MaxWidth=680,HorizontalAlignment=HorizontalAlignment.Left};
    readonly TextBox address=new(){Text="",Width=170},port=new(){Text="4433",Width=70},fingerprint=new(){Width=570};
    readonly PasswordBox token=new(){Width=570};
    readonly ComboBox windows=new(){Width=340,DisplayMemberPath="Label"};
    readonly ComboBox monitors=new(){Width=140,DisplayMemberPath="Id"};
    readonly ComboBox codec=new(){Width=160},resolution=new(){Width=110},fps=new(){Width=80},bitrate=new(){Width=100};
    readonly TextBlock status=new(){Text="Local Windows � Ctrl+Alt+Shift+Backspace releases control",VerticalAlignment=VerticalAlignment.Center,Foreground=Brushes.LightGray,Margin=new Thickness(12,0,8,0)};
    readonly Button control=new(){Content="Control Mac",Padding=new Thickness(14,5,14,5)};
    readonly ControlGate gate=new();
    readonly KeyboardHook keyboard;
    readonly DispatcherTimer timer=new(){Interval=TimeSpan.FromMilliseconds(250)};
    readonly RemoteView desktop;
    Session? session;
    RemoteView? appView;
    Window? appWindow;
    bool remote,closing,connecting;
    byte[]? pendingFrame;
    int frameScheduled;
    string? preferred;
    long lastLease;
    ulong controlGeneration;
    HwndSource? source;
    public MainWindow()
    {
        Title="MAC-DOW";WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;Topmost=true;ShowInTaskbar=true;MinWidth=300;
        Background=new SolidColorBrush(Color.FromRgb(28,33,39));Foreground=Brushes.White;
        Content=root;
        var strip=new DockPanel {Height=44,Background=Background};DockPanel.SetDock(strip,Dock.Top);root.Children.Add(strip);
        var left=new StackPanel{Orientation=Orientation.Horizontal};strip.Children.Add(left);
        left.Children.Add(MakeButton("01  Windows",()=>ShowDesktop()));
        left.Children.Add(MakeButton("02  MacBook",()=>ShowRemote()));
        left.Children.Add(MakeButton("Pair / App Priority",()=>ShowSetup()));
        monitors.SelectionChanged+=(_,_)=>{if(monitors.SelectedItem is MonitorArea m){preferred=m.Id;gate.Disarm();Position();}};left.Children.Add(monitors);
        var close=MakeButton("x",()=>Close());DockPanel.SetDock(close,Dock.Right);strip.Children.Add(close);strip.Children.Add(status);
        root.Children.Add(body);desktop=new RemoteView(HandlePointer,ReportVideo);BuildSetup();
        keyboard=new KeyboardHook(()=>gate.CanRoute&&OwnerIsForeground(),(code,down)=>session?.Send(new{type="key",code,down}),Escape);
        gate.Released+=()=>{keyboard.Dispose();session?.Send(new{type="release"});UpdateControl();};
        control.Click+=(_,_)=>Arm();
        Loaded+=(_,_)=>{RefreshMonitors();ShowDesktop();};
        SourceInitialized+=(_,_)=>{source=HwndSource.FromHwnd(new WindowInteropHelper(this).Handle);source.AddHook(WndProc);if(!Native.RegisterHotKey(source.Handle,901,0x4000|1|2|4,8))status.Text="Escape shortcut unavailable; close button and focus loss still release control.";};
        Deactivated+=(_,_)=>{if(appWindow==null)gate.SetContext(session?.IsReady==true,remote,false);};
        Activated+=(_,_)=>RefreshContext();
        timer.Tick+=(_,_)=>Tick();timer.Start();
        Closing+=(_,_)=>{closing=true;gate.Disarm();timer.Stop();Native.UnregisterHotKey(new WindowInteropHelper(this).Handle,901);source?.RemoveHook(WndProc);appWindow?.Close();session?.Abort("Closed");desktop.Dispose();};
    }
    Button MakeButton(string text,Action action){var b=new Button{Content=text,Padding=new Thickness(14,6,14,6),Margin=new Thickness(2),VerticalAlignment=VerticalAlignment.Center};b.Click+=(_,_)=>action();return b;}
    void BuildSetup()
    {
        setup.Children.Add(new TextBlock{Text="Connect your Mac",FontSize=26,Margin=new Thickness(0,0,0,12)});
        setup.Children.Add(new TextBlock{Text="Run the foreground companion on your Mac. Compare its fingerprint here.\nPairing lasts only for that host launch. Control always starts off.",TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,0,0,18)});
        var row=new StackPanel{Orientation=Orientation.Horizontal};row.Children.Add(address);row.Children.Add(port);AddField("Private Mac IPv4 / port",row);AddField("Certificate SHA-256 - compare on the Mac",fingerprint);AddField("Temporary 64-digit pairing token",token);
        setup.Children.Add(MakeButton("Connect and list Mac windows",async()=>await Connect()));
        codec.ItemsSource=new[]{"H.264 hardware","JPEG diagnostic"};codec.SelectedIndex=0;
        resolution.ItemsSource=new[]{960,1280,1600,1920};resolution.SelectedItem=1600;
        fps.ItemsSource=new[]{10,15,30};fps.SelectedItem=30;
        bitrate.ItemsSource=new[]{2,4,8,12,20};bitrate.SelectedItem=8;
        var quality=new StackPanel{Orientation=Orientation.Horizontal};foreach(var v in new Control[]{codec,resolution,fps,bitrate}){v.Margin=new Thickness(0,0,8,0);quality.Children.Add(v);}AddField("Codec / width cap / fps / H.264 Mbit/s",quality);
        setup.Children.Add(MakeButton("Start full-desktop tab",()=>Start(false)));
        AddField("App Priority - one specific Mac window",windows);setup.Children.Add(MakeButton("Open App Priority window",()=>Start(true)));
        setup.Children.Add(new TextBlock{Text="App Priority includes only the chosen window. Menus, child windows and dialogs are excluded. Control pauses when the chosen window is no longer foremost. It never falls back to desktop.",TextWrapping=TextWrapping.Wrap,Foreground=Brushes.LightSteelBlue,Margin=new Thickness(0,12,0,12)});
        setup.Children.Add(MakeButton("Disconnect",()=>Disconnect()));
        setup.Children.Add(new TextBlock{Text="Prerelease � unsigned � M3 performance unmeasured\nEscape: Ctrl+Alt+Shift+Backspace. Alt+Tab  Command+Tab while controlling.\nWindows security shortcuts remain local. No clipboard, audio, IME or file transfer.",TextWrapping=TextWrapping.Wrap});
    }
    void AddField(string label,UIElement element){setup.Children.Add(new TextBlock{Text=label,Margin=new Thickness(0,8,0,4)});setup.Children.Add(element);}
    void ShowSetup(){gate.Disarm();remote=false;if(setup.Parent is ScrollViewer old)old.Content=null;body.Child=new ScrollViewer{Content=setup,VerticalScrollBarVisibility=ScrollBarVisibility.Auto};Position(false);}
    void ShowDesktop(){gate.Disarm();remote=false;body.Child=null;Position();RefreshContext();status.Text="Local Windows � escape Ctrl+Alt+Shift+Backspace";}
    void ShowRemote()
    {
        if(session?.IsReady!=true){ShowSetup();return;}
        remote=true;body.Child=RemotePanel(desktop);Position(false);RefreshContext();UpdateControl();
    }
    DockPanel RemotePanel(RemoteView view)
    {
        if(view.Parent is Panel previous)previous.Children.Remove(view);
        var panel=new DockPanel();var toolbar=new StackPanel{Orientation=Orientation.Horizontal,Height=40,Background=new SolidColorBrush(Color.FromRgb(34,42,49))};
        // A single control button is reparented only after the previous surface has been detached.
        if(control.Parent is Panel old)old.Children.Remove(control);toolbar.Children.Add(control);
        toolbar.Children.Add(MakeButton("Release",Escape));toolbar.Children.Add(MakeButton("Disconnect",()=>Disconnect()));
        toolbar.Children.Add(new TextBlock{Text="  Ctrl+Alt+Shift+Backspace � release",VerticalAlignment=VerticalAlignment.Center});
        DockPanel.SetDock(toolbar,Dock.Top);panel.Children.Add(toolbar);panel.Children.Add(view);return panel;
    }
    async Task Connect()
    {
        if(connecting)return;connecting=true;Disconnect();status.Text="Connecting with pinned TLS.";
        var s=new Session();session=s;
        s.Message+=m=>Dispatcher.BeginInvoke(()=>HandleMessage(s,m));
        s.Frame+=b=>{Interlocked.Exchange(ref pendingFrame,b);if(Interlocked.Exchange(ref frameScheduled,1)==0)Dispatcher.BeginInvoke(()=>{var latest=Interlocked.Exchange(ref pendingFrame,null);Interlocked.Exchange(ref frameScheduled,0);if(latest!=null&&session==s&&s.IsReady&&(remote||appWindow!=null))(appView??desktop).Frame(latest);},DispatcherPriority.Background);};
        s.Ended+=why=>Dispatcher.BeginInvoke(()=>{if(session==s){gate.Disarm();(appView??desktop).Clear();status.Text=why;if(appWindow!=null)appWindow.Title="MAC-DOW - disconnected / view only";}});
        try{await s.Connect(address.Text.Trim(),port.Text.Trim(),fingerprint.Text.Replace(":","").Replace(" ","").Trim(),token.Password.Trim());token.Clear();}
        catch(Exception ex){s.Abort(ex.Message);status.Text=ex.Message;}
        finally{connecting=false;}
    }
    void HandleMessage(Session s,JsonElement m)
    {
        if(session!=s||!m.TryGetProperty("type",out var t))return;
        switch(t.GetString())
        {
            case "catalog":windows.ItemsSource=m.GetProperty("windows").EnumerateArray().Select(w=>new WindowChoice(w.GetProperty("id").GetUInt32(),w.GetProperty("app").GetString()+" - "+w.GetProperty("title").GetString())).ToList();status.Text="Paired � select desktop or a window";break;
            case "ready":status.Text="Connected � view only � click Control Mac";RefreshContext();break;
            case "pong":if(m.TryGetProperty("stamp",out var stamp)){double ms=Stopwatch.GetElapsedTime(stamp.GetInt64()).TotalMilliseconds;if(ms<10000){status.Text=$"{(gate.CanRoute?"CONTROLLING MAC":"VIEW ONLY")} � RTT {ms:F0} ms � {(appView??desktop).VideoStats}";if(appWindow!=null)appWindow.Title=$"MAC-DOW � {(gate.CanRoute?"CONTROLLING":"VIEW ONLY")} � RTT {ms:F0} ms � Ctrl+Alt+Shift+Backspace releases";}}break;
            case "video":(appView??desktop).Configure(m);break;
            case "error":case "status":var msg=m.TryGetProperty("message",out var v)?v.GetString():"Mac status changed";status.Text=msg;if(m.TryGetProperty("paused",out var paused)&&paused.GetBoolean()){gate.Disarm();(appView??desktop).Clear();}break;
        }
    }
    void Start(bool app)
    {
        if(session==null||session.IsReady){status.Text="Connect again before starting a new stream.";return;}
        uint id=0;if(app){if(windows.SelectedItem is not WindowChoice selected){status.Text="Select a specific window first.";return;}id=selected.ID;}
        gate.Disarm();
        if(app)
        {
            remote=false;body.Child=null;appView=new RemoteView(HandlePointer,ReportVideo);
            appWindow=new Window{Title="MAC-DOW App Priority � VIEW ONLY",Width=1100,Height=740,MinWidth=400,MinHeight=300,Background=Brushes.Black,Foreground=Brushes.White,Content=RemotePanel(appView),WindowStartupLocation=WindowStartupLocation.CenterScreen};
            appWindow.Activated+=(_,_)=>RefreshContext();appWindow.Deactivated+=(_,_)=>gate.SetContext(session?.IsReady==true,true,false);
            appWindow.Closed+=(_,_)=>{gate.Disarm();session?.Abort("App window closed");appView.Dispose();appView=null;appWindow=null;if(!closing){Show();ShowDesktop();}};
            Hide();appWindow.Show();
        }
        else{remote=true;body.Child=RemotePanel(desktop);Position(false);}
        session.Send(new{type="start",window=id,width=(int)resolution.SelectedItem,fps=(int)fps.SelectedItem,quality=70,codec=codec.SelectedIndex==0?"h264":"jpeg",bitrate=(int)bitrate.SelectedItem});
        status.Text="Approve requested scope by typing ALLOW on the Mac.";
    }
    void Arm()
    {
        (appView??desktop).Focus();RefreshContext();
        if(!gate.Arm()){status.Text="Connect, wait for a fresh frame and focus the remote window first.";return;}
        if(!CanControlVideo()){gate.Disarm();status.Text="Waiting for fresh video; control remains off.";return;}
        try{keyboard.Install();controlGeneration++;UpdateControl();}catch(Exception ex){gate.Disarm();status.Text="Cannot acquire keyboard: "+ex.Message;}
    }
    bool OwnerIsForeground()=>Native.GetForegroundWindow()==new WindowInteropHelper(appWindow??this).Handle;
    void RefreshContext()=>gate.SetContext(session?.IsReady==true,remote||appWindow!=null,OwnerIsForeground());
    void UpdateControl(){control.Content=gate.CanRoute?"CONTROLLING MAC":"Control Mac";control.Background=gate.CanRoute?Brushes.DarkOrange:SystemColors.ControlBrush;(appView??desktop).SetArmed(gate.CanRoute);}
    void Escape(){gate.Disarm();if(appWindow==null)ShowDesktop();else{appWindow.Title="MAC-DOW App Priority � VIEW ONLY � control released";UpdateControl();}}
    bool CanControlVideo() {long shown=(appView??desktop).LastPresented;return shown>0&&session!=null&&session.NativeAlive>0&&Stopwatch.GetElapsedTime(session.NativeAlive).TotalSeconds<1.5&&(session.LastFrame<=shown||Stopwatch.GetElapsedTime(shown).TotalSeconds<1);}
    void Tick()
    {
        RefreshContext();if(gate.CanRoute&&!CanControlVideo())gate.Disarm();
        if(session?.IsReady==true){lastLease=Stopwatch.GetTimestamp();session.Send(new{type="lease",active=gate.CanRoute,generation=controlGeneration,view=remote||appWindow!=null,stamp=lastLease});}
    }
    void HandlePointer(JsonElement m)
    {
        if(m.TryGetProperty("type",out var eventType)&&eventType.GetString()=="blur"){gate.Disarm();return;}
        if(!gate.CanRoute||!OwnerIsForeground())return;
        var type=m.GetProperty("type").GetString();if(type is "move" or "button" or "scroll")session?.Send(m);
    }
    void ReportVideo(string stats){ if(stats.StartsWith("WebView2 runtime unavailable"))status.Text=stats; }
    void Disconnect(){gate.Disarm();if(session!=null){var old=session;session=null;old.Abort("Disconnected");_ = old.DisposeAsync();}desktop.Clear();appView?.Clear();windows.ItemsSource=null;status.Text="Disconnected � view only";}
    void RefreshMonitors(){var all=Native.Monitors();if(all.Count==0)return;monitors.ItemsSource=all;monitors.SelectedItem=DisplayLayout.Select(all,preferred);}
    void Position(bool? strip=null){var all=Native.Monitors();if(all.Count==0)return;Native.Position(this,DisplayLayout.Select(all,preferred),strip??!remote);}
    nint WndProc(nint hwnd,int msg,nint w,nint l,ref bool handled)
    {
        if(msg==0x312&&w==901){Escape();handled=true;}
        if(msg is 0x007E or 0x02E0){gate.Disarm();Dispatcher.BeginInvoke(()=>{RefreshMonitors();Position();});}
        return 0;
    }
    record WindowChoice(uint ID,string Label);
}

internal sealed class RemoteView : WebView2
{
    bool ready,armed,framePending;
    JsonElement? config;
    readonly Action<JsonElement> pointer;
    readonly Action<string> report;
    public long LastPresented{get;private set;}
    public string VideoStats{get;private set;}="waiting for video";
    public RemoteView(Action<JsonElement> pointer,Action<string> report){this.pointer=pointer;this.report=report;Loaded+=async(_,_)=>await Initialize();}
    async Task Initialize()
    {
        if(CoreWebView2!=null)return;
        try{
            _=CoreWebView2Environment.GetAvailableBrowserVersionString();
            string cache=Path.Combine(Path.GetTempPath(),"MAC-DOW-WebView",Environment.ProcessId.ToString());
            var env=await CoreWebView2Environment.CreateAsync(null,cache);
            await EnsureCoreWebView2Async(env);
            if(CoreWebView2==null)throw new InvalidOperationException("WebView2 initialization did not produce a runtime.");
            CoreWebView2.Settings.AreDevToolsEnabled=false;CoreWebView2.Settings.AreDefaultContextMenusEnabled=false;CoreWebView2.Settings.AreBrowserAcceleratorKeysEnabled=false;CoreWebView2.Settings.IsStatusBarEnabled=false;CoreWebView2.Settings.IsZoomControlEnabled=false;
            CoreWebView2.PermissionRequested+=(_,e)=>e.State=CoreWebView2PermissionState.Deny;
            CoreWebView2.NewWindowRequested+=(_,e)=>e.Handled=true;
            CoreWebView2.DownloadStarting+=(_,e)=>e.Cancel=true;
            CoreWebView2.NavigationStarting+=(_,e)=>{if(e.Uri!="https://macdow.local/viewer.html")e.Cancel=true;};
            CoreWebView2.SetVirtualHostNameToFolderMapping("macdow.local",AppContext.BaseDirectory,CoreWebView2HostResourceAccessKind.Deny);
            CoreWebView2.WebMessageReceived+=(_,e)=>{if(e.Source!="https://macdow.local/viewer.html")return;try{using var doc=JsonDocument.Parse(e.WebMessageAsJson);var m=doc.RootElement;var type=m.GetProperty("type").GetString();if(type=="loaded"){ready=true;if(config!=null)Configure(config.Value);SetArmed(armed);}else if(type=="presented")LastPresented=Stopwatch.GetTimestamp();else if(type=="frameAck")framePending=false;else if(type=="stats"){VideoStats=m.GetProperty("message").GetString()??"";report(VideoStats);}else pointer(m);}catch(JsonException){}};
            CoreWebView2.Navigate("https://macdow.local/viewer.html");
        }catch(Exception ex){VideoStats="WebView2 runtime unavailable: "+ex.Message;report(VideoStats);}
    }
    public void Configure(JsonElement m){config=m;if(ready)CoreWebView2.PostWebMessageAsJson(m.GetRawText());}
    public void Frame(byte[] b){if(ready&&!framePending){framePending=true;CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="frame",data=Convert.ToBase64String(b)}));}}
    public void SetArmed(bool value){armed=value;if(ready)CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="armed",active=value}));}
    public void Clear(){LastPresented=0;framePending=false;if(ready)CoreWebView2.PostWebMessageAsJson("{\"type\":\"clear\"}");}
}
