using System.IO;
using System.Net.Http;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Microsoft.Win32;

namespace MacDow.Windows;

internal sealed class FileTransferWindow : Window
{
    sealed record Selection(string Path,string Name,bool Folder)
    {public override string ToString()=>Name+(Folder?"  ·  Folder":"");}
    readonly Session? session;
    readonly ListBox items=new(){BorderThickness=new Thickness(0),Background=Brushes.Transparent,MinHeight=120,HorizontalContentAlignment=HorizontalAlignment.Stretch};
    readonly TextBlock message=new(){TextWrapping=TextWrapping.Wrap,Foreground=new SolidColorBrush(Color.FromRgb(95,109,128)),Margin=new Thickness(0,10,0,0)};
    readonly TextBlock detail=new(){Text="Drag files or folders here, or add them below.",Foreground=new SolidColorBrush(Color.FromRgb(95,109,128)),TextWrapping=TextWrapping.Wrap};
    readonly ProgressBar progress=new(){Height=5,Minimum=0,Maximum=100,Visibility=Visibility.Collapsed,Margin=new Thickness(0,0,0,10)};
    readonly Button addFiles,addFolder,remove,send,cancel;
    CancellationTokenSource? operation;
    TaskCompletionSource? settled;
    bool busy,closeRequested,closed;
    public Session? BoundSession=>session;
    public bool IsBusy=>busy;
    public Task CancelAndWaitAsync(){operation?.Cancel();return settled?.Task??Task.CompletedTask;}
    public FileTransferWindow(Session? session)
    {
        this.session=session;Title="MAC-DOW · Send to Mac";Width=680;Height=560;MinWidth=540;MinHeight=450;WindowStartupLocation=WindowStartupLocation.CenterScreen;
        var layout=new Grid{Margin=new Thickness(28)};layout.RowDefinitions.Add(new(){Height=GridLength.Auto});layout.RowDefinitions.Add(new(){Height=GridLength.Auto});layout.RowDefinitions.Add(new());layout.RowDefinitions.Add(new(){Height=GridLength.Auto});layout.RowDefinitions.Add(new(){Height=GridLength.Auto});
        var heading=new StackPanel();heading.Children.Add(new TextBlock{Text="Send to your Mac",FontSize=26,FontWeight=FontWeights.SemiBold});heading.Children.Add(new TextBlock{Text="Files, ZIPs and folders go to Downloads/MAC-DOW on your Mac.",Foreground=message.Foreground,Margin=new Thickness(0,8,0,22),TextWrapping=TextWrapping.Wrap});layout.Children.Add(heading);
        var actions=new StackPanel{Orientation=Orientation.Horizontal,Margin=new Thickness(0,0,0,12)};
        Button Action(string text,Action action){var button=new Button{Content=text,Margin=new Thickness(0,0,8,0),Padding=new Thickness(14,8,14,8)};button.Click+=(_,_)=>action();return button;}
        addFiles=Action("Add files",ChooseFiles);addFolder=Action("Add folder",ChooseFolder);remove=Action("Remove",()=>{if(items.SelectedItem!=null)items.Items.Remove(items.SelectedItem);Refresh();});
        actions.Children.Add(addFiles);actions.Children.Add(addFolder);actions.Children.Add(remove);Grid.SetRow(actions,1);layout.Children.Add(actions);
        var drop=new DockPanel{Margin=new Thickness(14)};DockPanel.SetDock(detail,Dock.Top);detail.Margin=new Thickness(0,0,0,10);drop.Children.Add(detail);drop.Children.Add(items);
        var card=new Border{Background=Brushes.White,BorderBrush=new SolidColorBrush(Color.FromRgb(220,227,237)),BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(8),Child=drop,AllowDrop=true};
        card.DragOver+=(_,e)=>{e.Effects=!busy&&CanSend&&e.Data.GetDataPresent(DataFormats.FileDrop)?DragDropEffects.Copy:DragDropEffects.None;e.Handled=true;};
        card.Drop+=(_,e)=>{if(!busy&&CanSend&&e.Data.GetData(DataFormats.FileDrop) is string[] paths)Add(paths);e.Handled=true;};Grid.SetRow(card,2);layout.Children.Add(card);
        var feedback=new StackPanel{Margin=new Thickness(0,14,0,14)};feedback.Children.Add(progress);feedback.Children.Add(message);Grid.SetRow(feedback,3);layout.Children.Add(feedback);
        var bottom=new StackPanel{Orientation=Orientation.Horizontal,HorizontalAlignment=HorizontalAlignment.Right};
        cancel=Action("Close",()=>{if(busy)operation?.Cancel();else Close();});cancel.Margin=new Thickness(0,0,10,0);
        send=Action("Send to Mac",async()=>await Send());send.Margin=new Thickness(0);send.Style=(Style)FindResource("PrimaryButton");send.MinWidth=130;bottom.Children.Add(cancel);bottom.Children.Add(send);Grid.SetRow(bottom,4);layout.Children.Add(bottom);Content=layout;
        items.SelectionChanged+=(_,_)=>Refresh();
        if(session!=null)session.Ended+=SessionEnded;
        Closing+=(_,e)=>{if(busy){e.Cancel=true;closeRequested=true;operation?.Cancel();}};
        Closed+=(_,_)=>{closed=true;if(session!=null)session.Ended-=SessionEnded;operation?.Cancel();};
        message.Text=CanSend?"Ready when you are.":session?.IsPaired==true?"Update MAC-DOW on your Mac to enable file transfer.":"Connect to your Mac first, then open Files again.";Refresh();
    }
    bool CanSend=>session?.CanTransfer==true;
    void Refresh()
    {
        addFiles.IsEnabled=addFolder.IsEnabled=!busy&&CanSend;remove.IsEnabled=!busy&&items.SelectedItem!=null;
        send.IsEnabled=!busy&&CanSend&&items.Items.Count>0;send.Content=busy?"Sending…":"Send to Mac";cancel.Content=busy?"Cancel transfer":"Close";
        detail.Text=items.Items.Count==0?"Drop files or folders here.":$"{items.Items.Count:N0} item{(items.Items.Count==1?"":"s")} ready to send";
    }
    void SessionEnded(string reason)=>Dispatcher.BeginInvoke(()=>{if(closed)return;operation?.Cancel();message.Text="Your Mac disconnected. Reconnect, then open Files again.";Refresh();});
    void ChooseFiles(){var picker=new OpenFileDialog{Title="Choose files to send to your Mac",Multiselect=true,CheckFileExists=true};if(picker.ShowDialog(this)==true)Add(picker.FileNames);}
    void ChooseFolder(){var picker=new OpenFolderDialog{Title="Choose a folder to send to your Mac",Multiselect=false};if(picker.ShowDialog(this)==true)Add(new[]{picker.FolderName});}
    void Add(IEnumerable<string> paths)
    {
        try
        {
            foreach(string selected in paths)
            {
                if(items.Items.Count>=100)throw new InvalidDataException("Send up to 100 files or folders at once.");
                string path=Path.TrimEndingDirectorySeparator(Path.GetFullPath(selected));var attributes=File.GetAttributes(path);string name=Path.GetFileName(path);FileTransferPackage.ValidateName(name);
                if((attributes&FileAttributes.ReparsePoint)!=0)throw new InvalidDataException("Choose the original file or folder instead of a link.");
                if(!items.Items.Cast<Selection>().Any(item=>string.Equals(item.Path,path,StringComparison.OrdinalIgnoreCase)))items.Items.Add(new Selection(path,name,(attributes&FileAttributes.Directory)!=0));
            }
            message.Text="Ready to send. Your originals stay on this PC.";
        }
        catch(Exception ex)when(ex is IOException or UnauthorizedAccessException or ArgumentException){message.Text=ex is InvalidDataException?ex.Message:"One of these items cannot be read. Choose it again after checking access.";}
        Refresh();
    }
    async Task Send()
    {
        if(busy||session?.TransferTarget is not TransferTarget target||items.Items.Count==0)return;
        using var cancellation=CancellationTokenSource.CreateLinkedTokenSource(target.SessionEnded);operation=cancellation;settled=new(TaskCreationOptions.RunContinuationsAsynchronously);busy=true;progress.Visibility=Visibility.Visible;Refresh();
        int sent=0,total=items.Items.Count;
        try
        {
            var client=new FileTransferClient(target);
            while(items.Items.Count>0)
            {
                cancellation.Token.ThrowIfCancellationRequested();var item=(Selection)items.Items[0];progress.Value=0;progress.IsIndeterminate=item.Folder;message.Text=$"Preparing {item.Name}…";
                using var package=item.Folder?await FileTransferPackage.CreateFolderAsync(item.Path,new Progress<string>(text=>{if(!closed)message.Text=text;}),cancellation.Token):FileTransferPackage.CreateFile(item.Path);
                progress.IsIndeterminate=false;
                var receipt=await client.SendAsync(package,new Progress<TransferProgress>(p=>
                {
                    if(closed)return;progress.Value=p.Total==0?100:100d*p.Sent/p.Total;
                    message.Text=$"Sending {sent+1} of {total}: {item.Name}\n{Size(p.Sent)} of {Size(p.Total)}";
                }),cancellation.Token);
                items.Items.RemoveAt(0);sent++;message.Text=$"Saved {receipt.Name} to Downloads/MAC-DOW on your Mac.";
            }
            progress.Value=100;message.Text=$"{sent} item{(sent==1?"":"s")} saved to Downloads/MAC-DOW on your Mac.";
        }
        catch(OperationCanceledException){message.Text=session.IsPaired?"Transfer cancelled. Your originals stay on this PC.":"Your Mac disconnected. Reconnect, then open Files again.";}
        catch(Exception ex)when(ex is IOException or UnauthorizedAccessException or HttpRequestException or System.Text.Json.JsonException or ArgumentException)
        {message.Text=ex is InvalidDataException?ex.Message:ex is HttpRequestException?"Could not reach the paired Mac. Reconnect, then retry the remaining items.":"Could not send this item. Check that it is readable and your Mac has free space, then retry.";}
        finally{busy=false;operation=null;progress.IsIndeterminate=false;Refresh();if(closeRequested)Close();settled.TrySetResult();}
    }
    static string Size(long bytes)=>bytes>=1024L*1024*1024?$"{bytes/(1024d*1024*1024):0.0} GB":bytes>=1024*1024?$"{bytes/(1024d*1024):0.0} MB":bytes>=1024?$"{bytes/1024d:0.0} KB":$"{bytes} B";
}
