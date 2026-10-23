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
    readonly Border body=new(){Background=Brushes.Transparent};
    readonly StackPanel setup=new(){Margin=new Thickness(32,28,32,24),MaxWidth=720,HorizontalAlignment=HorizontalAlignment.Stretch};
    readonly ListBox nearby=new();
    readonly TextBlock pairingProgress=new(){TextWrapping=TextWrapping.Wrap,MaxWidth=570,Margin=new Thickness(0,10,0,12)};
    bool discovering;
    readonly ComboBox windows=new(){Width=340,DisplayMemberPath="Label"};
    readonly ComboBox monitors=new(){Width=140,DisplayMemberPath="Id"};
    readonly ComboBox codec=new(){Width=160},resolution=new(){Width=110},fps=new(){Width=80},bitrate=new(){Width=100};
    readonly TextBlock status=new(){Text="Ready to connect",VerticalAlignment=VerticalAlignment.Center,Foreground=Glass.Muted,Margin=new Thickness(20,8,20,10),FontSize=12};
    readonly Button updatesButton=new(){Content="Updates",Padding=new Thickness(12,5,12,5)};
    readonly DispatcherTimer updateTimer=new(){Interval=TimeSpan.FromHours(6)};
    bool checkingUpdates,showingUpdates;
    readonly Button control=new(){Content="Control Mac",Padding=new Thickness(14,5,14,5)};
    readonly ControlGate gate=new();
    readonly KeyboardHook keyboard;
    readonly DispatcherTimer timer=new(){Interval=TimeSpan.FromMilliseconds(250)};
    readonly RemoteView desktop;
    Session? session;
    RemoteView? appView;
    Window? appWindow;
    string appLabel="";
    bool appSized;
    FileTransferWindow? filesWindow;
    bool remote,closing,connecting,openingFiles,awaitingFileTransfer;
    string? preferred;
    long lastLease;
    ulong controlGeneration;
    bool? controlPermission;
    string? controlIssue;
    HwndSource? source;
    readonly TabBar tabBar;
    readonly StackPanel streamSection=new(){Visibility=Visibility.Collapsed};
    readonly TextBlock viewProgress=new(){Text="Waiting for Mac video...",Foreground=Ink,FontSize=15,Margin=new Thickness(16,12,16,12)};
    Button pairButton=null!;
    readonly DispatcherTimer discoveryTimer=new(){Interval=TimeSpan.FromSeconds(8)};
    int lastFound=-1;
    bool paired;
    public MainWindow()
    {
        Title="mac-lan - Connect your Mac";WindowStyle=WindowStyle.SingleBorderWindow;ResizeMode=ResizeMode.CanResize;Topmost=false;ShowInTaskbar=true;MinWidth=680;MinHeight=540;Width=840;Height=720;
        Glass.Apply(this);Content=root;
        updatesButton.Click+=(_,_)=>ShowUpdates();
        tabBar=new TabBar(ShowDesktop,ShowRemote,ShowSetup,ShowUpdates,Close,monitors,updatesButton,RequestControl,Escape,Disconnect,()=>session?.IsReady==true&&CanControlVideo(),ShowFiles);
        monitors.SelectionChanged+=(_,_)=>{if(monitors.SelectedItem is MonitorArea m){preferred=m.Id;gate.Disarm("monitor-change");tabBar.DockTo(m);if(remote)Position(false);}};
        DockPanel.SetDock(status,Dock.Bottom);root.Children.Add(status);
        root.Children.Add(body);desktop=new RemoteView(HandlePointer,ReportVideo);desktop.VideoInvalidated+=()=>gate.Disarm("video-stale");BuildSetup();
        keyboard=new KeyboardHook(()=>gate.CanRoute&&OwnerIsForeground(),(code,down)=>session?.Send(new{type="key",code,down}),Escape);
        gate.Released+=reason=>{keyboard.Release();if(reason!=null)session?.Send(new{type="release",reason});else session?.Send(new{type="release"});UpdateControl();};
        control.Click+=(_,_)=>{if(gate.CanRoute)gate.Disarm("manual");else Arm();};
        Loaded+=(_,_)=>{RefreshMonitors();ShowSetup();_=FindMac();discoveryTimer.Start();};
        discoveryTimer.Tick+=async(_,_)=>{if(IsVisible&&!remote)await FindMac();};
        SourceInitialized+=(_,_)=>{source=HwndSource.FromHwnd(new WindowInteropHelper(this).Handle);source.AddHook(WndProc);if(!Native.RegisterHotKey(source.Handle,901,0x4000|1|2|4,8))status.Text="Escape shortcut unavailable; close button and focus loss still release control.";};
        Deactivated+=(_,_)=>{if(appWindow==null)gate.SetContext(session?.IsReady==true,remote,false,"window-deactivated");};
        Activated+=(_,_)=>RefreshContext();
        timer.Tick+=(_,_)=>Tick();timer.Start();
        Loaded+=async(_,_)=>await CheckUpdates();updateTimer.Tick+=async(_,_)=>await CheckUpdates();updateTimer.Start();
        Closing+=async(_,e)=>
        {
            if(closing)return;
            if(awaitingFileTransfer){e.Cancel=true;return;}
            if(filesWindow?.IsBusy==true)
            {
                // Keep the process alive until cancellation has closed the upload and removed any temporary folder ZIP.
                e.Cancel=true;awaitingFileTransfer=true;gate.Disarm("shutdown");session?.Abort("Closed");
                await filesWindow.CancelAndWaitAsync();awaitingFileTransfer=false;Close();return;
            }
            closing=true;Telemetry.Report("shutdown");gate.Disarm("shutdown");keyboard.Dispose();timer.Stop();updateTimer.Stop();discoveryTimer.Stop();Native.UnregisterHotKey(new WindowInteropHelper(this).Handle,901);source?.RemoveHook(WndProc);appWindow?.Close();session?.Abort("Closed");filesWindow?.Close();desktop.Dispose();tabBar.Dispose();
        };
    }
    Button MakeButton(string text,Action action){var b=new Button{Content=text,Margin=new Thickness(0,4,0,4),HorizontalAlignment=HorizontalAlignment.Left};b.Click+=(_,_)=>action();return b;}
    async void ShowFiles()
    {
        if(openingFiles||closing||awaitingFileTransfer)return;
        openingFiles=true;
        try
        {
            gate.Disarm("files-open");SendLease();var previous=filesWindow;
            if(previous!=null&&ReferenceEquals(previous.BoundSession,session)){previous.Activate();return;}
            if(previous!=null){await previous.CancelAndWaitAsync();previous.Close();}
            if(closing||awaitingFileTransfer)return;
            var current=new FileTransferWindow(session);filesWindow=current;current.Closed+=(_,_)=>{if(ReferenceEquals(filesWindow,current))filesWindow=null;};current.Show();current.Activate();
        }
        finally{openingFiles=false;}
    }
    void ShowSetup()
    {
        gate.Disarm("tab-change");remote=false;if(appWindow!=null){appWindow.Activate();return;}
        status.Visibility=Visibility.Visible;body.Background=Brushes.Transparent;
        Title=paired?"mac-lan - Connected to your Mac":"mac-lan - Connect your Mac";
        WindowStyle=WindowStyle.SingleBorderWindow;ResizeMode=ResizeMode.CanResize;WindowState=WindowState.Normal;Width=840;Height=720;
        if(setup.Parent is ScrollViewer old)old.Content=null;body.Child=new ScrollViewer{Content=setup,VerticalScrollBarVisibility=ScrollBarVisibility.Auto,HorizontalScrollBarVisibility=ScrollBarVisibility.Disabled};
        tabBar.SetMode(true);Show();
        var selected=DisplayLayout.Select(Native.Monitors(),preferred);Left=(selected.Left+(selected.Width-Width*selected.Dpi/96)/2)*96/selected.Dpi;Top=(selected.Top+Math.Max(12,(selected.Height-Height*selected.Dpi/96)/2))*96/selected.Dpi;Activate();SendLease();
    }
    void ShowDesktop(){gate.Disarm("tab-change");remote=false;body.Child=null;Hide();tabBar.SetMode(true);RefreshContext();SendLease();status.Text="Windows desktop. Control is released.";}
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
        // App Priority is the Mac app's own window on Windows: no mac-lan chrome inside it.
        // Click to control; Ctrl+Alt+Shift+Backspace or focus loss releases; closing disconnects.
        var panel=new DockPanel{Background=Brushes.Black};
        if(viewProgress.Parent is Panel previousNotice)previousNotice.Children.Remove(viewProgress);
        viewProgress.Visibility=view.LastPresented>0?Visibility.Collapsed:Visibility.Visible;
        DockPanel.SetDock(viewProgress,Dock.Top);panel.Children.Add(viewProgress);panel.Children.Add(view);return panel;
    }
    // Discovery runs by itself while the setup page is open and nothing is paired; a refresh keeps the chosen Mac.
    async Task FindMac()
    {
        if(discovering||paired||connecting||closing)return;discovering=true;
        if(nearby.Items.Count==0)pairingProgress.Text="Looking for your Mac on this Wi-Fi or Ethernet network...";
        try
        {
            var result=await UpdateProcess.Call("discover");
            var found=new List<DiscoveredHost>();
            if(result.GetProperty("ok").GetBoolean()&&result.TryGetProperty("hosts",out var hosts)&&hosts.ValueKind==JsonValueKind.Array)
                foreach(var h in hosts.EnumerateArray().Take(32))
                    if(DiscoveredHost.TryCreate(h.GetProperty("name").GetString()??"",h.GetProperty("address").GetString()??"",h.GetProperty("port").GetInt32(),h.GetProperty("fingerprint").GetString()??"",out var valid))found.Add(valid!);
            if(paired||connecting)return;
            string? previous=(nearby.SelectedItem as DiscoveredHost)?.Fingerprint;
            nearby.ItemsSource=found;nearby.SelectedItem=found.FirstOrDefault(h=>h.Fingerprint==previous)??(found.Count==1?found[0]:null);
            pairingProgress.Text=found.Count==0?(result.GetProperty("message").GetString()??"")+" This list refreshes automatically.":found.Count==1?"Found your Mac. Choose Connect.":"Choose your Mac, then Connect.";
            if(found.Count!=lastFound){lastFound=found.Count;Telemetry.Report(found.Count==0?"discovery-empty":"discovery-ready","ok",found.Count);}
        }
        catch(Exception e){Telemetry.Report("discovery-failed",e.GetType().Name);if(!paired&&!connecting)pairingProgress.Text="Could not search this network yet. Keep the Mac app open on the same network; mac-lan keeps looking.";}
        finally{discovering=false;nearby.IsEnabled=!connecting&&!paired&&nearby.Items.Count>0;pairButton.IsEnabled=!paired&&!connecting&&nearby.SelectedItem is DiscoveredHost;}
    }
    async Task ConnectNearby()
    {
        if(connecting||paired)return;
        if(nearby.SelectedItem is not DiscoveredHost selected){pairingProgress.Text="Find and select your Mac first.";return;}
        string identity=Pairing.IdentityCode(selected.Fingerprint);
        if(MessageBox.Show(this,"Does mac-lan Host on your Mac show this exact identity?\n\n"+identity+"\n\nCompare every group directly on the Mac before continuing. The network name alone is not proof.","Compare Mac identity",MessageBoxButton.YesNo,MessageBoxImage.Question,MessageBoxResult.No)!=MessageBoxResult.Yes)return;
        connecting=true;Disconnect();
        pairButton.IsEnabled=false;pairButton.Content="Waiting for Mac...";nearby.IsEnabled=false;
        string number=System.Security.Cryptography.RandomNumberGenerator.GetInt32(100000,1000000).ToString();
        pairingProgress.Text="Pairing number: "+number+"\nOn the Mac, approve only the request showing this number. Approval expires in 45 seconds.";
        status.Text="Waiting for Mac approval: "+number;
        var s=CreateSession();
        Telemetry.Report("pairing-start");
        try{await s.ConnectGuided(selected,number);}
        catch(Exception e){connecting=false;pairButton.Content="Connect";pairButton.IsEnabled=nearby.SelectedItem is DiscoveredHost;nearby.IsEnabled=nearby.Items.Count>0;Telemetry.Report("pairing-failed",e.GetType().Name);s.Abort(e.Message);pairingProgress.Text=status.Text="Pairing failed. Keep mac-lan running on the Mac and approve the matching request there.";}
    }
    Session CreateSession()
    {
        var s=new Session();session=s;controlPermission=null;controlIssue=null;
        s.Message+=m=>Dispatcher.BeginInvoke(()=>HandleMessage(s,m));
        // Each connection owns its dependency queue and scheduling flag. A late
        // callback from an old connection cannot consume a new stream's frames.
        var frames=new EncodedFrameQueue();int frameScheduled=0;
        bool CurrentStream()=>session==s&&s.IsReady&&(remote||appWindow!=null);
        s.Frame+=b=>
        {
            frames.Enqueue(b,Environment.TickCount64);
            if(Interlocked.Exchange(ref frameScheduled,1)!=0)return;
            Dispatcher.BeginInvoke(()=>
            {
                Interlocked.Exchange(ref frameScheduled,0);
                if(CurrentStream())(appView??desktop).Frames(frames,CurrentStream);
                else frames.Clear();
            },DispatcherPriority.Background);
        };
        s.Ended+=why=>Dispatcher.BeginInvoke(()=>{if(session==s){paired=false;connecting=false;pairButton.Content="Connect";pairButton.IsEnabled=nearby.SelectedItem is DiscoveredHost;nearby.IsEnabled=nearby.Items.Count>0;streamSection.Visibility=Visibility.Collapsed;Telemetry.Report("stream-ended");gate.Disarm("disconnected");(appView??desktop).Clear();status.Text=pairingProgress.Text=why;viewProgress.Text=why+" Open Connection to reconnect.";viewProgress.Visibility=Visibility.Visible;if(appWindow!=null)appWindow.Title=AppTitle();}});
        return s;
    }
    void HandleMessage(Session s,JsonElement m)
    {
        if(session!=s||!m.TryGetProperty("type",out var t))return;
        switch(t.GetString())
        {
            case "catalog":paired=true;connecting=false;Title="mac-lan - Connected to your Mac";pairButton.Content="Connected";pairButton.IsEnabled=false;streamSection.Visibility=Visibility.Visible;Telemetry.Report("pairing-approved");windows.ItemsSource=m.GetProperty("windows").EnumerateArray().Select(w=>new WindowChoice(w.GetProperty("id").GetUInt32(),w.GetProperty("app").GetString()+" - "+w.GetProperty("title").GetString(),w.GetProperty("app").GetString()??"Mac app")).ToList();status.Text=pairingProgress.Text=s.VideoError is string videoError?"Connected for files. Video is unavailable on your Mac: "+videoError:"Connected. Use MacBook to view the desktop, or Files to send items to your Mac.";SendLease();break;
            case "ready":Telemetry.Report("stream-started");viewProgress.Text="Mac approved viewing. Waiting for the first video frame...";viewProgress.Visibility=Visibility.Visible;status.Text="Mac approved viewing. Waiting for video; control stays off.";RefreshContext();SendLease();break;
            case "pong":if(m.TryGetProperty("stamp",out var stamp)){double ms=Stopwatch.GetElapsedTime(stamp.GetInt64()).TotalMilliseconds;if(ms<10000){status.Text=$"{(gate.CanRoute?"CONTROLLING MAC":"VIEW ONLY")} | RTT {ms:F0} ms | {(appView??desktop).VideoStats}";if(appWindow!=null)appWindow.Title=AppTitle();}}break;
            case "video":(appView??desktop).Configure(m);if(appView!=null&&!appSized)FitAppWindow(m);break;
            case "controlStatus":HandleControlStatus(m);break;
            case "error":case "status":var msg=m.TryGetProperty("message",out var v)?v.GetString():"Mac status changed";status.Text=msg;if(t.GetString()=="error"){Telemetry.Report("capture-failed","host-error");viewProgress.Text=msg;viewProgress.Visibility=Visibility.Visible;}if(m.TryGetProperty("paused",out var paused)&&paused.GetBoolean()){gate.Disarm("mac-paused");(appView??desktop).Clear();viewProgress.Text=msg;viewProgress.Visibility=Visibility.Visible;}break;
        }
    }
    void Start(bool app)
    {
        if(session==null||!session.IsPaired){status.Text="Connect to your Mac first, then choose what to view.";return;}
        if(session.VideoError is string videoError){ShowSetup();status.Text=pairingProgress.Text="Connected for files. Video is unavailable on your Mac: "+videoError;return;}
        if(session.IsReady||session.IsStarting){ShowRemote();return;}
        if(session.MaxFPS<60){ShowSetup();status.Text=pairingProgress.Text=$"Update mac-lan on your Mac for 60 fps. The connected Mac host supports up to {session.MaxFPS} fps.";return;}
        uint id=0;if(app){if(windows.SelectedItem is not WindowChoice selected){status.Text="Select a specific window first.";return;}id=selected.ID;appLabel=selected.App;appSized=false;}
        gate.Disarm("stream-start");viewProgress.Text="Approve viewing on your Mac. Video starts after you choose Allow.";viewProgress.Visibility=Visibility.Visible;
        if(app)
        {
            remote=false;body.Child=null;appView=new RemoteView(HandlePointer,ReportVideo);appView.VideoInvalidated+=()=>gate.Disarm("video-stale");
            appWindow=new Window{Title=appLabel,Width=1100,Height=740,MinWidth=400,MinHeight=300,Content=RemotePanel(appView),WindowStartupLocation=WindowStartupLocation.CenterScreen};Glass.Apply(appWindow);
            appWindow.Activated+=(_,_)=>RefreshContext();appWindow.Deactivated+=(_,_)=>gate.SetContext(session?.IsReady==true,true,false,"window-deactivated");
            appWindow.Closed+=(_,_)=>{gate.Disarm("disconnected");session?.Abort("App window closed");appView.Dispose();appView=null;appWindow=null;if(!closing){tabBar.DockTo(DisplayLayout.Select(Native.Monitors(),preferred));ShowDesktop();}};
            Hide();tabBar.Suspend();appWindow.Show();
        }
        else{remote=true;WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;body.Child=RemotePanel(desktop);Show();Position(false);tabBar.SetMode(false);}
        var profile=codec.SelectedItem as StreamProfile??StreamProfile.DefaultHardware;
        session.Send(profile.CreateStart(id,(int)resolution.SelectedItem,(int)fps.SelectedItem,(int)bitrate.SelectedItem));
        Telemetry.Report("stream-request");status.Text=pairingProgress.Text="Waiting for you to choose Allow on your Mac. Video starts after that approval.";
    }
    void Arm(bool focusViewer=true)
    {
        if(focusViewer)(appView??desktop).Focus();RefreshContext();
        if(!gate.Arm()){status.Text="Connect, wait for a fresh frame and focus the remote window first.";return;}
        if(ControlVideoBlockReason() is string blocked){gate.Disarm(blocked);status.Text="Waiting for fresh video; control remains off.";return;}
        try{keyboard.Install();controlIssue=null;controlGeneration++;SendLease();UpdateControl();}catch(Exception ex){gate.Disarm("keyboard-unavailable");status.Text="Cannot acquire keyboard: "+ex.Message;}
    }
    void RequestControl(){if(session?.IsReady!=true)return;ShowRemote();Dispatcher.BeginInvoke(()=>{(appWindow??this).Activate();Arm();},DispatcherPriority.Input);}
    bool OwnerIsForeground()=>Native.GetForegroundWindow()==new WindowInteropHelper(appWindow??this).Handle;
    void RefreshContext()=>gate.SetContext(session?.IsReady==true,remote||appWindow!=null,OwnerIsForeground());
    void UpdateControl(){control.Content=gate.CanRoute?"Stop control":"Control Mac";control.Background=gate.CanRoute?Glass.Warn:Glass.Fill;control.Foreground=gate.CanRoute?Brushes.Black:Ink;control.IsEnabled=gate.CanRoute||session?.IsReady==true&&CanControlVideo();control.ToolTip=control.IsEnabled?"Keyboard and mouse control is optional.":"Available once fresh Mac video arrives.";(appView??desktop).SetArmed(gate.CanRoute);}
    void Escape(){gate.Disarm("manual");if(appWindow==null)ShowDesktop();else{appWindow.Title=AppTitle();UpdateControl();}}
    string? ControlVideoBlockReason()=>session==null?"disconnected":VideoFreshness.BlockReason(controlPermission,(appView??desktop).LastPresented,session.NativeAlive,session.LastFrame,Stopwatch.GetTimestamp(),Stopwatch.Frequency);
    bool CanControlVideo()=>ControlVideoBlockReason()==null;
    void SendLease(){if(session?.IsReady==true||session?.IsPaired==true&&session.CanTransfer){bool ready=session.IsReady;lastLease=Stopwatch.GetTimestamp();session.Send(new{type="lease",active=ready&&gate.CanRoute,generation=controlGeneration,view=ready&&(remote||appWindow!=null),stamp=lastLease});}}
    void Tick()
    {
        RefreshContext();string? videoBlocked=ControlVideoBlockReason();if(gate.CanRoute&&videoBlocked!=null)gate.Disarm(videoBlocked);UpdateControl();tabBar.SetConnection(paired,gate.CanRoute,session?.IsStarting==true?"Waiting for Mac approval":session?.IsReady==true&&(appView??desktop).LastPresented==0?"Waiting for video":session?.IsReady==true&&(remote||appWindow!=null)&&!gate.CanRoute?controlIssue??(videoBlocked is "video-stale" or "native-heartbeat-stale"?"Video catching up":"Click Mac to control"):null);
        if((appView??desktop).LastPresented>0)viewProgress.Visibility=Visibility.Collapsed;
        if(appWindow!=null)appWindow.Title=AppTitle();
        SendLease();
    }
    void HandlePointer(JsonElement m)
    {
        if(m.TryGetProperty("type",out var eventType)&&eventType.GetString()=="blur"){gate.Disarm(m.TryGetProperty("reason",out var reason)&&reason.ValueKind==JsonValueKind.String&&reason.GetString()=="pointer-cancel"?"pointer-cancel":"viewer-blur");return;}
        if(eventType.ValueKind==JsonValueKind.String&&eventType.GetString()=="requestControl")
        {
            // The first desktop click is an explicit local request, consumed by the viewer.
            // Never activate a window or route input from an unfocused or stale view.
            if(session?.IsReady==true&&(remote||appWindow!=null)&&OwnerIsForeground()&&CanControlVideo())Arm(false);
            return;
        }
        if(!gate.CanRoute||!OwnerIsForeground())return;
        var type=m.GetProperty("type").GetString();if(type is "move" or "button" or "scroll")session?.Send(m);
    }
    void HandleControlStatus(JsonElement m)
    {
        if(!m.TryGetProperty("code",out var code)||code.ValueKind!=JsonValueKind.String)return;
        bool wasDenied=controlPermission==false;
        if(m.TryGetProperty("controlPermission",out var permission)&&permission.ValueKind is JsonValueKind.True or JsonValueKind.False)controlPermission=permission.GetBoolean();
        if(wasDenied&&controlPermission==true)controlIssue=null;
        if(controlPermission==false)
        {
            controlIssue="Allow Accessibility on your Mac to control";
            if(gate.CanRoute)gate.Disarm("mac-permission");
            status.Text="On your Mac, open System Settings > Privacy & Security > Accessibility and allow mac-lan. Then click the Mac desktop again.";
            UpdateControl();return;
        }
        switch(code.GetString())
        {
            case "ready":controlIssue=null;break;
            case "accessibility-missing":controlIssue="Allow Accessibility on your Mac to control";gate.Disarm("mac-permission");status.Text="On your Mac, open System Settings > Privacy & Security > Accessibility and allow mac-lan. Then click the Mac desktop again.";break;
            case "scope-blocked":controlIssue="Control paused: refocus the selected Mac window";gate.Disarm("mac-scope");break;
            case "lease-expired":case "stale-input":case "generation-revoked":controlIssue="Control paused: click Mac to control";gate.Disarm("mac-watchdog");break;
        }
        UpdateControl();
    }
    void ReportVideo(string stats){ if(stats.StartsWith("WebView2 runtime unavailable")){status.Text=stats;viewProgress.Text=stats;viewProgress.Visibility=Visibility.Visible;Telemetry.Report("decode-failed","WebView2");} }
    void Disconnect(){paired=false;pairButton.Content="Connect";pairButton.IsEnabled=!connecting&&nearby.SelectedItem is DiscoveredHost;streamSection.Visibility=Visibility.Collapsed;gate.Disarm("disconnected");if(session!=null){var old=session;session=null;old.Abort("Disconnected");_ = old.DisposeAsync();}desktop.Clear();appView?.Clear();windows.ItemsSource=null;status.Text="Disconnected. Control is released.";}
    void RefreshMonitors(){var all=Native.Monitors();if(all.Count==0)return;monitors.ItemsSource=all;monitors.SelectedItem=DisplayLayout.Select(all,preferred);}
    void Position(bool? strip=null){var all=Native.Monitors();if(all.Count==0||!remote)return;Native.Position(this,DisplayLayout.Select(all,preferred),false);}
    nint WndProc(nint hwnd,int msg,nint w,nint l,ref bool handled)
    {
        if(msg==0x312&&w==901){Escape();handled=true;}
        if(msg is 0x007E or 0x02E0){gate.Disarm("display-change");Dispatcher.BeginInvoke(()=>{RefreshMonitors();Position();});}
        return 0;
    }
    void ShowUpdates()
    {
        if(showingUpdates||closing||awaitingFileTransfer)return;
        showingUpdates=true;gate.Disarm("updates-open");
        try{var dialog=new UpdateDialog(appWindow??this,PrepareUpdateRestart,()=>{Disconnect();Close();});dialog.ShowDialog();}
        finally{showingUpdates=false;}
    }
    async Task PrepareUpdateRestart()
    {
        gate.Disarm("shutdown");awaitingFileTransfer=true;
        try{if(filesWindow?.IsBusy==true)await filesWindow.CancelAndWaitAsync();}
        finally{awaitingFileTransfer=false;}
    }
    async Task CheckUpdates()
    {
        if(checkingUpdates||closing)return;var preferences=UpdatePreferences.Load();if(!preferences.Automatic)return;checkingUpdates=true;
        try {var result=await UpdateProcess.DownloadAvailable(preferences,text=>updatesButton.ToolTip=text);updatesButton.Content=UpdateDownload.Ready(result)?"Restart to update":"Updates";updatesButton.ToolTip=UpdateDownload.Ready(result)?"An update is downloaded and verified. Restart when you are ready.":result.GetProperty("message").GetString();}
        catch(Exception e){updatesButton.ToolTip="Update check unavailable: "+e.Message;}
        finally{checkingUpdates=false;}
    }
    string AppTitle()=>appLabel+(session?.IsReady!=true?" (disconnected)":gate.CanRoute?"":" (click to control)");
    // Size the App Priority window once per stream so it matches the Mac window's shape.
    void FitAppWindow(JsonElement m)
    {
        if(appWindow?.Content is not FrameworkElement content||content.ActualWidth<=0||!m.TryGetProperty("width",out var w)||!m.TryGetProperty("height",out var h)||!w.TryGetDouble(out double vw)||!h.TryGetDouble(out double vh)||vw<=0||vh<=0)return;
        appSized=true;
        if(appWindow.WindowState!=WindowState.Normal)return;
        var area=SystemParameters.WorkArea;double chromeW=appWindow.ActualWidth-content.ActualWidth,chromeH=appWindow.ActualHeight-content.ActualHeight;
        double width=Math.Min(vw,area.Width*0.9-chromeW),height=width*vh/vw;
        if(height>area.Height*0.9-chromeH){height=area.Height*0.9-chromeH;width=height*vw/vh;}
        appWindow.Width=width+chromeW;appWindow.Height=height+chromeH;
        appWindow.Left=area.Left+(area.Width-appWindow.Width)/2;appWindow.Top=area.Top+(area.Height-appWindow.Height)/2;
    }
    record WindowChoice(uint ID,string Label,string App);
}

internal sealed class RemoteView : WebView2
{
    bool ready,armed;
    bool? postedArmed;
    long postedArmedEpoch;
    JsonElement? config;
    EncodedFrameQueue? frames;
    Func<bool>? currentStream;
    readonly VideoBatchGate bridge=new();
    readonly DispatcherTimer frameWatchdog=new(){Interval=TimeSpan.FromMilliseconds(100)};
    readonly Action<JsonElement> pointer;
    readonly Action<string> report;
    public event Action? VideoInvalidated;
    public long LastPresented{get;private set;}
    public string VideoStats{get;private set;}="waiting for video";
    public RemoteView(Action<JsonElement> pointer,Action<string> report)
    {
        this.pointer=pointer;this.report=report;
        Loaded+=async(_,_)=>{await Initialize();if(bridge.HasPending)frameWatchdog.Start();PumpFrames();};
        Unloaded+=(_,_)=>frameWatchdog.Stop();
        frameWatchdog.Tick+=(_,_)=>
        {
            if(!bridge.Expired(Environment.TickCount64))return;
            // Do not accumulate browser work behind a missing acknowledgment.
            // A fresh epoch rejects its delayed output and resumes at a keyframe.
            frames?.Clear();ResetEpoch();PumpFrames();
        };
    }
    async Task Initialize()
    {
        if(CoreWebView2!=null)return;
        try{
            _=CoreWebView2Environment.GetAvailableBrowserVersionString();
            string cache=Path.Combine(Path.GetTempPath(),"mac-lan-WebView",Environment.ProcessId.ToString());
            var env=await CoreWebView2Environment.CreateAsync(null,cache);
            await EnsureCoreWebView2Async(env);
            if(CoreWebView2==null)throw new InvalidOperationException("WebView2 initialization did not produce a runtime.");
            CoreWebView2.Settings.AreDevToolsEnabled=false;CoreWebView2.Settings.AreDefaultContextMenusEnabled=false;CoreWebView2.Settings.AreBrowserAcceleratorKeysEnabled=false;CoreWebView2.Settings.IsStatusBarEnabled=false;CoreWebView2.Settings.IsZoomControlEnabled=false;
            CoreWebView2.PermissionRequested+=(_,e)=>e.State=CoreWebView2PermissionState.Deny;
            CoreWebView2.NewWindowRequested+=(_,e)=>e.Handled=true;
            CoreWebView2.DownloadStarting+=(_,e)=>e.Cancel=true;
            CoreWebView2.NavigationStarting+=(_,e)=>{if(e.Uri!="https://macdow.local/viewer.html")e.Cancel=true;};
            CoreWebView2.SetVirtualHostNameToFolderMapping("macdow.local",Payloads.Root,CoreWebView2HostResourceAccessKind.Deny);
            CoreWebView2.WebMessageReceived+=(_,e)=>
            {
                if(e.Source!="https://macdow.local/viewer.html")return;
                try
                {
                    using var doc=JsonDocument.Parse(e.WebMessageAsJson);var m=doc.RootElement;var type=m.GetProperty("type").GetString();
                    if(type=="loaded"){ready=true;ResetEpoch();SetArmed(armed);}
                    else if(type=="presented")
                    {
                        if(MessageEpoch(m,out long epoch)&&bridge.IsCurrent(epoch)&&currentStream?.Invoke()==true)LastPresented=Stopwatch.GetTimestamp();
                    }
                    else if(type=="frameAck")
                    {
                        if(MessageEpoch(m,out long epoch)&&m.TryGetProperty("batch",out var batch)&&batch.ValueKind==JsonValueKind.Number&&batch.TryGetInt64(out long number)&&bridge.Acknowledge(epoch,number))
                        {frameWatchdog.Stop();PumpFrames();}
                    }
                    else if(type=="clearAck")
                    {
                        if(MessageEpoch(m,out long epoch)&&bridge.AcknowledgeReset(epoch))PumpFrames();
                    }
                    else if(type=="stats"){VideoStats=m.GetProperty("message").GetString()??"";report(VideoStats);}
                    else if(MessageEpoch(m,out long epoch)&&bridge.IsCurrent(epoch)&&currentStream?.Invoke()==true)pointer(m);
                }
                catch(JsonException){}
            };
            CoreWebView2.Navigate("https://macdow.local/viewer.html");
        }catch(Exception ex){VideoStats="WebView2 runtime unavailable: "+ex.Message;report(VideoStats);}
    }
    static bool MessageEpoch(JsonElement m,out long epoch){epoch=0;return m.TryGetProperty("epoch",out var value)&&value.ValueKind==JsonValueKind.Number&&value.TryGetInt64(out epoch);}
    public void Configure(JsonElement m){config=m;PostConfiguration();}
    void PostConfiguration()
    {
        if(!ready||config==null)return;
        var metadata=JsonSerializer.Deserialize<Dictionary<string,JsonElement>>(config.Value.GetRawText())!;
        metadata["epoch"]=JsonSerializer.SerializeToElement(bridge.Epoch);
        CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(metadata));
    }
    public void Frames(EncodedFrameQueue incoming,Func<bool> isCurrent)
    {
        if(!isCurrent())return;
        if(!ReferenceEquals(frames,incoming))
        {
            frames?.Clear();frames=incoming;currentStream=isCurrent;ResetEpoch();
        }
        PumpFrames();
    }
    void PumpFrames()
    {
        if(!ready||!bridge.Ready||bridge.HasPending||frames==null||currentStream?.Invoke()!=true)return;
        var batch=frames.Drain(Environment.TickCount64);if(batch.Count==0)return;
        long number=bridge.Begin(Environment.TickCount64)!.Value;
        CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="frames",epoch=bridge.Epoch,batch=number,frames=batch.Select(Convert.ToBase64String)}));
        frameWatchdog.Start();
    }
    void ResetEpoch()
    {
        bridge.Reset();LastPresented=0;armed=false;frameWatchdog.Stop();VideoInvalidated?.Invoke();
        if(ready){CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="clear",epoch=bridge.Epoch}));PostConfiguration();}
    }
    public void SetArmed(bool value)
    {
        armed=value&&bridge.Ready&&LastPresented>0&&currentStream?.Invoke()==true;
        if(!ready||postedArmed==armed&&postedArmedEpoch==bridge.Epoch)return;
        postedArmed=armed;postedArmedEpoch=bridge.Epoch;
        CoreWebView2.PostWebMessageAsJson(JsonSerializer.Serialize(new{type="armed",epoch=bridge.Epoch,active=armed}));
    }
    public void Clear(){frames?.Clear();frames=null;currentStream=null;ResetEpoch();}
}
