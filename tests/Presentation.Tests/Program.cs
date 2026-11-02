using System.Windows;
using System.Windows.Controls;
using System.Windows.Markup;
using System.Windows.Media;
using System.Windows.Threading;
using MacDow.Core;
using MacDow.Windows;

internal static class Program
{
    [STAThread] static void Main()
    {
        var app=new Application();
        using var source=System.IO.File.OpenRead(System.IO.Path.Combine(AppContext.BaseDirectory,"Theme.xaml"));
        app.Resources.MergedDictionaries.Add((ResourceDictionary)XamlReader.Load(source));
        var host=new DiscoveredHost("MacBook","192.168.1.2",4433,new string('a',64));
        var combo=new ComboBox{Width=500,DisplayMemberPath="Label",ItemsSource=new[]{host},SelectedIndex=0};
        CheckText(combo,"MacBook (192.168.1.2)","Selected Mac shows its friendly label instead of its internal record");
        var monitor=new MonitorArea("\\\\.\\DISPLAY2",0,0,1920,1080,96,false);
        CheckText(new ComboBox{Width=500,DisplayMemberPath="DisplayName",ItemsSource=new[]{monitor},SelectedIndex=0},"Display 2 - 1920 x 1080","Selected display uses its readable name");
        var main=monitor with{Id="main",Primary=true};
        var left=monitor with{Id="left",Left=-1920};
        var picker=new DisplaySelector();int changes=0;
        picker.MonitorSelected+=_=>changes++;
        picker.Update(new[]{main,left},"main");
        CheckText(picker,"Right","Top-bar display selector renders the physical position in a compact control");
        var selectedText=Descendants(picker).OfType<TextBlock>().Single(t=>t.Text=="Right");
        Check(picker.ActualHeight==24&&selectedText.ActualHeight>12&&selectedText.ActualHeight+picker.Padding.Top+picker.Padding.Bottom<=24,"The selected label fits the 24-pixel top-bar control without vertical clipping");
        Check(picker.IsEnabled&&changes==0,"Populating and synchronizing the picker do not move the app");
        picker.SelectedIndex=0;
        Check(changes==1&&((DisplayChoice)picker.SelectedItem).Monitor.Id=="left","Picking the other screen requests exactly one monitor change");
        picker.Update(new[]{main},"main");
        CheckText(picker,"Display 1 (main)","Removing a display updates the visible selected label");
        Check(!picker.IsEnabled&&changes==1,"Single-display fallback cannot issue a stale selection");
        picker.Update(new[]{main,left,main with{Id="above",Top=-1080}},"above");
        Check(picker.Items.Count==3&&picker.IsEnabled&&((DisplayChoice)picker.SelectedItem).Monitor.Id=="above"&&changes==1,"Hotplug and settings synchronization retain the selected display without callbacks");
        Console.WriteLine("Presentation checks passed; no windows shown, hooks, capture or input.");
        app.Shutdown();
    }
    static void Check(bool value,string name){if(!value)throw new Exception(name);Console.WriteLine("PASS "+name);}
    static void CheckText(ComboBox combo,string expected,string name)
    {
        combo.Style=(Style)Application.Current.FindResource(typeof(ComboBox));
        combo.ApplyTemplate();combo.Measure(new Size(500,100));combo.Arrange(new Rect(0,0,double.IsNaN(combo.Width)?500:combo.Width,double.IsNaN(combo.Height)?100:combo.Height));combo.UpdateLayout();
        Dispatcher.CurrentDispatcher.Invoke(()=>{},DispatcherPriority.ApplicationIdle);
        string[] text=Descendants(combo).OfType<TextBlock>().Select(t=>t.Text).ToArray();
        if(!text.Contains(expected))throw new Exception(name+"; actual: "+string.Join(" | ",text));
        Console.WriteLine("PASS "+name);
    }
    static IEnumerable<DependencyObject> Descendants(DependencyObject root)
    {
        for(int i=0;i<VisualTreeHelper.GetChildrenCount(root);i++)
        {
            var child=VisualTreeHelper.GetChild(root,i);yield return child;
            foreach(var item in Descendants(child))yield return item;
        }
    }
}
