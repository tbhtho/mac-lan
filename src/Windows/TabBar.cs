using System.Runtime.InteropServices;
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
    const int CallbackMessage=0x8000+724;
    readonly Button local,mac;
    readonly TextBlock connection=new(){Foreground=new SolidColorBrush(Color.FromRgb(167,184,207)),VerticalAlignment=VerticalAlignment.Center,Margin=new Thickness(16,0,12,0),Text="Mac disconnected",TextWrapping=TextWrapping.NoWrap};
    bool registered,positioning,queued,disposed;
    string? monitor;
    HwndSource? source;
    public TabBar(Action windows,Action macbook,Action setup,Action updates,Action quit,ComboBox monitors,Button updateButton,Action? controlMac=null,Action? release=null,Action? disconnect=null,Func<bool>? canControl=null,Action? files=null)
    {
        Title="MAC-DOW Tabs";WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;ShowInTaskbar=false;Topmost=true;Height=48;
        Background=new SolidColorBrush(Color.FromRgb(19,32,51));Foreground=Brushes.White;
        var grid=new DockPanel{LastChildFill=true,Margin=new Thickness(12,5,12,5)};
        var right=new StackPanel{Orientation=Orientation.Horizontal,HorizontalAlignment=HorizontalAlignment.Right};DockPanel.SetDock(right,Dock.Right);
        Button ActionButton(string label,Action action){var b=new Button{Content=label,Padding=new Thickness(12,5,12,5),Margin=new Thickness(4,0,0,0),MinHeight=32,Background=Brushes.Transparent,BorderBrush=Brushes.Transparent,Foreground=Brushes.White,FontSize=12};b.Click+=(_,_)=>action();return b;}
        var connectionButton=ActionButton("Connection",()=>{});
        connectionButton.Click+=(_,_)=>
        {
            var menu=new ContextMenu{PlacementTarget=connectionButton,Placement=PlacementMode.Bottom,Background=Background,Foreground=Brushes.White,BorderBrush=new SolidColorBrush(Color.FromRgb(57,74,96)),Padding=new Thickness(4)};
            var itemTemplate=new ControlTemplate(typeof(MenuItem));
            var surface=new FrameworkElementFactory(typeof(Border),"MenuItemSurface");
            surface.SetValue(Border.BackgroundProperty,Brushes.Transparent);surface.SetValue(Border.CornerRadiusProperty,new CornerRadius(4));surface.SetValue(Border.PaddingProperty,new Thickness(12,7,12,7));
            var header=new FrameworkElementFactory(typeof(ContentPresenter));header.SetValue(ContentPresenter.ContentSourceProperty,"Header");header.SetValue(ContentPresenter.RecognizesAccessKeyProperty,true);surface.AppendChild(header);itemTemplate.VisualTree=surface;
            itemTemplate.Triggers.Add(new Trigger{Property=MenuItem.IsHighlightedProperty,Value=true,Setters={new Setter(Border.BackgroundProperty,new SolidColorBrush(Color.FromRgb(45,67,98)),"MenuItemSurface")}});
            itemTemplate.Triggers.Add(new Trigger{Property=IsEnabledProperty,Value=false,Setters={new Setter(OpacityProperty,0.45)}});
            MenuItem Item(string label,Action? action,bool enabled=true)
            {
                var item=new MenuItem{Header=label,IsEnabled=enabled&&action!=null,Foreground=Brushes.White,Template=itemTemplate};
                if(action!=null)item.Click+=(_,_)=>action();
                return item;
            }
            menu.Items.Add(Item("Connection settings",setup));
            menu.Items.Add(new Separator());
            menu.Items.Add(Item("Control Mac",controlMac,canControl?.Invoke()==true));
            menu.Items.Add(Item("Release control",release));
            menu.Items.Add(Item("Disconnect",disconnect));
            connectionButton.ContextMenu=menu;menu.IsOpen=true;
        };
        if(files!=null)right.Children.Add(ActionButton("Files",files));
        right.Children.Add(connectionButton);
        updateButton.Foreground=Brushes.White;updateButton.Background=Brushes.Transparent;updateButton.BorderBrush=Brushes.Transparent;updateButton.MinHeight=32;updateButton.FontSize=12;
        right.Children.Add(updateButton);right.Children.Add(ActionButton("Quit",quit));grid.Children.Add(right);
        var left=new StackPanel{Orientation=Orientation.Horizontal};DockPanel.SetDock(left,Dock.Left);
        left.Children.Add(new TextBlock{Text="MAC-DOW",Foreground=Brushes.White,FontWeight=FontWeights.SemiBold,FontSize=12,VerticalAlignment=VerticalAlignment.Center,Margin=new Thickness(2,0,24,0)});
        local=ActionButton("Windows",windows);mac=ActionButton("MacBook",macbook);local.MinWidth=108;mac.MinWidth=108;left.Children.Add(local);left.Children.Add(mac);grid.Children.Add(left);grid.Children.Add(connection);
        Content=grid;SetMode(true);
        SourceInitialized+=(_,_)=>{source=HwndSource.FromHwnd(new WindowInteropHelper(this).Handle);source.AddHook(Hook);};
        Closing+=(_,_)=>{Undock();source?.RemoveHook(Hook);};
    }
    public void SetMode(bool windows)
    {
        local.Background=windows?new SolidColorBrush(Color.FromRgb(42,96,230)):Brushes.Transparent;
        mac.Background=windows?Brushes.Transparent:new SolidColorBrush(Color.FromRgb(42,96,230));
    }
    public void SetConnection(bool connected,bool controlling,string? detail=null)
    {
        connection.Text=!string.IsNullOrWhiteSpace(detail)?detail:controlling?"Controlling Mac":connected?"Mac connected":"Mac disconnected";
        connection.ToolTip=connection.Text;
    }
    BarData Data()=>new(){Size=(uint)Marshal.SizeOf<BarData>(),Window=new WindowInteropHelper(this).Handle,Callback=CallbackMessage,Edge=1};
    public void DockTo(MonitorArea selected)
    {
        if(disposed)return;
        monitor=selected.Id;
        if(!IsVisible)Show();
        if(!registered){var data=Data();registered=SHAppBarMessage(0,ref data)!=0;if(!registered){Topmost=false;WindowStyle=WindowStyle.ToolWindow;Width=640;Height=80;Telemetry.Report("bar-failed","registration");return;}Telemetry.Report("bar-docked","ok");}
        Reposition();
    }
    void Reposition()
    {
        if(!registered||positioning||monitor==null)return;
        positioning=true;
        try
        {
            var screen=Native.ScreenBounds(monitor);if(screen==null)return;
            var current=Native.Monitors().FirstOrDefault(m=>m.Id==monitor);if(current==null)return;
            int height=TabBarLayout.HeightPixels(current.Dpi);
            var data=Data();data.Bounds=new Rect{Left=screen.Value.Left,Top=screen.Value.Top,Right=screen.Value.Left+screen.Value.Width,Bottom=screen.Value.Top+height};
            SHAppBarMessage(2,ref data);data.Bounds.Bottom=data.Bounds.Top+height;SHAppBarMessage(3,ref data);
            SetWindowPos(data.Window,-1,data.Bounds.Left,data.Bounds.Top,data.Bounds.Right-data.Bounds.Left,height,0x0010|0x0040);
        }
        finally{positioning=false;}
    }
    nint Hook(nint hwnd,int message,nint w,nint l,ref bool handled)
    {
        if(message==CallbackMessage&&w==1&&!queued&&!positioning){queued=true;Dispatcher.BeginInvoke(()=>{queued=false;Reposition();},DispatcherPriority.Background);}
        if(message==CallbackMessage&&w==2)Topmost=l==0;
        if(message==0x0006&&registered){var d=Data();d.Param=w==0?0:1;SHAppBarMessage(6,ref d);}
        return 0;
    }
    void Undock(){if(!registered)return;var data=Data();SHAppBarMessage(1,ref data);registered=false;Telemetry.Report("bar-restored","ok");}
    public void Suspend(){Undock();Hide();}
    public void Dispose(){if(disposed)return;disposed=true;Close();}
}
