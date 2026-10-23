using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Shapes;

namespace MacDow.Windows;

// Glass confirmation panel shared in layout with the Mac approval window:
// symbol, title, a large code, short explanation, and Cancel / primary action.
internal sealed class ConfirmDialog : Window
{
    ConfirmDialog(Window owner,string title,string code,string detail,string action)
    {
        Owner=owner;WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;SizeToContent=SizeToContent.WidthAndHeight;
        WindowStartupLocation=WindowStartupLocation.CenterOwner;ShowInTaskbar=false;Title=title;
        Glass.Apply(this);
        var panel=new StackPanel{Width=400,Margin=new Thickness(32,28,32,24)};
        panel.Children.Add(new Path{Data=Geometry.Parse("M 7,1 H 45 A 3,3 0 0 1 48,4 V 29 H 4 V 4 A 3,3 0 0 1 7,1 Z M 0,32 H 52 L 49,36 H 3 Z"),Stroke=Glass.Ink,StrokeThickness=1.5,HorizontalAlignment=HorizontalAlignment.Left,Margin=new Thickness(0,0,0,14)});
        panel.Children.Add(new TextBlock{Text=title,FontSize=17,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,10)});
        panel.Children.Add(new TextBlock{Text=code,FontFamily=new FontFamily("Cascadia Mono, Consolas"),FontSize=20,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,10)});
        panel.Children.Add(new TextBlock{Text=detail,Foreground=Glass.Muted,FontSize=13,LineHeight=19,Margin=new Thickness(0,0,0,20)});
        var buttons=new StackPanel{Orientation=Orientation.Horizontal,HorizontalAlignment=HorizontalAlignment.Right};
        var cancel=new Button{Content="Cancel",IsCancel=true,MinWidth=96,Margin=new Thickness(0,0,10,0)};
        var confirm=new Button{Content=action,IsDefault=true,MinWidth=96,Style=(Style)FindResource("PrimaryButton")};
        confirm.Click+=(_,_)=>DialogResult=true;
        buttons.Children.Add(cancel);buttons.Children.Add(confirm);panel.Children.Add(buttons);
        Content=panel;
        MouseLeftButtonDown+=(_,e)=>{if(e.ButtonState==System.Windows.Input.MouseButtonState.Pressed)DragMove();};
    }
    public static bool Ask(Window owner,string title,string code,string detail,string action)=>new ConfirmDialog(owner,title,code,detail,action).ShowDialog()==true;
}
