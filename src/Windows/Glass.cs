using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media;

namespace MacDow.Windows;

// Shared dark palette (mirrors Theme.xaml) and the translucent window backdrop.
internal static class Glass
{
    static SolidColorBrush Brush(uint argb){var b=new SolidColorBrush(Color.FromArgb((byte)(argb>>24),(byte)(argb>>16),(byte)(argb>>8),(byte)argb));b.Freeze();return b;}
    public static readonly SolidColorBrush Ink=Brush(0xFFF2F2F5),Muted=Brush(0xFF9C9CA6),Line=Brush(0x26FFFFFF),Fill=Brush(0x14FFFFFF),Selected=Brush(0x33FFFFFF),Accent=Brush(0xFF0A84FF),
        Warn=Brush(0xFFFF9F0A),Tint=Brush(0x8C141519),Solid=Brush(0xFF1C1D21),Note=Brush(0x1A0A84FF);

    [StructLayout(LayoutKind.Sequential)] struct Margins {public int Left,Right,Top,Bottom;}
    [DllImport("dwmapi.dll")] static extern int DwmSetWindowAttribute(nint hwnd,int attribute,ref int value,int size);
    [DllImport("dwmapi.dll")] static extern int DwmExtendFrameIntoClientArea(nint hwnd,ref Margins margins);

    // Windows 11 22H2+ draws an acrylic backdrop behind a lightly tinted window; older
    // systems keep the same dark palette on a solid background.
    public static void Apply(Window window,bool rounded=true)
    {
        window.FontFamily=new FontFamily("Segoe UI");window.Foreground=Ink;window.Background=Solid;
        window.SourceInitialized+=(_,_)=>
        {
            var hwnd=new WindowInteropHelper(window).Handle;
            int on=1,corners=rounded?2:1,acrylic=3;
            try
            {
                DwmSetWindowAttribute(hwnd,20,ref on,sizeof(int));
                DwmSetWindowAttribute(hwnd,33,ref corners,sizeof(int));
                if(Environment.OSVersion.Version.Build<22621||DwmSetWindowAttribute(hwnd,38,ref acrylic,sizeof(int))!=0)return;
                var margins=new Margins{Left=-1,Right=-1,Top=-1,Bottom=-1};
                if(DwmExtendFrameIntoClientArea(hwnd,ref margins)!=0)return;
                if(HwndSource.FromHwnd(hwnd)?.CompositionTarget is {} target)target.BackgroundColor=Colors.Transparent;
                window.Background=Tint;
            }
            catch(DllNotFoundException){}
        };
    }
}
