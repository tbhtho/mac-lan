using System.Runtime.InteropServices;
using System.IO;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Controls.Primitives;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Threading;
using MacDow.Core;

namespace MacDow.Windows;

// A dedicated AppBar owns the reservation. Setup and video windows never resize it.
internal sealed class TabBar : Window,IDisposable
{
    [StructLayout(LayoutKind.Sequential)] struct Rect {public int Left,Top,Right,Bottom;}
    [StructLayout(LayoutKind.Sequential)] struct BarData {public uint Size;public nint Window;public uint Callback,Edge;public Rect Bounds;public nint Param;}
    [DllImport("shell32.dll")] static extern nuint SHAppBarMessage(uint message,ref BarData data);
    [DllImport("user32.dll",SetLastError=true)] static extern bool SetWindowPos(nint hwnd,nint after,int x,int y,int width,int height,uint flags);
    [StructLayout(LayoutKind.Sequential)] struct Point {public int X,Y;}
    [DllImport("user32.dll")] static extern bool GetCursorPos(out Point point);
    const int CallbackMessage=0x8000+724;
    readonly Button local,mac;
    readonly BarPreferencesStore preferences=BarPreferencesStore.CurrentUser();
    readonly TabBarAutoHide autoHide;
    readonly DispatcherTimer hideTimer=new(){Interval=TimeSpan.FromMilliseconds(100)};
    readonly ContextMenu barMenu=new();
    readonly MenuItem autoHideToggle;
    readonly FrameworkElement barContent;
    Rect barBounds,screenBounds;
    int fullHeight;
    uint currentDpi=96;
    bool keyboardNavigation;
    public bool AutoHide=>autoHide.Enabled;
    public double VideoTopInset=>AutoHide?2*96.0/currentDpi:TabBarLayout.Dip;
    public event Action? AutoHideChanged;
    public event Action? HiddenWhileActive;
    readonly TextBlock connection=new(){Foreground=Glass.Muted,VerticalAlignment=VerticalAlignment.Center,FontSize=12,Text="Mac disconnected",TextWrapping=TextWrapping.NoWrap,TextTrimming=TextTrimming.CharacterEllipsis};
    bool registered,positioning,queued,disposed;
    string? monitor;
    HwndSource? source;
    public TabBar(Action windows,Action macbook,Action setup,Action updates,Action quit,ComboBox monitors,Button updateButton,Action? controlMac=null,Action? release=null,Action? disconnect=null,Func<bool>? canControl=null,Action? files=null)
    {
        autoHide=new(preferences.LoadAutoHide());
        Title="mac-lan Tabs";WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;ShowInTaskbar=false;Topmost=true;Height=TabBarLayout.Dip;
        Glass.Apply(this,rounded:false);FontSize=12;
        var ghost=(Style)FindResource("GhostButton");
        var grid=new DockPanel{LastChildFill=true,Margin=new Thickness(10,3,6,3)};
        var right=new StackPanel{Orientation=Orientation.Horizontal,HorizontalAlignment=HorizontalAlignment.Right};DockPanel.SetDock(right,Dock.Right);
        Button ActionButton(string label,Action action){var b=new Button{Content=label,Style=ghost,Margin=new Thickness(2,0,0,0),MinHeight=24,Padding=new Thickness(10,2,10,2)};b.Click+=(_,_)=>action();return b;}
        var connectionButton=ActionButton("Connection",setup);
        if(files!=null)right.Children.Add(ActionButton("Files",files));
        right.Children.Add(connectionButton);
        updateButton.Style=ghost;updateButton.Margin=new Thickness(2,0,0,0);updateButton.MinHeight=24;updateButton.Padding=new Thickness(10,2,10,2);
        right.Children.Add(updateButton);
        autoHideToggle=new MenuItem{IsCheckable=true,IsChecked=AutoHide};UpdateAutoHideLabel();
        autoHideToggle.Click+=(_,_)=>SetAutoHide(autoHideToggle.IsChecked);
        barMenu.Items.Add(autoHideToggle);ContextMenu=barMenu;
        Button? barOptions=null;barOptions=ActionButton("Bar",()=>{barMenu.PlacementTarget=barOptions;barMenu.IsOpen=true;});barOptions.ToolTip="Top bar settings";
        right.Children.Add(barOptions);right.Children.Add(ActionButton("Quit",quit));grid.Children.Add(right);
        var left=new StackPanel{Orientation=Orientation.Horizontal};DockPanel.SetDock(left,Dock.Left);
        left.Children.Add(new TextBlock{Text="mac-lan",FontWeight=FontWeights.SemiBold,FontSize=12,VerticalAlignment=VerticalAlignment.Center,Margin=new Thickness(2,0,14,0)});
        // Segmented control: one pill highlights the visible desktop.
        Button Segment(string label,Action action){var b=new Button{Content=label,Style=ghost,MinWidth=84,MinHeight=22,Padding=new Thickness(10,1,10,1)};b.Click+=(_,_)=>action();return b;}
        local=Segment("Windows",windows);mac=Segment("MacBook",macbook);
        var segments=new StackPanel{Orientation=Orientation.Horizontal};segments.Children.Add(local);segments.Children.Add(mac);
        left.Children.Add(new Border{Background=Glass.Fill,BorderBrush=Glass.Line,BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(7),Padding=new Thickness(1),VerticalAlignment=VerticalAlignment.Center,Child=segments});
        grid.Children.Add(left);
        var status=new StackPanel{Orientation=Orientation.Horizontal,Margin=new Thickness(16,0,12,0),VerticalAlignment=VerticalAlignment.Center};status.Children.Add(connection);
        grid.Children.Add(status);
        barContent=grid;Content=grid;SetMode(true);
        hideTimer.Tick+=(_,_)=>PollAutoHide();
        MouseEnter+=(_,_)=>{if(registered&&AutoHide&&autoHide.Update(true,true,false,Environment.TickCount64))ApplyBarBounds();};
        PreviewKeyDown+=(_,_)=>keyboardNavigation=true;
        PreviewMouseDown+=(_,_)=>keyboardNavigation=false;
        Deactivated+=(_,_)=>keyboardNavigation=false;
        SourceInitialized+=(_,_)=>{source=HwndSource.FromHwnd(new WindowInteropHelper(this).Handle);source.AddHook(Hook);};
        Closing+=(_,_)=>{hideTimer.Stop();Undock();source?.RemoveHook(Hook);};
    }
    public void SetMode(bool windows)
    {
        local.Background=windows?Glass.Selected:Brushes.Transparent;
        mac.Background=windows?Brushes.Transparent:Glass.Selected;
        local.FontWeight=windows?FontWeights.SemiBold:FontWeights.Normal;
        mac.FontWeight=windows?FontWeights.Normal:FontWeights.SemiBold;
    }
    public void SetConnection(bool connected,bool controlling,string? detail=null)
    {
        connection.Text=!string.IsNullOrWhiteSpace(detail)?detail:controlling?"Controlling Mac":connected?"Mac connected":"Mac disconnected";
        connection.ToolTip=connection.Text;
    }
    BarData Data()=>new(){Size=(uint)Marshal.SizeOf<BarData>(),Window=new WindowInteropHelper(this).Handle,Callback=CallbackMessage,Edge=1};
    void UpdateAutoHideLabel()=>autoHideToggle.Header=AutoHide?"✓ Auto-hide top bar":"Auto-hide top bar";
    void SetAutoHide(bool enabled)
    {
        autoHide.Configure(enabled);UpdateAutoHideLabel();
        try{preferences.SaveAutoHide(enabled);autoHideToggle.ToolTip=null;}
        catch(Exception error)when(error is IOException or UnauthorizedAccessException or System.Security.SecurityException)
        {autoHideToggle.ToolTip="This setting applies for this session; it could not be saved.";Telemetry.Log.Record("bar-preference-failed","save",error:error);}
        Reposition();if(AutoHide&&registered)hideTimer.Start();else hideTimer.Stop();
        AutoHideChanged?.Invoke();
    }
    void PollAutoHide()
    {
        if(!registered||!AutoHide||!IsVisible||!GetCursorPos(out var point))return;
        bool atEdge=point.X>=screenBounds.Left&&point.X<screenBounds.Right&&point.Y>=screenBounds.Top&&point.Y<screenBounds.Top+2;
        bool inside=autoHide.Revealed&&point.X>=barBounds.Left&&point.X<barBounds.Right&&point.Y>=barBounds.Top&&point.Y<barBounds.Top+fullHeight;
        bool interacting=barMenu.IsOpen||IsMouseCaptureWithin||keyboardNavigation&&IsKeyboardFocusWithin;
        if(autoHide.Update(atEdge,inside,interacting,Environment.TickCount64))ApplyBarBounds();
    }
    void ApplyBarBounds()
    {
        bool tucked=AutoHide&&!autoHide.Revealed;
        barContent.Visibility=tucked?Visibility.Collapsed:Visibility.Visible;
        bool restoreFocus=tucked&&IsActive;
        if(restoreFocus)System.Windows.Input.Keyboard.ClearFocus();
        SetWindowPos(new WindowInteropHelper(this).Handle,Topmost?-1:-2,barBounds.Left,barBounds.Top,barBounds.Right-barBounds.Left,tucked?2:fullHeight,0x0010|0x0040);
        if(restoreFocus)HiddenWhileActive?.Invoke();
    }
    public void DockTo(MonitorArea selected)
    {
        if(disposed)return;
        monitor=selected.Id;
        if(!IsVisible)Show();
        if(!registered){var data=Data();registered=SHAppBarMessage(0,ref data)!=0;if(!registered){Topmost=false;WindowStyle=WindowStyle.ToolWindow;Width=640;Height=80;autoHide.Configure(false);autoHideToggle.IsChecked=false;UpdateAutoHideLabel();autoHideToggle.IsEnabled=false;Telemetry.Report("bar-failed","registration");return;}Telemetry.Report("bar-docked","ok");}
        Reposition();
        if(AutoHide)hideTimer.Start();
    }
    void Reposition()
    {
        if(!registered||positioning||monitor==null)return;
        positioning=true;
        try
        {
            var screen=Native.ScreenBounds(monitor);if(screen==null)return;
            var current=Native.Monitors().FirstOrDefault(m=>m.Id==monitor);if(current==null)return;
            currentDpi=current.Dpi;fullHeight=TabBarLayout.HeightPixels(current.Dpi);
            screenBounds=new(){Left=screen.Value.Left,Top=screen.Value.Top,Right=screen.Value.Left+screen.Value.Width,Bottom=screen.Value.Top+screen.Value.Height};
            int height=AutoHide?2:fullHeight;
            var data=Data();data.Bounds=new Rect{Left=screen.Value.Left,Top=screen.Value.Top,Right=screen.Value.Left+screen.Value.Width,Bottom=screen.Value.Top+height};
            SHAppBarMessage(2,ref data);data.Bounds.Bottom=data.Bounds.Top+height;SHAppBarMessage(3,ref data);
            barBounds=data.Bounds;ApplyBarBounds();
        }
        finally{positioning=false;}
    }
    nint Hook(nint hwnd,int message,nint w,nint l,ref bool handled)
    {
        if(message==CallbackMessage&&w==1&&!queued&&!positioning){queued=true;Dispatcher.BeginInvoke(()=>{queued=false;Reposition();},DispatcherPriority.Background);}
        // A full-screen window normally sends the bar below it; mac-lan's own Mac view must not.
        if(message==CallbackMessage&&w==2)Topmost=l==0||Native.ForegroundIsOwnProcess();
        if(message==0x0006&&registered){var d=Data();d.Param=w==0?0:1;SHAppBarMessage(6,ref d);}
        return 0;
    }
    void Undock(){if(!registered)return;var data=Data();SHAppBarMessage(1,ref data);registered=false;Telemetry.Report("bar-restored","ok");}
    public void Suspend(){hideTimer.Stop();barMenu.IsOpen=false;Undock();Hide();}
    public void KeepAbove(){if(IsVisible){Topmost=true;Native.KeepAbove(this);}}
    public void Dispose(){if(disposed)return;disposed=true;Close();}
}
