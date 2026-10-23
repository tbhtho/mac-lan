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

internal sealed partial class MainWindow : Window
{
    readonly DockPanel root=new();
    readonly Border body=new(){Background=new SolidColorBrush(Color.FromRgb(243,245,249))};
    readonly StackPanel setup=new(){Margin=new Thickness(32,28,32,24),MaxWidth=720,HorizontalAlignment=HorizontalAlignment.Stretch};
    readonly ComboBox nearby=new(){Width=440,DisplayMemberPath="Label"};
    readonly TextBlock pairingProgress=new(){TextWrapping=TextWrapping.Wrap,MaxWidth=570,Margin=new Thickness(0,10,0,12)};
    bool discovering;
    readonly ComboBox windows=new(){Width=340,DisplayMemberPath="Label"};
    readonly ComboBox monitors=new(){Width=140,DisplayMemberPath="Id"};
    readonly ComboBox codec=new(){Width=160},resolution=new(){Width=110},fps=new(){Width=80},bitrate=new(){Width=100};
    readonly TextBlock status=new(){Text="Ready to connect",VerticalAlignment=VerticalAlignment.Center,Foreground=new SolidColorBrush(Color.FromRgb(95,109,128)),Margin=new Thickness(20,8,20,8),FontSize=12};
    readonly Button updatesButton=new(){Content="Updates",Padding=new Thickness(12,5,12,5)};
    readonly DispatcherTimer updateTimer=new(){Interval=TimeSpan.FromHours(6)};
    bool checkingUpdates;
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
    readonly TabBar tabBar;
    readonly StackPanel streamSection=new(){Visibility=Visibility.Collapsed};
    readonly TextBlock viewProgress=new(){Text="Waiting for Mac video...",Foreground=Ink,FontSize=15,Margin=new Thickness(16,12,16,12)};
    Button findButton=null!,pairButton=null!;
    bool paired;
    public MainWindow()
    {
        Title="MAC-DOW - Connect your Mac";WindowStyle=WindowStyle.SingleBorderWindow;ResizeMode=ResizeMode.CanResize;Topmost=false;ShowInTaskbar=true;MinWidth=680;MinHeight=540;Width=840;Height=720;
        Content=root;
        updatesButton.Click+=(_,_)=>ShowUpdates();
        tabBar=new TabBar(ShowDesktop,ShowRemote,ShowSetup,ShowUpdates,Close,monitors,updatesButton,RequestControl,Escape,Disconnect,()=>session?.IsReady==true&&CanControlVideo());
        monitors.SelectionChanged+=(_,_)=>{if(monitors.SelectedItem is MonitorArea m){preferred=m.Id;gate.Disarm();tabBar.DockTo(m);if(remote)Position(false);}};
        DockPanel.SetDock(status,Dock.Bottom);root.Children.Add(status);
        root.Children.Add(body);desktop=new RemoteView(HandlePointer,ReportVideo);BuildSetup();
        keyboard=new KeyboardHook(()=>gate.CanRoute&&OwnerIsForeground(),(code,down)=>session?.Send(new{type="key",code,down}),Escape);
        gate.Released+=()=>{keyboard.Dispose();session?.Send(new{type="release"});UpdateControl();};
        control.Click+=(_,_)=>{if(gate.CanRoute)gate.Disarm();else Arm();};
        Loaded+=(_,_)=>{RefreshMonitors();ShowSetup();};
        SourceInitialized+=(_,_)=>{source=HwndSource.FromHwnd(new WindowInteropHelper(this).Handle);source.AddHook(WndProc);if(!Native.RegisterHotKey(source.Handle,901,0x4000|1|2|4,8))status.Text="Escape shortcut unavailable; close button and focus loss still release control.";};
        Deactivated+=(_,_)=>{if(appWindow==null)gate.SetContext(session?.IsReady==true,remote,false);};
        Activated+=(_,_)=>RefreshContext();
        timer.Tick+=(_,_)=>Tick();timer.Start();
        Loaded+=async(_,_)=>await CheckUpdates();updateTimer.Tick+=async(_,_)=>await CheckUpdates();updateTimer.Start();
        Closing+=(_,_)=>{closing=true;Telemetry.Report("shutdown");gate.Disarm();timer.Stop();updateTimer.Stop();Native.UnregisterHotKey(new WindowInteropHelper(this).Handle,901);source?.RemoveHook(WndProc);appWindow?.Close();session?.Abort("Closed");desktop.Dispose();tabBar.Dispose();};
    }
    Button MakeButton(string text,Action action){var b=new Button{Content=text,Margin=new Thickness(0,4,0,4),HorizontalAlignment=HorizontalAlignment.Left};b.Click+=(_,_)=>action();return b;}
    void ShowSetup()
    {
        gate.Disarm();remote=false;if(appWindow!=null){appWindow.Activate();return;}
        status.Visibility=Visibility.Visible;body.Background=new SolidColorBrush(Color.FromRgb(243,245,249));
        Title=paired?"MAC-DOW - Connected to your Mac":"MAC-DOW - Connect your Mac";
        WindowStyle=WindowStyle.SingleBorderWindow;ResizeMode=ResizeMode.CanResize;WindowState=WindowState.Normal;Width=840;Height=720;
        if(setup.Parent is ScrollViewer old)old.Content=null;body.Child=new ScrollViewer{Content=setup,VerticalScrollBarVisibility=ScrollBarVisibility.Auto,HorizontalScrollBarVisibility=ScrollBarVisibility.Disabled};
        tabBar.SetMode(true);Show();
        var selected=DisplayLayout.Select(Native.Monitors(),preferred);Left=(selected.Left+(selected.Width-Width*selected.Dpi/96)/2)*96/selected.Dpi;Top=(selected.Top+Math.Max(12,(selected.Height-Height*selected.Dpi/96)/2))*96/selected.Dpi;Activate();SendLease();
    }
    void ShowDesktop(){gate.Disarm();remote=false;body.Child=null;Hide();tabBar.SetMode(true);RefreshContext();SendLease();status.Text="Windows desktop. Control is released.";}
    void ShowRemote()
    {
        if(appWindow!=null){appWindow.Activate();return;}
        if(session?.IsPaired!=true){ShowSetup();return;}
        if(!session.IsReady&&!session.IsStarting){Start(false);return;}
        remote=true;WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;body.Child=RemotePanel(desktop);Show();Position(false);tabBar.SetMode(false);Activate();RefreshContext();SendLease();UpdateControl();
    }
    FrameworkElement RemotePanel(RemoteView view)
    {
        if(view.Parent is Panel previous)previous.Children.Remove(view);
        if(view.Parent is Border previousBorder)previousBorder.Child=null;
        status.Visibility=Visibility.Collapsed;
        if(ReferenceEquals(view,desktop)){body.Background=Brushes.Black;return view;}
        var panel=new DockPanel{Background=Brushes.White};var toolbar=new StackPanel{Orientation=Orientation.Horizontal,Background=Brushes.White,Margin=new Thickness(12,4,12,4)};
        // A single control button is reparented only after the previous surface has been detached.
        if(control.Parent is Panel old)old.Children.Remove(control);toolbar.Children.Add(control);
        toolbar.Children.Add(MakeButton("Updates",ShowUpdates));
        toolbar.Children.Add(MakeButton("Release",Escape));toolbar.Children.Add(MakeButton("Disconnect",()=>Disconnect()));
        toolbar.Children.Add(new TextBlock{Text="  Ctrl+Alt+Shift+Backspace releases control",FontSize=12,VerticalAlignment=VerticalAlignment.Center});
        DockPanel.SetDock(toolbar,Dock.Top);panel.Children.Add(toolbar);
        if(viewProgress.Parent is Panel previousNotice)previousNotice.Children.Remove(viewProgress);
        viewProgress.Visibility=view.LastPresented>0?Visibility.Collapsed:Visibility.Visible;
        DockPanel.SetDock(viewProgress,Dock.Top);panel.Children.Add(viewProgress);panel.Children.Add(view);return panel;
    }
    async Task FindMac()
    {
        if(discovering)return;discovering=true;findButton.IsEnabled=false;findButton.Content="Searching...";nearby.IsEnabled=false;pairButton.IsEnabled=false;pairingProgress.Text="Looking for your Mac on this Wi-Fi or Ethernet network...";
        try
        {
            var result=await UpdateProcess.Call("discover");
            pairingProgress.Text=result.GetProperty("message").GetString();
            var found=new List<DiscoveredHost>();
            if(result.GetProperty("ok").GetBoolean()&&result.TryGetProperty("hosts",out var hosts)&&hosts.ValueKind==JsonValueKind.Array)
                foreach(var h in hosts.EnumerateArray().Take(32))
                    if(DiscoveredHost.TryCreate(h.GetProperty("name").GetString()??"",h.GetProperty("address").GetString()??"",h.GetProperty("port").GetInt32(),h.GetProperty("fingerprint").GetString()??"",out var valid))found.Add(valid!);
            nearby.ItemsSource=found;if(found.Count==1)nearby.SelectedIndex=0;
            Telemetry.Report(found.Count==0?"discovery-empty":"discovery-ready","ok",found.Count);
        }
        catch(Exception e){Telemetry.Report("discovery-failed",e.GetType().Name);pairingProgress.Text="Could not search this network. Keep the Mac app open, check that both devices use the same network, then try again.";}
        finally{discovering=false;nearby.IsEnabled=!connecting&&nearby.Items.Count>0;findButton.IsEnabled=true;findButton.Content="Find my Mac";pairButton.IsEnabled=!paired&&!connecting&&nearby.SelectedItem is DiscoveredHost;}
    }
    async Task ConnectNearby()
    {
        if(connecting||paired)return;
        if(nearby.SelectedItem is not DiscoveredHost selected){pairingProgress.Text="Find and select your Mac first.";return;}
        string identity=Pairing.IdentityCode(selected.Fingerprint);
        if(MessageBox.Show(this,"Does MAC-DOW Host on your Mac show this exact identity?\n\n"+identity+"\n\nCompare every group directly on the Mac before continuing. The network name alone is not proof.","Compare Mac identity",MessageBoxButton.YesNo,MessageBoxImage.Question,MessageBoxResult.No)!=MessageBoxResult.Yes)return;
        connecting=true;Disconnect();
        pairButton.IsEnabled=false;pairButton.Content="Waiting for Mac...";nearby.IsEnabled=false;
        string number=System.Security.Cryptography.RandomNumberGenerator.GetInt32(100000,1000000).ToString();
        pairingProgress.Text="Pairing number: "+number+"\nOn the Mac, approve only the request showing this number. Approval expires in 45 seconds.";
        status.Text="Waiting for Mac approval: "+number;
        var s=CreateSession();
        Telemetry.Report("pairing-start");
        try{await s.ConnectGuided(selected,number);}
        catch(Exception e){connecting=false;pairButton.Content="Connect";pairButton.IsEnabled=nearby.SelectedItem is DiscoveredHost;nearby.IsEnabled=nearby.Items.Count>0;Telemetry.Report("pairing-failed",e.GetType().Name);s.Abort(e.Message);pairingProgress.Text=status.Text="Pairing failed. Keep MAC-DOW running on the Mac and approve the matching request there.";}
    }
    Session CreateSession()
    {
        var s=new Session();session=s;
        s.Message+=m=>Dispatcher.BeginInvoke(()=>HandleMessage(s,m));
        s.Frame+=b=>{Interlocked.Exchange(ref pendingFrame,b);if(Interlocked.Exchange(ref frameScheduled,1)==0)Dispatcher.BeginInvoke(()=>{var latest=Interlocked.Exchange(ref pendingFrame,null);Interlocked.Exchange(ref frameScheduled,0);if(latest!=null&&session==s&&s.IsReady&&(remote||appWindow!=null))(appView??desktop).Frame(latest);},DispatcherPriority.Background);};
        s.Ended+=why=>Dispatcher.BeginInvoke(()=>{if(session==s){paired=false;connecting=false;pairButton.Content="Connect";pairButton.IsEnabled=nearby.SelectedItem is DiscoveredHost;nearby.IsEnabled=nearby.Items.Count>0;streamSection.Visibility=Visibility.Collapsed;Telemetry.Report("stream-ended");gate.Disarm();(appView??desktop).Clear();status.Text=pairingProgress.Text=why;viewProgress.Text=why+" Open Connection to reconnect.";viewProgress.Visibility=Visibility.Visible;if(appWindow!=null)appWindow.Title="MAC-DOW - disconnected / view only";}});
        return s;
    }
    void HandleMessage(Session s,JsonElement m)
    {
        if(session!=s||!m.TryGetProperty("type",out var t))return;
        switch(t.GetString())
        {
            case "catalog":paired=true;connecting=false;Title="MAC-DOW - Connected to your Mac";pairButton.Content="Connected";pairButton.IsEnabled=false;streamSection.Visibility=Visibility.Visible;Telemetry.Report("pairing-approved");windows.ItemsSource=m.GetProperty("windows").EnumerateArray().Select(w=>new WindowChoice(w.GetProperty("id").GetUInt32(),w.GetProperty("app").GetString()+" - "+w.GetProperty("title").GetString())).ToList();status.Text=pairingProgress.Text="Connected. Click MacBook above to view the desktop, or choose a window below.";break;
            case "ready":Telemetry.Report("stream-started");viewProgress.Text="Mac approved viewing. Waiting for the first video frame...";viewProgress.Visibility=Visibility.Visible;status.Text="Mac approved viewing. Waiting for video; control stays off.";RefreshContext();SendLease();break;
            case "pong":if(m.TryGetProperty("stamp",out var stamp)){double ms=Stopwatch.GetElapsedTime(stamp.GetInt64()).TotalMilliseconds;if(ms<10000){status.Text=$"{(gate.CanRoute?"CONTROLLING MAC":"VIEW ONLY")} | RTT {ms:F0} ms | {(appView??desktop).VideoStats}";if(appWindow!=null)appWindow.Title=$"MAC-DOW | {(gate.CanRoute?"CONTROLLING":"VIEW ONLY")} | RTT {ms:F0} ms | Ctrl+Alt+Shift+Backspace releases";}}break;
            case "video":(appView??desktop).Configure(m);break;
            case "error":case "status":var msg=m.TryGetProperty("message",out var v)?v.GetString():"Mac status changed";status.Text=msg;if(t.GetString()=="error"){Telemetry.Report("capture-failed","host-error");viewProgress.Text=msg;viewProgress.Visibility=Visibility.Visible;}if(m.TryGetProperty("paused",out var paused)&&paused.GetBoolean()){gate.Disarm();(appView??desktop).Clear();viewProgress.Text=msg;viewProgress.Visibility=Visibility.Visible;}break;
        }
    }
    void Start(bool app)
    {
        if(session==null||!session.IsPaired){status.Text="Connect to your Mac first, then choose what to view.";return;}
        if(session.IsReady||session.IsStarting){ShowRemote();return;}
        if(session.MaxFPS<60){ShowSetup();status.Text=pairingProgress.Text=$"Update MAC-DOW on your Mac for 60 fps. The connected Mac host supports up to {session.MaxFPS} fps.";return;}
        uint id=0;if(app){if(windows.SelectedItem is not WindowChoice selected){status.Text="Select a specific window first.";return;}id=selected.ID;}
        gate.Disarm();viewProgress.Text="Approve viewing on your Mac. Video starts after you choose Allow.";viewProgress.Visibility=Visibility.Visible;
        if(app)
        {
            remote=false;body.Child=null;appView=new RemoteView(HandlePointer,ReportVideo);
            appWindow=new Window{Title="MAC-DOW App Priority - View only",Width=1100,Height=740,MinWidth=400,MinHeight=300,Background=Brushes.Black,Content=RemotePanel(appView),WindowStartupLocation=WindowStartupLocation.CenterScreen};
            appWindow.Activated+=(_,_)=>RefreshContext();appWindow.Deactivated+=(_,_)=>gate.SetContext(session?.IsReady==true,true,false);
            appWindow.Closed+=(_,_)=>{gate.Disarm();session?.Abort("App window closed");appView.Dispose();appView=null;appWindow=null;if(!closing){tabBar.DockTo(DisplayLayout.Select(Native.Monitors(),preferred));ShowDesktop();}};
            Hide();tabBar.Suspend();appWindow.Show();
        }
        else{remote=true;WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;body.Child=RemotePanel(desktop);Show();Position(false);tabBar.SetMode(false);}
        var profile=codec.SelectedItem as StreamProfile??StreamProfile.DefaultHardware;
        session.Send(profile.CreateStart(id,(int)resolution.SelectedItem,(int)fps.SelectedItem,(int)bitrate.SelectedItem));
        Telemetry.Report("stream-request");status.Text=pairingProgress.Text="Waiting for you to choose Allow on your Mac. Video starts after that approval.";
    }
    void Arm()
    {
        (appView??desktop).Focus();RefreshContext();
        if(!gate.Arm()){status.Text="Connect, wait for a fresh frame and focus the remote window first.";return;}
        if(!CanControlVideo()){gate.Disarm();status.Text="Waiting for fresh video; control remains off.";return;}
        try{keyboard.Install();controlGeneration++;SendLease();UpdateControl();}catch(Exception ex){gate.Disarm();status.Text="Cannot acquire keyboard: "+ex.Message;}
    }
    void RequestControl(){if(session?.IsReady!=true)return;ShowRemote();Dispatcher.BeginInvoke(()=>{(appWindow??this).Activate();Arm();},DispatcherPriority.Input);}
    bool OwnerIsForeground()=>Native.GetForegroundWindow()==new WindowInteropHelper(appWindow??this).Handle;
    void RefreshContext()=>gate.SetContext(session?.IsReady==true,remote||appWindow!=null,OwnerIsForeground());
    void UpdateControl(){control.Content=gate.CanRoute?"Stop control":"Control Mac";control.Background=gate.CanRoute?Brushes.DarkOrange:Brushes.White;control.Foreground=gate.CanRoute?Brushes.White:Ink;control.IsEnabled=gate.CanRoute||session?.IsReady==true&&CanControlVideo();control.ToolTip=control.IsEnabled?"Keyboard and mouse control is optional.":"Available once fresh Mac video arrives.";(appView??desktop).SetArmed(gate.CanRoute);}
    void Escape(){gate.Disarm();if(appWindow==null)ShowDesktop();else{appWindow.Title="MAC-DOW App Priority | VIEW ONLY | control released";UpdateControl();}}
    bool CanControlVideo() {long shown=(appView??desktop).LastPresented;return shown>0&&session!=null&&session.NativeAlive>0&&Stopwatch.GetElapsedTime(session.NativeAlive).TotalSeconds<1.5&&(session.LastFrame<=shown||Stopwatch.GetElapsedTime(shown).TotalSeconds<1);}
    void SendLease(){if(session?.IsReady==true){lastLease=Stopwatch.GetTimestamp();session.Send(new{type="lease",active=gate.CanRoute,generation=controlGeneration,view=remote||appWindow!=null,stamp=lastLease});}}
    void Tick()
    {
        RefreshContext();if(gate.CanRoute&&!CanControlVideo())gate.Disarm();UpdateControl();tabBar.SetConnection(paired,gate.CanRoute,session?.IsStarting==true?"Waiting for Mac approval":session?.IsReady==true&&(appView??desktop).LastPresented==0?"Waiting for video":null);
        if((appView??desktop).LastPresented>0)viewProgress.Visibility=Visibility.Collapsed;
        SendLease();
    }
    void HandlePointer(JsonElement m)
    {
        if(m.TryGetProperty("type",out var eventType)&&eventType.GetString()=="blur"){gate.Disarm();return;}
        if(!gate.CanRoute||!OwnerIsForeground())return;
        var type=m.GetProperty("type").GetString();if(type is "move" or "button" or "scroll")session?.Send(m);
    }
    void ReportVideo(string stats){ if(stats.StartsWith("WebView2 runtime unavailable")){status.Text=stats;viewProgress.Text=stats;viewProgress.Visibility=Visibility.Visible;Telemetry.Report("decode-failed","WebView2");} }
    void Disconnect(){paired=false;pairButton.Content="Connect";pairButton.IsEnabled=!connecting&&nearby.SelectedItem is DiscoveredHost;streamSection.Visibility=Visibility.Collapsed;gate.Disarm();if(session!=null){var old=session;session=null;old.Abort("Disconnected");_ = old.DisposeAsync();}desktop.Clear();appView?.Clear();windows.ItemsSource=null;status.Text="Disconnected. Control is released.";}
    void RefreshMonitors(){var all=Native.Monitors();if(all.Count==0)return;monitors.ItemsSource=all;monitors.SelectedItem=DisplayLayout.Select(all,preferred);}
    void Position(bool? strip=null){var all=Native.Monitors();if(all.Count==0||!remote)return;Native.Position(this,DisplayLayout.Select(all,preferred),false);}
    nint WndProc(nint hwnd,int msg,nint w,nint l,ref bool handled)
    {
        if(msg==0x312&&w==901){Escape();handled=true;}
        if(msg is 0x007E or 0x02E0){gate.Disarm();Dispatcher.BeginInvoke(()=>{RefreshMonitors();Position();});}
        return 0;
    }
    void ShowUpdates(){gate.Disarm();var dialog=new UpdateDialog(appWindow??this,()=>{Disconnect();Close();});dialog.ShowDialog();}
    async Task CheckUpdates()
    {
        if(checkingUpdates||closing)return;var preferences=UpdatePreferences.Load();if(!preferences.Automatic)return;checkingUpdates=true;
        try {var result=await UpdateProcess.DownloadAvailable(preferences);if(result.TryGetProperty("stage",out var staged))updatesButton.Content=staged.GetProperty("canApply").GetBoolean()?"Restart to update":"Update needs attention";updatesButton.ToolTip=result.GetProperty("message").GetString();}
        catch(Exception e){updatesButton.ToolTip="Update check unavailable: "+e.Message;}
        finally{checkingUpdates=false;}
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
            CoreWebView2.SetVirtualHostNameToFolderMapping("macdow.local",Payloads.Root,CoreWebView2HostResourceAccessKind.Deny);
            CoreWebView2.WebMessageReceived+=(_,e)=>{if(e.Source!="https://macdow.local/viewer.html")return;try{using var doc=JsonDocument.Parse(e.WebMessageAsJson);var m=doc.RootElement;var type=m.GetProperty("type").GetString();if(type=="loaded"){ready=true;if(config!=null)Configure(config.Value);SetArmed(armed);}else if(type=="presented")LastPresented=Stopwatch.GetTimestamp();else if(type=="frameAck")framePending=false;else if(type=="stats"){VideoStats=m.GetProperty("message").GetString()??"";report(VideoStats);}else pointer(m);}catch(JsonException){}};
            CoreWebView2.Navigate("https://macdow.local/viewer.html");
        }catch(Exception ex){VideoStats="WebView2 runtime unavailable: "+ex.Message;report(VideoStats);}
    }
    public void Configure(JsonElement m){config=m;if(ready)CoreWebView2.PostWebMessageAsJson(m.GetRawText());}
    public void Frame(byte[] b){if(ready&&!framePending){framePending=true;CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="frame",data=Convert.ToBase64String(b)}));}}
    public void SetArmed(bool value){armed=value;if(ready)CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="armed",active=value}));}
    public void Clear(){LastPresented=0;framePending=false;if(ready)CoreWebView2.PostWebMessageAsJson("{\"type\":\"clear\"}");}
}
