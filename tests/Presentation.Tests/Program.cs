using System.Windows;
using System.Windows.Controls;
using System.Windows.Markup;
using System.Windows.Media;
using System.Windows.Threading;
using MacDow.Core;

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
        Console.WriteLine("Presentation checks passed; no windows shown, hooks, capture or input.");
        app.Shutdown();
    }
    static void CheckText(ComboBox combo,string expected,string name)
    {
        combo.Style=(Style)Application.Current.FindResource(typeof(ComboBox));
        combo.ApplyTemplate();combo.Measure(new Size(500,100));combo.Arrange(new Rect(0,0,500,100));combo.UpdateLayout();
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
