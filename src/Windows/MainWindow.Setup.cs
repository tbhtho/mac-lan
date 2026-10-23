using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using MacDow.Core;

namespace MacDow.Windows;

internal sealed partial class MainWindow
{
    static Brush Ink=>Glass.Ink;
    static Brush Muted=>Glass.Muted;
    void BuildSetup()
    {
        FontSize=14;
        setup.Children.Add(new TextBlock{Text="Connect your Mac",FontSize=28,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,8)});
        setup.Children.Add(new TextBlock{Text="Open mac-lan on your Mac. It shows up here automatically; choose it, Connect, then allow the connection there.",Foreground=Muted,Margin=new Thickness(0,0,0,24),LineHeight=21});
        pairButton=MakeButton("Connect",async()=>await ConnectNearby());pairButton.Style=(Style)FindResource("PrimaryButton");pairButton.IsEnabled=false;pairButton.MinWidth=144;setup.Children.Add(pairButton);
        setup.Children.Add(new TextBlock{Text="Macs on your network",FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,18,0,8)});
        nearby.Style=(Style)FindResource("Catalog");nearby.IsEnabled=false;setup.Children.Add(nearby);
        nearby.MouseDoubleClick+=async(_,e)=>{if(e.OriginalSource is DependencyObject d&&ItemsControl.ContainerFromElement(nearby,d) is ListBoxItem&&pairButton.IsEnabled)await ConnectNearby();};
        nearby.SelectionChanged+=(_,_)=>pairButton.IsEnabled=!paired&&!connecting&&nearby.SelectedItem is DiscoveredHost;
        pairingProgress.Text="Looking for your Mac on this network...";
        pairingProgress.Margin=new Thickness(0);pairingProgress.MaxWidth=double.PositiveInfinity;pairingProgress.Foreground=Ink;
        setup.Children.Add(new Border{Background=Glass.Note,BorderBrush=Glass.Line,BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(10),Padding=new Thickness(14,12,14,12),Margin=new Thickness(0,14,0,4),Child=pairingProgress});

        void Field(StackPanel parent,string label,UIElement element){parent.Children.Add(new TextBlock{Text=label,Foreground=Muted,FontSize=12,Margin=new Thickness(0,10,0,5)});parent.Children.Add(element);}

        streamSection.Margin=new Thickness(0,24,0,0);
        streamSection.Children.Add(new Border{Height=1,Background=Glass.Line,Margin=new Thickness(0,0,0,20)});
        streamSection.Children.Add(new TextBlock{Text="App Priority",FontSize=19,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,8)});
        streamSection.Children.Add(new TextBlock{Text="Use the MacBook tab for the desktop. You can also open one Mac window separately.",Foreground=Muted,Margin=new Thickness(0,0,0,8)});
        Field(streamSection,"Choose one Mac window",windows);windows.Width=double.NaN;
        streamSection.Children.Add(MakeButton("Open in App Priority",()=>Start(true)));
        streamSection.Children.Add(new TextBlock{Text="App Priority shows the selected window. Separate menus and dialogs stay on your Mac.",Foreground=Muted,FontSize=12,Margin=new Thickness(0,6,0,8)});
        codec.DisplayMemberPath="Label";codec.ItemsSource=StreamProfile.Options;codec.SelectedItem=StreamProfile.DefaultHardware;
        resolution.ItemsSource=new[]{960,1280,1600,1920};resolution.SelectedItem=1920;
        fps.ItemsSource=new[]{60};fps.SelectedItem=60;bitrate.ItemsSource=new[]{2,4,8,12,20};bitrate.SelectedItem=8;
        var quality=new StackPanel();var qualityRow=new WrapPanel();
        foreach(var option in new[]{("Video",codec),("Width",resolution),("Frames / sec",fps),("Mbit / sec",bitrate)}){var column=new StackPanel{Margin=new Thickness(0,0,12,0)};Field(column,option.Item1,option.Item2);qualityRow.Children.Add(column);}
        codec.Width=252;quality.Children.Add(qualityRow);
        quality.Children.Add(new TextBlock{Text="The desktop targets 60 fps with H.264 and a maximum width of 1920 pixels. The full desktop keeps its proportions, with black bars when needed. Actual frame rate depends on your Mac, PC and network. Compatible JPEG uses more network bandwidth.",Foreground=Muted,FontSize=12,Margin=new Thickness(0,6,0,8)});
        streamSection.Children.Add(new Expander{Header="Streaming quality",Content=quality});
        streamSection.Children.Add(MakeButton("Disconnect",()=>Disconnect()));setup.Children.Add(streamSection);

        var options=new StackPanel{Margin=new Thickness(0,8,0,8)};monitors.Width=double.NaN;monitors.DisplayMemberPath="DisplayName";Field(options,"Display for Windows / Mac tabs",monitors);
        options.Children.Add(new TextBlock{Text="The tab strip reserves its own space. App title bars stay below it.",Foreground=Muted,FontSize=12,Margin=new Thickness(0,8,0,10)});
        options.Children.Add(new TextBlock{Text="Mac diagnostics are sent automatically. Windows diagnostics stay on this PC.",Foreground=Muted,FontSize=12,Margin=new Thickness(0,4,0,8)});
        setup.Children.Add(new Expander{Header="Display and diagnostics",Content=options});
        setup.Children.Add(new TextBlock{Text="Click the Mac desktop once to enable keyboard and mouse control. That first click only activates control. Ctrl+Alt+Shift+Backspace releases it.",Foreground=Muted,FontSize=12,Margin=new Thickness(0,24,0,0)});
    }
}
