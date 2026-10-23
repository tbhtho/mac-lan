using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;

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
    public void Save(){Directory.CreateDirectory(Path.GetDirectoryName(PathName)!);File.WriteAllText(PathName,JsonSerializer.Serialize(this));}
}
internal static class UpdateProcess
{
    internal static string Helper => Path.Combine(AppContext.BaseDirectory,"MAC-DOW-Update.exe");
    internal static async Task<JsonElement> Call(params string[] arguments)
    {
        if(!File.Exists(Helper))throw new InvalidOperationException("Update helper is missing. Extract the complete Windows release ZIP. Manual private releases: https://github.com/tbhtho/MAC-DOW/releases");
        var start=new ProcessStartInfo(Helper){UseShellExecute=false,CreateNoWindow=true,RedirectStandardOutput=true,RedirectStandardError=true};
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
    readonly TextBlock description=new(){TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,10,0,10)};
    readonly ComboBox channel=new(){ItemsSource=new[]{"stable","preview"},Width=160,HorizontalAlignment=HorizontalAlignment.Left};
    readonly CheckBox automatic=new(){Content="Check automatically while MAC-DOW is open",Margin=new Thickness(0,10,0,10)};
    readonly CheckBox relaunch=new(){Content="Reopen MAC-DOW after a verified apply/rollback",Margin=new Thickness(0,10,0,10)};
    readonly Button apply=new(){Content="Apply verified update",IsEnabled=false,Margin=new Thickness(0,6,0,6)};
    readonly Action shutdown;
    readonly StackPanel actions=new();
    string? stage;
    string? tag;
    bool busy;
    public UpdateDialog(Window owner,Action shutdown)
    {
        this.shutdown=shutdown;preferences=UpdatePreferences.Load();
        stage=preferences.Stage;
        Owner=owner;Title="MAC-DOW Updates";Width=650;Height=570;MinWidth=500;MinHeight=400;WindowStartupLocation=WindowStartupLocation.CenterOwner;
        var content=new StackPanel{Margin=new Thickness(24)};Content=new ScrollViewer{Content=content,VerticalScrollBarVisibility=ScrollBarVisibility.Auto};
        content.Children.Add(new TextBlock{Text="Updates",FontSize=26});
        content.Children.Add(new TextBlock{Text="Private GitHub releases. Existing GitHub CLI authentication only; no token is collected or saved.",TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,10,0,10)});
        channel.SelectedItem=preferences.Channel;content.Children.Add(channel);automatic.IsChecked=preferences.Automatic;content.Children.Add(automatic);
        content.Children.Add(description);content.Children.Add(actions);
        Add("Check now",Check);
        Add("Update - download and verify to staging",StageUpdate);
        apply.Click+=async(_,_)=>await Run(Apply);actions.Children.Add(apply);
        Add("Open staged files (manual review)",()=>{if(stage!=null&&Directory.Exists(stage))Process.Start(new ProcessStartInfo("explorer.exe"){ArgumentList={stage},UseShellExecute=false});return Task.CompletedTask;});
        Add("Rollback to previous signed release",Rollback);
        content.Children.Add(relaunch);
        content.Children.Add(new TextBlock{Text="Automatic replacement requires a cryptographically verified GitHub release attestation. Current mutable/unattested releases remain staged for manual review. Release provenance is distinct from Windows code signing. Settings stay outside the install folder.",TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,12,0,0)});
        channel.SelectionChanged+=(_,_)=>Save();automatic.Checked+=(_,_)=>Save();automatic.Unchecked+=(_,_)=>Save();
        Loaded+=async(_,_)=>await Run(async()=>{await Check();if(preferences.ResultFile is string path&&File.Exists(path)){using var saved=JsonDocument.Parse(File.ReadAllText(path));description.Text+="\nLast worker result: "+saved.RootElement.GetProperty("message").GetString();}});
    }
    void Save(){preferences.Channel=channel.SelectedItem as string??"preview";preferences.Automatic=automatic.IsChecked==true;preferences.Save();}
    void Add(string label,Func<Task> action){var button=new Button{Content=label,Margin=new Thickness(0,6,0,6),Padding=new Thickness(8)};button.Click+=async(_,_)=>await Run(action);actions.Children.Add(button);}
    async Task Run(Func<Task> action)
    {
        if(busy)return;busy=true;actions.IsEnabled=false;description.Text="Working securely; no app replacement without a separate confirmation...";
        try{await action();}catch(Exception e){description.Text="Update unavailable: "+e.Message;}
        finally{busy=false;actions.IsEnabled=true;}
    }
    void Describe(JsonElement result)
    {
        description.Text=result.GetProperty("message").GetString();
        if(result.TryGetProperty("version",out var v))description.Text="Current "+v.GetString()+"\n"+description.Text;
        if(result.TryGetProperty("plan",out var plan))description.Text+="\nAvailable "+plan.GetProperty("tag").GetString();
    }
    async Task Check(){Save();var result=await UpdateProcess.Call("check","--channel",preferences.Channel);Describe(result);apply.IsEnabled=false;}
    async Task StageUpdate()
    {
        Save();apply.IsEnabled=false;var result=await UpdateProcess.Call("stage","--channel",preferences.Channel);Describe(result);
        if(result.GetProperty("ok").GetBoolean()&&result.TryGetProperty("stage",out var s))
        {
            stage=s.GetProperty("directory").GetString();tag=s.GetProperty("plan").GetProperty("tag").GetString();preferences.Stage=stage;preferences.Save();
            apply.IsEnabled=s.GetProperty("canApply").GetBoolean();description.Text+="\nStaged: "+stage;
        }
    }
    async Task Apply()
    {
        if(stage==null||tag==null)return;
        var result=await UpdateProcess.Call("preflight","--stage",stage,"--install",AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar));
        if(!result.GetProperty("ok").GetBoolean()){Describe(result);apply.IsEnabled=false;return;}
        string restart=relaunch.IsChecked==true?"MAC-DOW will reopen in view-only mode.":"Reopen MAC-DOW yourself afterwards.";
        if(MessageBox.Show(this,"Apply "+tag+"? MAC-DOW will close. The update is reverified, the previous folder is retained for rollback, and external settings are preserved. "+restart,"Confirm update",MessageBoxButton.YesNo,MessageBoxImage.Question)!=MessageBoxResult.Yes){description.Text="Apply cancelled.";return;}
        preferences.Journal=Path.Combine(stage,"transaction.json");preferences.ResultFile=Path.Combine(stage,"worker-result.json");preferences.Save();
        var args=new List<string>{"apply","--stage",stage,"--install",AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar),"--wait-pid",Environment.ProcessId.ToString(),"--confirm","APPLY "+tag,"--result-file",preferences.ResultFile!};
        if(relaunch.IsChecked==true)args.Add("--relaunch");
        UpdateProcess.StartDetached(args.ToArray());Close();shutdown();
    }
    async Task Rollback()
    {
        string? journal=preferences.Journal;
        if(journal==null||!File.Exists(journal)){description.Text="No applied update is recorded. Nothing changed.";return;}
        string install=AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar);
        var result=await UpdateProcess.Call("rollback-preflight","--journal",journal,"--install",install);
        Describe(result);
        if(!result.GetProperty("ok").GetBoolean())return;
        string version=result.GetProperty("stage").GetProperty("plan").GetProperty("tag").GetString()!;
        if(MessageBox.Show(this,"Close MAC-DOW and reinstall verified "+version+"? This downloads the signed older release again; external settings are preserved. "+(relaunch.IsChecked==true?"MAC-DOW will reopen.":"Reopen it yourself afterwards."),"Confirm rollback",MessageBoxButton.YesNo,MessageBoxImage.Question)!=MessageBoxResult.Yes)return;
        preferences.ResultFile=Path.Combine(Path.GetDirectoryName(journal)!,"worker-result.json");preferences.Save();
        var args=new List<string>{"rollback","--journal",journal,"--install",install,"--wait-pid",Environment.ProcessId.ToString(),"--confirm","ROLLBACK","--result-file",preferences.ResultFile};
        if(relaunch.IsChecked==true)args.Add("--relaunch");
        UpdateProcess.StartDetached(args.ToArray());Close();shutdown();
    }
}
