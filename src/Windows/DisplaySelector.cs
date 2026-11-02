using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using MacDow.Core;

namespace MacDow.Windows;

// A separate selector keeps the connection page's control in its own visual tree.
internal sealed class DisplaySelector : ComboBox
{
    bool synchronizing;
    public event Action<MonitorArea>? MonitorSelected;
    public DisplaySelector()
    {
        Style=(Style)Application.Current.FindResource(typeof(ComboBox));
        DisplayMemberPath=nameof(DisplayChoice.Label);Width=142;MinHeight=24;Height=24;FontSize=12;
        Padding=new Thickness(8,2,28,2);
        VerticalAlignment=VerticalAlignment.Center;
        AutomationProperties.SetName(this,"Display for Windows and Mac tabs");
        SelectionChanged+=(_,_)=>{if(!synchronizing&&SelectedItem is DisplayChoice choice)MonitorSelected?.Invoke(choice.Monitor);};
    }
    public void Update(IReadOnlyList<MonitorArea> all,string? selected)
    {
        synchronizing=true;
        try
        {
            ItemsSource=DisplayLayout.Choices(all);
            SelectedItem=Items.Cast<DisplayChoice>().FirstOrDefault(c=>c.Monitor.Id==selected);
            IsEnabled=all.Count>1;
            ToolTip=SelectedItem is DisplayChoice choice?"Move mac-lan to a display. Current: "+choice.Monitor.DisplayName:"No display available";
        }
        finally{synchronizing=false;}
    }
}
