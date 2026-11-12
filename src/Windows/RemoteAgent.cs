using System.Collections.Concurrent;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;

namespace MacDow.Windows;

/// <summary>What the remote-command link looks like right now.</summary>
internal sealed record RemoteState(bool Connected,string PeerName,bool PeerAccepts,string PeerNote,bool Accept,bool Agents,bool Busy,IReadOnlyList<string> Trusted)
{
    public static readonly RemoteState None=new(false,"your Mac",false,"",false,false,false,[]);
}

/// <summary>An action that needs the user's approval before it is sent to the Mac.</summary>
/// <summary>Remember, when not empty, words a third choice that also stops this request asking again.</summary>
internal sealed record RemotePromptRequest(string Id,string Title,string Detail,string Reason,string Origin,string Remember);

/// <summary>
/// Owns the bundled helper in remote-agent mode for the life of mac-lan. The helper holds the
/// link to the Mac's /remote endpoint, runs commands the Mac sends once the user allows that,
/// and serves local tools (the MCP server, the command-line shell) on loopback. This window
/// talks to it over the helper's private pipes, one JSON object per line; only this channel may
/// change the two switches or answer approval prompts.
/// </summary>
internal sealed class RemoteAgent : IDisposable
{
    readonly ConcurrentDictionary<int,TaskCompletionSource<JsonElement>> pending=new();
    readonly object gate=new();
    Process? process;
    StreamWriter? input;
    int nextRequest;
    bool disposed;
    public RemoteState State {get;private set;}=RemoteState.None;
    /// <summary>Raised on a background thread.</summary>
    public event Action? StateChanged;
    /// <summary>Raised on a background thread; answer with <see cref="Answer"/>.</summary>
    public event Action<RemotePromptRequest>? Prompt;
    /// <summary>Raised on a background thread when a prompt no longer needs an answer (cancelled or expired).</summary>
    public event Action<string>? PromptWithdrawn;
    public bool Available {get {lock(gate)return process is {HasExited:false};}}

    /// <summary>Starts the helper. False when this build has no bundled helper.</summary>
    public bool Start()
    {
        lock(gate)
        {
            if(disposed)return false;
            if(process is {HasExited:false})return true;
            try
            {
                if(!File.Exists(Payloads.Helper))return false;
                var start=new ProcessStartInfo(Payloads.Helper){UseShellExecute=false,CreateNoWindow=true,RedirectStandardInput=true,RedirectStandardOutput=true,RedirectStandardError=true,
                    StandardOutputEncoding=Encoding.UTF8,StandardErrorEncoding=Encoding.UTF8,StandardInputEncoding=new UTF8Encoding(false)};
                start.ArgumentList.Add("remote-agent");
                var started=Process.Start(start);
                if(started==null)return false;
                process=started;input=started.StandardInput;input.AutoFlush=true;
                _=Task.Run(()=>Read(started));
                _=Task.Run(async()=>{try{await started.StandardError.ReadToEndAsync();}catch(Exception e)when(e is IOException or ObjectDisposedException){}});
            }
            catch(Exception e)when(e is IOException or System.ComponentModel.Win32Exception or InvalidOperationException or UnauthorizedAccessException)
            {Telemetry.Log.Record("remote-agent-failed","start",error:e);return false;}
        }
        _=Refresh();
        return true;
    }
    async Task Read(Process source)
    {
        try
        {
            while(await source.StandardOutput.ReadLineAsync() is string line)
            {
                if(line.Length==0||line.Length>8*1024*1024)continue;
                JsonElement message;
                try{using var doc=JsonDocument.Parse(line);message=doc.RootElement.Clone();}catch(JsonException){continue;}
                if(message.ValueKind!=JsonValueKind.Object)continue;
                if(message.TryGetProperty("rid",out var rid)&&rid.ValueKind==JsonValueKind.Number&&rid.TryGetInt32(out int id))
                {
                    if(pending.TryRemove(id,out var waiter))waiter.TrySetResult(message);
                    continue;
                }
                if(!message.TryGetProperty("ev",out var kind)||kind.ValueKind!=JsonValueKind.String)continue;
                switch(kind.GetString())
                {
                    case "state":Apply(message);break;
                    case "confirm":Prompt?.Invoke(new(Text(message,"id"),Text(message,"title"),Text(message,"detail"),Text(message,"reason"),Text(message,"origin"),Text(message,"remember")));break;
                    case "confirm-expired":PromptWithdrawn?.Invoke(Text(message,"id"));break;
                }
            }
        }
        catch(Exception e)when(e is IOException or ObjectDisposedException or InvalidOperationException){}
        // The helper ended: nothing can answer any more.
        foreach(var id in pending.Keys)if(pending.TryRemove(id,out var waiter))waiter.TrySetException(new IOException("mac-lan's remote helper stopped. Reopen mac-lan."));
        State=RemoteState.None;StateChanged?.Invoke();
    }
    static string Text(JsonElement message,string name)=>message.TryGetProperty(name,out var value)&&value.ValueKind==JsonValueKind.String?value.GetString()??"":"";
    static bool Flag(JsonElement message,string name)=>message.TryGetProperty(name,out var value)&&value.ValueKind==JsonValueKind.True;
    void Apply(JsonElement message)
    {
        bool connected=false,accepts=false;string name="your Mac",note="";
        if(message.TryGetProperty("devices",out var devices)&&devices.ValueKind==JsonValueKind.Array)
            foreach(var device in devices.EnumerateArray())
            {
                if(Flag(device,"this_device"))continue;
                connected=Flag(device,"connected")&&Text(device,"platform").Length>0;accepts=Flag(device,"accepts_commands");note=Text(device,"note");
                if(Text(device,"platform").Length>0&&Text(device,"name") is {Length:>0} peer)name=peer;
            }
        var trusted=new List<string>();
        if(message.TryGetProperty("trusted",out var folders)&&folders.ValueKind==JsonValueKind.Array)
            foreach(var folder in folders.EnumerateArray())if(folder.ValueKind==JsonValueKind.String&&folder.GetString() is {Length:>0} path)trusted.Add(path);
        State=new(connected,name,accepts,note,Flag(message,"accept"),Flag(message,"agents"),Flag(message,"busy"),trusted);
        StateChanged?.Invoke();
    }
    /// <summary>Sends one request; throws IOException with the helper's explanation when refused.</summary>
    public async Task<JsonElement> Call(Dictionary<string,object?> request,int waitSeconds=0)
    {
        int id=Interlocked.Increment(ref nextRequest);
        request["rid"]=id;if(waitSeconds>0)request["wait"]=waitSeconds;
        var waiter=new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        pending[id]=waiter;
        try
        {
            lock(gate)
            {
                if(input==null||process is not {HasExited:false})throw new IOException("Remote commands are unavailable in this build of mac-lan.");
                input.WriteLine(JsonSerializer.Serialize(request));
            }
            var answer=await waiter.Task.WaitAsync(TimeSpan.FromSeconds(waitSeconds+60));
            if(!Flag(answer,"ok"))throw new IOException(Text(answer,"error") is {Length:>0} text?text:"mac-lan could not do that.");
            return answer;
        }
        catch(Exception e)when(e is TimeoutException or ObjectDisposedException){throw new IOException("mac-lan's remote helper did not answer.",e);}
        finally{pending.TryRemove(id,out _);}
    }
    async Task Refresh(){try{Apply(await Call(new(){["op"]="state"}));}catch(IOException){}}
    /// <summary>Connects the helper to the Mac this session is paired with.</summary>
    public void Link(TransferTarget target)
    {
        if(!Start())return;
        _=Send(new(){["op"]="link",["address"]=target.Endpoint.Host,["port"]=target.Endpoint.Port,["fingerprint"]=target.Fingerprint,["token"]=target.Token});
    }
    public void Unlink(){if(Available)_=Send(new(){["op"]="unlink"});}
    /// <summary>Drops what this PC remembers about the Mac's standby link, when the Mac is forgotten.</summary>
    public void ForgetStandby(){if(Available)_=Send(new(){["op"]="forget-standby"});}
    public void Answer(string id,bool allow,bool always=false)=>_=Send(new(){["op"]="confirm",["id"]=id,["allow"]=allow,["always"]=allow&&always});
    /// <summary>Adds or removes a Mac folder in which files may be replaced without asking. Throws IOException with the reason when refused.</summary>
    public async Task Trust(string folder,bool trust){Apply(await Call(new(){["op"]=trust?"trust":"untrust",["path"]=folder}));}
    public Task Set(bool? accept=null,bool? agents=null)
    {
        var request=new Dictionary<string,object?>{["op"]="set"};
        if(accept!=null)request["accept"]=accept;if(agents!=null)request["agents"]=agents;
        return Send(request,true);
    }
    async Task Send(Dictionary<string,object?> request,bool apply=false)
    {
        try{var answer=await Call(request);if(apply)Apply(answer);}
        catch(IOException e){Telemetry.Log.Record("remote-agent-failed",request["op"] as string??"request",error:e);}
    }
    public void Dispose()
    {
        Process? ending;
        lock(gate){if(disposed)return;disposed=true;ending=process;process=null;try{input?.Close();}catch(Exception e)when(e is IOException or ObjectDisposedException){}input=null;}
        if(ending==null)return;
        // Closing the pipe asks the helper to end its commands and exit; do not leave it behind.
        try{if(!ending.WaitForExit(1500))ending.Kill(true);}catch(Exception e)when(e is InvalidOperationException or System.ComponentModel.Win32Exception){}
        ending.Dispose();
    }
}
