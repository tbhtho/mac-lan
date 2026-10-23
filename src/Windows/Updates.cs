using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;

namespace MacDow.Windows;

internal sealed class UpdatePreferences
{
    public string Channel { get; set; } = "preview";
    public bool Automatic { get; set; } = true;
    public string? Stage { get; set; }
    public string? Journal { get; set; }
    public string? ResultFile { get; set; }
    static string PathName => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","update-preferences.json");
    public static UpdatePreferences Load()
    {
        try { var p=JsonSerializer.Deserialize<UpdatePreferences>(File.ReadAllText(PathName))??new();if(p.Channel is not ("stable" or "preview"))p.Channel="preview";return p; }
        catch(IOException){return new();}catch(JsonException){return new();}catch(UnauthorizedAccessException){return new();}
    }
    public void Save(){Directory.CreateDirectory(Path.GetDirectoryName(PathName)!);string temporary=PathName+"."+Guid.NewGuid().ToString("N")+".tmp";try{File.WriteAllText(temporary,JsonSerializer.Serialize(this));File.Move(temporary,PathName,true);}finally{if(File.Exists(temporary))File.Delete(temporary);}}
}
internal static class UpdateProcess
{
    static readonly SemaphoreSlim operations=new(1,1);
    static readonly SemaphoreSlim downloads=new(1,1);
    internal static string Helper => Payloads.Helper;
    internal static async Task<JsonElement> Call(params string[] arguments)
    {
        await operations.WaitAsync();try{return await CallCore(arguments);}finally{operations.Release();}
    }
    static async Task<JsonElement> CallCore(string[] arguments)
    {
        if(!File.Exists(Helper))throw new InvalidOperationException("This development build has no bundled update helper. Use a packaged mac-lan.exe.");
        // The helper writes UTF-8 JSON; the console code page would garble names such as Marcel’s MacBook.
        var start=new ProcessStartInfo(Helper){UseShellExecute=false,CreateNoWindow=true,RedirectStandardOutput=true,RedirectStandardError=true,StandardOutputEncoding=System.Text.Encoding.UTF8,StandardErrorEncoding=System.Text.Encoding.UTF8};
        foreach(string argument in arguments)start.ArgumentList.Add(argument);
        using var process=Process.Start(start)??throw new IOException("Cannot start update helper.");
        using var timeout=new CancellationTokenSource(TimeSpan.FromMinutes(5));
        var output=process.StandardOutput.ReadToEndAsync(timeout.Token);
        var errors=process.StandardError.ReadToEndAsync(timeout.Token);
        try {await process.WaitForExitAsync(timeout.Token);}
        catch {try{process.Kill(true);}catch(InvalidOperationException){}throw;}
        string text=await output;_ = await errors;
        if(text.Length>2*1024*1024)throw new InvalidDataException("Updater output exceeded limit.");
        using var doc=JsonDocument.Parse(text);return doc.RootElement.Clone();
    }
    internal static async Task<JsonElement> DownloadAvailable(UpdatePreferences preferences,Action<string>? progress=null)
    {
        await downloads.WaitAsync();
        try
        {
            // An opening dialog can overlap the startup check. Reuse the result of
            // the first download only after another fresh preflight, rather than
            // downloading the same release twice.
            var latest=UpdatePreferences.Load();
            string? existing=latest.Channel==preferences.Channel?latest.Stage:preferences.Stage;
            return await UpdateDownload.Run(preferences.Channel,existing,Payloads.Install,arguments=>Call(arguments),directory=>
            {var current=UpdatePreferences.Load();current.Stage=directory;current.Save();},progress);
        }
        finally{downloads.Release();}
    }
    internal static void StartDetached(string[] arguments)
    {
        string root=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","updates","runner-"+Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);string copy=Path.Combine(root,"MAC-DOW-Update.exe");
        File.Copy(Helper,copy,false);
        var start=new ProcessStartInfo(copy){UseShellExecute=false,CreateNoWindow=true,WorkingDirectory=root};
        foreach(string argument in arguments)start.ArgumentList.Add(argument);
        _=Process.Start(start)??throw new IOException("Cannot start update worker.");
    }
}
internal sealed class UpdateDialog : Window
{
    readonly UpdatePreferences preferences;
    readonly TextBlock description=new(){TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,18,0,18)};
    readonly ComboBox channel=new(){ItemsSource=new[]{"stable","preview"},Width=160,HorizontalAlignment=HorizontalAlignment.Left};
    readonly CheckBox automatic=new(){Content="Download updates automatically",Margin=new Thickness(0,8,0,4)};
    readonly CheckBox relaunch=new(){Content="Reopen mac-lan after updating",IsChecked=true,Margin=new Thickness(0,10,0,10)};
    readonly Button apply=new(){Content="Restart to update",IsEnabled=false,Margin=new Thickness(0,6,0,6)};
    readonly Action shutdown;
    readonly Func<Task> prepareRestart;
    readonly StackPanel actions=new();
    string? stage;
    string? tag;
    bool busy;
    public UpdateDialog(Window owner,Func<Task> prepareRestart,Action shutdown)
    {
        this.shutdown=shutdown;this.prepareRestart=prepareRestart;preferences=UpdatePreferences.Load();
        stage=preferences.Stage;
        Owner=owner;Title="mac-lan Updates";Width=570;Height=510;MinWidth=470;MinHeight=420;WindowStartupLocation=WindowStartupLocation.CenterOwner;Glass.Apply(this);
        var content=new StackPanel{Margin=new Thickness(28)};Content=new ScrollViewer{Content=content,VerticalScrollBarVisibility=ScrollBarVisibility.Auto};
        content.Children.Add(new TextBlock{Text="Keep mac-lan up to date",FontSize=25,FontWeight=FontWeights.SemiBold});
        content.Children.Add(new TextBlock{Text="Updates download in the background. Restart when you are ready.",TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,10,0,0)});
        content.Children.Add(description);content.Children.Add(actions);
        apply.Style=(Style)FindResource("PrimaryButton");
        apply.Click+=async(_,_)=>await Run(Apply);actions.Children.Add(apply);
        Add("Check now",Check);
        automatic.IsChecked=preferences.Automatic;content.Children.Add(automatic);
        content.Children.Add(relaunch);
        var advanced=new StackPanel();advanced.Children.Add(new TextBlock{Text="Update channel",Margin=new Thickness(0,0,0,6)});channel.SelectedItem=preferences.Channel;advanced.Children.Add(channel);
        var rollback=new Button{Content="Restore previous version",Margin=new Thickness(0,12,0,0)};rollback.Click+=async(_,_)=>await Run(Rollback);advanced.Children.Add(rollback);
        content.Children.Add(new Expander{Header="Advanced",Content=advanced});
        channel.SelectionChanged+=(_,_)=>Save();automatic.Checked+=(_,_)=>Save();automatic.Unchecked+=(_,_)=>Save();
        Loaded+=async(_,_)=>await Run(async()=>{await Check();if(preferences.ResultFile is string path&&File.Exists(path)){using var saved=JsonDocument.Parse(File.ReadAllText(path));description.Text+="\nLast worker result: "+saved.RootElement.GetProperty("message").GetString();}});
    }
    void Save(){var latest=UpdatePreferences.Load();preferences.Channel=latest.Channel=channel.SelectedItem as string??"preview";preferences.Automatic=latest.Automatic=automatic.IsChecked==true;latest.Save();}
    void Add(string label,Func<Task> action){var button=new Button{Content=label,Margin=new Thickness(0,6,0,6),Padding=new Thickness(8)};button.Click+=async(_,_)=>await Run(action);actions.Children.Add(button);}
    async Task Run(Func<Task> action)
    {
        if(busy)return;busy=true;actions.IsEnabled=false;channel.IsEnabled=false;description.Text="Checking for updates…";
        try{await action();}catch(Exception e){description.Text="Update unavailable: "+e.Message;}
        finally{busy=false;actions.IsEnabled=true;channel.IsEnabled=true;}
    }
    void Describe(JsonElement result)
    {
        description.Text=result.GetProperty("message").GetString();
        if(result.TryGetProperty("status",out var status)&&status.GetString()=="current")description.Text="You're up to date.";
        if(result.TryGetProperty("version",out var v))description.Text="Current "+v.GetString()+"\n"+description.Text;
        if(result.TryGetProperty("plan",out var plan))description.Text+="\nAvailable "+plan.GetProperty("tag").GetString();
    }
    async Task Check()
    {
        Save();apply.IsEnabled=false;var result=await UpdateProcess.DownloadAvailable(UpdatePreferences.Load(),text=>description.Text=text);Describe(result);
        if(!UpdateDownload.Ready(result))return;
        var ready=result.GetProperty("stage");stage=ready.GetProperty("directory").GetString();tag=ready.GetProperty("plan").GetProperty("tag").GetString();
        apply.IsEnabled=true;description.Text="Update "+tag+" is ready. Restart to install it and keep your settings.";
    }
    async Task Apply()
    {
        if(stage==null||tag==null)return;
        var result=await UpdateProcess.Call("preflight","--stage",stage,"--install",Payloads.Install);
        if(!UpdateDownload.Ready(result,tag)){Describe(result);apply.IsEnabled=false;return;}
        description.Text="Preparing to restart…";await prepareRestart();
        var latest=UpdatePreferences.Load();latest.Journal=Path.Combine(stage,"transaction.json");latest.ResultFile=Path.Combine(stage,"worker-result.json");latest.Save();
        var args=new List<string>{"apply","--stage",stage,"--install",Payloads.Install,"--wait-pid",Environment.ProcessId.ToString(),"--confirm","APPLY "+tag,"--result-file",latest.ResultFile!};
        if(relaunch.IsChecked==true)args.Add("--relaunch");
        UpdateProcess.StartDetached(args.ToArray());Close();shutdown();
    }
    async Task Rollback()
    {
        string? journal=UpdatePreferences.Load().Journal;
        if(journal==null||!File.Exists(journal)){description.Text="No applied update is recorded. Nothing changed.";return;}
        string install=Payloads.Install;
        var result=await UpdateProcess.Call("rollback-preflight","--journal",journal,"--install",install);
        Describe(result);
        if(!result.GetProperty("ok").GetBoolean())return;
        string version=result.GetProperty("stage").GetProperty("plan").GetProperty("tag").GetString()!;
        if(MessageBox.Show(this,"Close mac-lan and reinstall verified "+version+"? This downloads the signed older release again; external settings are preserved. "+(relaunch.IsChecked==true?"mac-lan will reopen.":"Reopen it yourself afterwards."),"Confirm rollback",MessageBoxButton.YesNo,MessageBoxImage.Question)!=MessageBoxResult.Yes)return;
        await prepareRestart();var latest=UpdatePreferences.Load();latest.ResultFile=Path.Combine(Path.GetDirectoryName(journal)!,"worker-result.json");latest.Save();
        var args=new List<string>{"rollback","--journal",journal,"--install",install,"--wait-pid",Environment.ProcessId.ToString(),"--confirm","ROLLBACK","--result-file",latest.ResultFile};
        if(relaunch.IsChecked==true)args.Add("--relaunch");
        UpdateProcess.StartDetached(args.ToArray());Close();shutdown();
    }
}
