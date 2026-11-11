using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Threading;
using Microsoft.Win32;

namespace MacDow.Windows;

/// <summary>
/// Runs shell commands on the paired Mac and shows their output as it arrives. Each line is one
/// command; "cd" changes the folder later commands run in. It is not a terminal emulator:
/// full-screen programs and password prompts are not supported.
/// </summary>
internal sealed class TerminalWindow : Window
{
    const int MaxOutputChars=400_000;
    readonly RemoteAgent agent;
    readonly TextBox output=new(){IsReadOnly=true,AcceptsReturn=true,TextWrapping=TextWrapping.Wrap,VerticalScrollBarVisibility=ScrollBarVisibility.Auto,HorizontalScrollBarVisibility=ScrollBarVisibility.Disabled,
        FontFamily=new FontFamily("Cascadia Mono, Consolas"),FontSize=13,BorderThickness=new Thickness(0),Background=Brushes.Transparent,Padding=new Thickness(12),IsUndoEnabled=false};
    readonly TextBox command=new(){FontFamily=new FontFamily("Cascadia Mono, Consolas"),FontSize=13,Padding=new Thickness(10,8,10,8),VerticalContentAlignment=VerticalAlignment.Center};
    readonly TextBlock folder=new(){FontFamily=new FontFamily("Cascadia Mono, Consolas"),FontSize=12,Foreground=Glass.Muted,TextWrapping=TextWrapping.NoWrap,TextTrimming=TextTrimming.CharacterEllipsis,Margin=new Thickness(2,0,0,6)};
    readonly TextBlock link=new(){Foreground=Glass.Muted,FontSize=12,Margin=new Thickness(0,6,0,14)};
    readonly Button run,send;
    readonly CheckBox accept=new(){Content="Let my Mac run commands on this PC",Foreground=Glass.Ink,Margin=new Thickness(0,0,0,8)};
    readonly CheckBox agents=new(){Content="Let AI agents and local tools on this PC use the link (MCP)",Foreground=Glass.Ink,Margin=new Thickness(0,0,0,8)};
    readonly List<string> history=[];
    int historyIndex;
    string cwd="";
    string? running;
    bool applying,closed;

    public TerminalWindow(RemoteAgent agent)
    {
        this.agent=agent;Title="mac-lan · Terminal";Width=860;Height=640;MinWidth=560;MinHeight=420;WindowStartupLocation=WindowStartupLocation.CenterScreen;Glass.Apply(this);
        var layout=new Grid{Margin=new Thickness(24,22,24,20)};
        layout.RowDefinitions.Add(new(){Height=GridLength.Auto});layout.RowDefinitions.Add(new());layout.RowDefinitions.Add(new(){Height=GridLength.Auto});layout.RowDefinitions.Add(new(){Height=GridLength.Auto});
        var heading=new StackPanel();
        heading.Children.Add(new TextBlock{Text="Terminal on your Mac",FontSize=24,FontWeight=FontWeights.SemiBold});
        heading.Children.Add(link);layout.Children.Add(heading);
        var screen=new Border{Background=new SolidColorBrush(Color.FromArgb(0x66,0,0,0)),BorderBrush=Glass.Line,BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(10),Child=output};
        Grid.SetRow(screen,1);layout.Children.Add(screen);

        var entry=new Grid{Margin=new Thickness(0,12,0,0)};entry.ColumnDefinitions.Add(new());entry.ColumnDefinitions.Add(new(){Width=GridLength.Auto});entry.ColumnDefinitions.Add(new(){Width=GridLength.Auto});
        entry.RowDefinitions.Add(new(){Height=GridLength.Auto});entry.RowDefinitions.Add(new(){Height=GridLength.Auto});
        Grid.SetColumnSpan(folder,3);entry.Children.Add(folder);
        Grid.SetRow(command,1);entry.Children.Add(command);
        run=new Button{Content="Run",MinWidth=92,Margin=new Thickness(8,0,0,0),Padding=new Thickness(14,8,14,8),Style=(Style)FindResource("PrimaryButton")};
        run.Click+=async(_,_)=>{if(running!=null)await Stop();else await Submit();};
        Grid.SetRow(run,1);Grid.SetColumn(run,1);entry.Children.Add(run);
        send=new Button{Content="Send files here…",Margin=new Thickness(8,0,0,0),Padding=new Thickness(14,8,14,8),ToolTip="Copy files from this PC into the Mac folder shown above. Existing files are not replaced."};
        send.Click+=async(_,_)=>await SendFiles();
        Grid.SetRow(send,1);Grid.SetColumn(send,2);entry.Children.Add(send);
        Grid.SetRow(entry,2);layout.Children.Add(entry);

        var access=new StackPanel{Margin=new Thickness(0,10,0,0)};
        access.Children.Add(new TextBlock{Text="Both are off until you turn them on. Commands run with your Windows account's rights. Deleting, system and administrator commands, replacing files and deployments always ask you first.",Foreground=Glass.Muted,FontSize=12,Margin=new Thickness(0,0,0,10)});
        access.Children.Add(accept);access.Children.Add(agents);
        var copy=new Button{Content="Copy MCP setup command",HorizontalAlignment=HorizontalAlignment.Left,Padding=new Thickness(12,6,12,6),ToolTip="Registers mac-lan as an MCP server in Claude Code. Other agents use the same program with --mcp."};
        copy.Click+=(_,_)=>CopySetup();access.Children.Add(copy);
        var options=new Expander{Header="Access for the Mac and AI agents",Content=access,Margin=new Thickness(0,12,0,0),Foreground=Glass.Ink};
        Grid.SetRow(options,3);layout.Children.Add(options);
        Content=layout;

        accept.Click+=async(_,_)=>{if(applying)return;bool on=accept.IsChecked==true;if(on&&!RemotePrompt.Ask(this,"Let your Mac run commands on this PC?","From its mac-lan menu, or from AI agents you allowed on the Mac, your paired Mac can then run commands and read or write files here with your Windows account's rights.","Destructive commands are confirmed on the Mac first. Every request is recorded in mac-lan's remote-audit.log on this PC.","This PC","Turn on")){ShowState();return;}await agent.Set(accept:on);ShowState();};
        agents.Click+=async(_,_)=>{if(applying)return;bool on=agents.IsChecked==true;if(on&&!RemotePrompt.Ask(this,"Let AI agents on this PC use the link?","Programs running as you on this PC, such as an AI coding agent connected through mac-lan's MCP server, can then run commands and transfer files on your Mac if the Mac allows commands.","Destructive commands, replacing files and deployments still ask you here first.","This PC","Turn on")){ShowState();return;}await agent.Set(agents:on);ShowState();};
        command.PreviewKeyDown+=async(_,e)=>
        {
            if(e.Key==Key.Enter){e.Handled=true;if(running==null)await Submit();}
            else if(e.Key==Key.Up&&history.Count>0){e.Handled=true;historyIndex=Math.Max(0,historyIndex-1);command.Text=history[historyIndex];command.CaretIndex=command.Text.Length;}
            else if(e.Key==Key.Down&&history.Count>0){e.Handled=true;historyIndex=Math.Min(history.Count,historyIndex+1);command.Text=historyIndex<history.Count?history[historyIndex]:"";command.CaretIndex=command.Text.Length;}
            else if(e.Key==Key.C&&Keyboard.Modifiers==ModifierKeys.Control&&command.SelectionLength==0&&running!=null){e.Handled=true;await Stop();}
        };
        agent.StateChanged+=OnState;
        Closed+=(_,_)=>{closed=true;agent.StateChanged-=OnState;};
        Loaded+=async(_,_)=>{ShowState();command.Focus();await ResolveHome();};
        Print("Each line runs on your Mac as one command. \"cd\" changes folder; Ctrl+C or Stop ends a running command.\n");
    }
    void OnState()=>Dispatcher.BeginInvoke(()=>{if(closed)return;bool was=cwd.Length>0;ShowState();if(!was&&agent.State is {Connected:true,PeerAccepts:true})_=ResolveHome();},DispatcherPriority.Background);
    void ShowState()
    {
        var state=agent.State;
        applying=true;accept.IsChecked=state.Accept;agents.IsChecked=state.Agents;applying=false;
        bool ready=state.Connected&&state.PeerAccepts;
        link.Text=!agent.Available?"Remote commands are unavailable in this build of mac-lan."
            :!state.Connected?"Not connected. Connect to your Mac in mac-lan; this window reconnects by itself."
            :!state.PeerAccepts?$"Connected to {state.PeerName}, but it does not accept commands yet. On the Mac, open the mac-lan menu → Remote Commands… and turn on \"Let the paired PC run commands on this Mac\"."
            :$"Connected to {state.PeerName}. Commands run there in zsh with your Mac account's rights.";
        link.Foreground=ready?Glass.Muted:Glass.Warn;
        command.IsEnabled=ready&&running==null;send.IsEnabled=ready&&running==null&&cwd.Length>0;
        run.IsEnabled=ready||running!=null;run.Content=running!=null?"Stop":"Run";
        folder.Text=cwd.Length>0?cwd:ready?"…":"";
        Title=ready?"mac-lan · Terminal · "+state.PeerName:"mac-lan · Terminal";
    }
    void Print(string text)
    {
        if(text.Length==0||closed)return;
        output.AppendText(text.Replace("\r\n","\n").Replace('\r','\n'));
        // Keep the newest output; a long build must not grow this window without bound.
        if(output.Text.Length>MaxOutputChars)output.Text=output.Text[^(MaxOutputChars*3/4)..];
        output.CaretIndex=output.Text.Length;output.ScrollToEnd();
    }
    static string Text(JsonElement value,string name)=>value.TryGetProperty(name,out var v)&&v.ValueKind==JsonValueKind.String?v.GetString()??"":"";
    async Task ResolveHome()
    {
        if(cwd.Length>0||agent.State is not {Connected:true,PeerAccepts:true})return;
        try{cwd=Text(await agent.Call(new(){["op"]="resolve",["path"]="",["cwd"]=""}),"path");}catch(IOException){}
        ShowState();
    }
    async Task Submit()
    {
        string line=command.Text.Trim();
        if(line.Length==0||running!=null)return;
        if(history.Count==0||history[^1]!=line)history.Add(line);
        if(history.Count>200)history.RemoveAt(0);
        historyIndex=history.Count;command.Clear();
        Print($"\n{cwd}$ {line}\n");
        if(line=="clear"||line=="cls"){output.Clear();return;}
        if(line=="cd"||line.StartsWith("cd ",StringComparison.Ordinal))
        {
            string target=line.Length>2?line[2..].Trim().Trim('"','\''):"";
            try{cwd=Text(await agent.Call(new(){["op"]="resolve",["path"]=target.Length==0?"~":target,["cwd"]=cwd}),"path");}
            catch(IOException e){Print(e.Message+"\n");}
            ShowState();return;
        }
        await Follow(new(){["op"]="exec",["command"]=line,["cwd"]=cwd});
    }
    // Starts a command or transfer and prints its output until it ends.
    async Task Follow(Dictionary<string,object?> request)
    {
        try
        {
            var job=(await agent.Call(request)).GetProperty("job");
            running=Text(job,"execution_id");ShowState();
            string noted="";
            while(true)
            {
                Print(Text(job,"output"));
                string note=Text(job,"note");
                if(note.Length>0&&note!=noted){noted=note;Print("["+note+"]\n");}
                if(job.TryGetProperty("finished",out var finished)&&finished.ValueKind==JsonValueKind.True)
                {
                    string state=Text(job,"state");
                    if(state!="succeeded")Print(job.TryGetProperty("exit_code",out var code)&&code.ValueKind==JsonValueKind.Number?$"[{state}, exit code {code.GetInt32()}]\n":$"[{state}]\n");
                    break;
                }
                if(closed)return;
                long since=job.TryGetProperty("next_offset",out var next)&&next.TryGetInt64(out long offset)?offset:0;
                job=(await agent.Call(new(){["op"]="status",["id"]=running,["since"]=since},10)).GetProperty("job");
            }
        }
        catch(Exception e)when(e is IOException or KeyNotFoundException or InvalidOperationException){Print(e.Message+"\n");}
        finally{running=null;if(!closed){ShowState();command.Focus();}}
    }
    async Task Stop()
    {
        if(running is not string id)return;
        try{await agent.Call(new(){["op"]="cancel",["id"]=id});}catch(IOException e){Print(e.Message+"\n");}
    }
    async Task SendFiles()
    {
        if(running!=null||cwd.Length==0)return;
        var picker=new OpenFileDialog{Title="Choose files to copy into "+cwd+" on your Mac",Multiselect=true,CheckFileExists=true};
        if(picker.ShowDialog(this)!=true)return;
        foreach(string path in picker.FileNames)
        {
            if(closed)return;
            Print($"\ncopy {path} → {cwd}\n");
            await Follow(new(){["op"]="transfer",["direction"]="push",["local"]=path,["remote"]=cwd});
        }
    }
    void CopySetup()
    {
        string text="claude mcp add mac-lan -- \""+Payloads.Install+"\" --mcp";
        try{Clipboard.SetText(text);Print("\nCopied: "+text+"\nRun it once in a terminal to register mac-lan with Claude Code. Other agents (Codex and similar) use the same program and argument as a stdio MCP server. Also turn on the AI agents switch below.\n");}
        catch(System.Runtime.InteropServices.COMException){Print("\n"+text+"\n");}
    }
}

/// <summary>
/// Asks the user to approve something that changes the other computer. Topmost and without an
/// owner, because a request from an AI agent can arrive while every mac-lan window is hidden.
/// Deny is the default and the answer after 55 seconds.
/// </summary>
internal sealed class RemotePrompt : Window
{
    RemotePrompt(Window? owner,string title,string detail,string reason,string origin,string action)
    {
        WindowStyle=WindowStyle.None;ResizeMode=ResizeMode.NoResize;SizeToContent=SizeToContent.WidthAndHeight;ShowInTaskbar=owner==null;Topmost=true;Title="mac-lan";
        if(owner is {IsVisible:true}){Owner=owner;WindowStartupLocation=WindowStartupLocation.CenterOwner;}else WindowStartupLocation=WindowStartupLocation.CenterScreen;
        Glass.Apply(this);
        var panel=new StackPanel{Width=460,Margin=new Thickness(30,26,30,22)};
        panel.Children.Add(new TextBlock{Text=origin,Foreground=Glass.Warn,FontSize=12,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,8)});
        panel.Children.Add(new TextBlock{Text=title,FontSize=18,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,12)});
        panel.Children.Add(new Border{Background=new SolidColorBrush(Color.FromArgb(0x66,0,0,0)),BorderBrush=Glass.Line,BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(8),Padding=new Thickness(12,10,12,10),Margin=new Thickness(0,0,0,12),
            Child=new ScrollViewer{MaxHeight=180,VerticalScrollBarVisibility=ScrollBarVisibility.Auto,Content=new TextBlock{Text=detail.Length>4000?detail[..4000]+"…":detail,FontFamily=new FontFamily("Cascadia Mono, Consolas"),FontSize=13}}});
        panel.Children.Add(new TextBlock{Text=reason,Foreground=Glass.Muted,FontSize=13,LineHeight=19,Margin=new Thickness(0,0,0,20)});
        var buttons=new StackPanel{Orientation=Orientation.Horizontal,HorizontalAlignment=HorizontalAlignment.Right};
        var deny=new Button{Content="Deny",IsCancel=true,IsDefault=true,MinWidth=96,Margin=new Thickness(0,0,10,0)};
        var allow=new Button{Content=action,MinWidth=96,Style=(Style)FindResource("PrimaryButton")};
        allow.Click+=(_,_)=>DialogResult=true;
        buttons.Children.Add(deny);buttons.Children.Add(allow);panel.Children.Add(buttons);Content=panel;
        MouseLeftButtonDown+=(_,e)=>{if(e.ButtonState==MouseButtonState.Pressed)DragMove();};
        var expiry=new DispatcherTimer{Interval=TimeSpan.FromSeconds(55)};
        expiry.Tick+=(_,_)=>{expiry.Stop();if(IsVisible)DialogResult=false;};
        Loaded+=(_,_)=>{expiry.Start();Activate();deny.Focus();};Closed+=(_,_)=>expiry.Stop();
    }
    public static bool Ask(Window? owner,string title,string detail,string reason,string origin,string action="Allow")=>new RemotePrompt(owner,title,detail,reason,origin,action).ShowDialog()==true;
}
