using System.IO;
using System.Diagnostics;
using System.Net;
using System.Net.Security;
using System.Net.WebSockets;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
using MacDow.Core;
namespace MacDow.Windows;

internal sealed class Session : IAsyncDisposable
{
    readonly ClientWebSocket socket=new();
    readonly CancellationTokenSource stop=new();
    readonly Channel<string> outgoing=Channel.CreateBounded<string>(new BoundedChannelOptions(64){SingleReader=true,FullMode=BoundedChannelFullMode.Wait});
    readonly object lifecycle=new();
    const int New=0,Authenticating=1,Paired=2,Starting=3,Ready=4,Stopped=5;
    int phase;
    int maxFPS=30;
    string? remoteError;
    string? videoError;
    Uri? transferEndpoint;
    string? fingerprint;
    TransferTarget? transferTarget;
    Task? reader,writer;
    public event Action<byte[]>? Frame;
    public event Action<JsonElement>? Message;
    public event Action<string>? Ended;
    public bool IsReady => Volatile.Read(ref phase)==Ready;
    public bool IsPaired { get { int current=Volatile.Read(ref phase);return current is Paired or Starting or Ready; } }
    public bool IsStarting => Volatile.Read(ref phase)==Starting;
    public int MaxFPS => Volatile.Read(ref maxFPS);
    // Mac hosts from alpha.22 force a keyframe on request; older hosts close the session on unknown input.
    public bool SupportsKeyframes {get;private set;}
    public string? VideoError {get {lock(lifecycle)return videoError;}}
    public bool CanTransfer => TransferTarget!=null;
    public TransferTarget? TransferTarget {get {lock(lifecycle)return phase is Paired or Starting or Ready?transferTarget:null;}}
    public long NativeAlive {get;private set;}
    public long LastFrame {get;private set;}
    public Task Connect(string host,string port,string fingerprint,string token)
    {
        if(!Pairing.Validate(host,port,fingerprint,token))throw new ArgumentException("Enter the advanced connection details from your Mac.");
        return ConnectCore(host,port,fingerprint,new{type="auth",token});
    }
    public Task ConnectGuided(DiscoveredHost host,string pairNumber)
    {
        if(!DiscoveredHost.TryCreate(host.Name,host.Address,host.Port,host.Fingerprint,out _) ||
           pairNumber.Length!=6||pairNumber.Any(c=>c<'0'||c>'9'))throw new ArgumentException("Invalid guided pairing request.");
        return ConnectCore(host.Address,host.Port.ToString(System.Globalization.CultureInfo.InvariantCulture),host.Fingerprint,new{type="pair",code=pairNumber});
    }
    async Task ConnectCore(string host,string port,string fingerprint,object authentication)
    {
        lock(lifecycle){if(phase!=New)throw new InvalidOperationException("A session can connect only once.");phase=Authenticating;transferEndpoint=new Uri($"https://{host}:{port}/transfer");this.fingerprint=fingerprint.ToLowerInvariant();}
        socket.Options.RemoteCertificateValidationCallback=(_,cert,_,_)=>cert!=null&&Pin(cert,fingerprint);
        socket.Options.Proxy=null;
        using var timeout=CancellationTokenSource.CreateLinkedTokenSource(stop.Token);timeout.CancelAfter(TimeSpan.FromSeconds(8));
        await socket.ConnectAsync(new Uri($"wss://{host}:{port}/session"),timeout.Token);
        // Authentication must lead the wire even if UI focus changes during ConnectAsync.
        lock(lifecycle){if(phase==Stopped)return;outgoing.Writer.TryWrite(JsonSerializer.Serialize(authentication));}
        writer=WriteLoop();reader=ReadLoop();
    }
    static bool Pin(X509Certificate cert,string expected)
    {byte[] actual=SHA256.HashData(cert.GetRawCertData());return CryptographicOperations.FixedTimeEquals(actual,Convert.FromHexString(expected));}
    public void Send(object value)
    {
        var message=JsonSerializer.SerializeToElement(value);
        if(!message.TryGetProperty("type",out var kind)||kind.ValueKind!=JsonValueKind.String)return;
        bool overflow;
        lock(lifecycle)
        {
            var type=kind.GetString();
            if(type=="start")
            {
                if(phase!=Paired)return;
                if(!message.TryGetProperty("fps",out var requestedFPS)||requestedFPS.ValueKind!=JsonValueKind.Number||!requestedFPS.TryGetInt32(out var requested)||requested<60)
                    throw new InvalidOperationException("Video requires at least 60 fps. Update both mac-lan apps if needed.");
                if(requested>maxFPS)
                    throw new InvalidOperationException($"Update mac-lan on your Mac to stream at {requested} fps. This Mac host supports up to {maxFPS} fps.");
                phase=Starting;
            }
            else if(type=="lease"&&phase==Paired&&transferTarget!=null)
            {
                // A new host accepts idle keepalives so large file transfers do not expire before video starts.
                if(!message.TryGetProperty("active",out var active)||active.ValueKind!=JsonValueKind.False||!message.TryGetProperty("view",out var view)||view.ValueKind!=JsonValueKind.False)return;
            }
            else if(phase!=Ready||type is not ("release" or "lease" or "key" or "move" or "button" or "scroll"))return;
            overflow=!outgoing.Writer.TryWrite(message.GetRawText());
        }
        if(overflow)Abort("Input queue overflow; disconnected safely.");
    }
    async Task WriteLoop()
    {
        try{await foreach(var line in outgoing.Reader.ReadAllAsync(stop.Token)){using var deadline=CancellationTokenSource.CreateLinkedTokenSource(stop.Token);deadline.CancelAfter(1000);await socket.SendAsync(Encoding.UTF8.GetBytes(line),WebSocketMessageType.Text,true,deadline.Token);}}
        catch(Exception ex)when(ex is WebSocketException or OperationCanceledException){Abort("Connection ended.");}
    }
    async Task ReadLoop()
    {
        try
        {
            var buffer=new byte[65536];
            while(!stop.IsCancellationRequested)
            {
                using var message=new MemoryStream();WebSocketReceiveResult result;
                do{result=await socket.ReceiveAsync(buffer,stop.Token);if(result.MessageType==WebSocketMessageType.Close){Abort("Mac disconnected.");return;}message.Write(buffer,0,result.Count);if(message.Length>4*1024*1024)throw new InvalidDataException("Oversized frame.");}while(!result.EndOfMessage);
                if(result.MessageType==WebSocketMessageType.Binary){if(IsReady){var data=message.ToArray();if(data.Length>0&&data[0]!=4)LastFrame=Stopwatch.GetTimestamp();Frame?.Invoke(data);}}
                else
                {
                    using var doc=JsonDocument.Parse(message.ToArray());var value=doc.RootElement.Clone();
                    if(value.TryGetProperty("type",out var t)&&t.ValueKind==JsonValueKind.String)
                    {
                        lock(lifecycle)
                        {
                            if(phase==Stopped)return;
                            if(t.GetString()=="catalog")
                            {
                                if(phase!=Authenticating)throw new InvalidDataException("Unexpected pairing response.");
                                SupportsKeyframes=value.TryGetProperty("keyframes",out var keyframes)&&keyframes.ValueKind==JsonValueKind.True;
                                maxFPS=value.TryGetProperty("maxFps",out var advertised)&&advertised.ValueKind==JsonValueKind.Number&&advertised.TryGetInt32(out var maximum)&&maximum is >=5 and <=60?maximum:30;
                                if(value.TryGetProperty("videoError",out var captureError)&&captureError.ValueKind==JsonValueKind.String)
                                {
                                    string? text=captureError.GetString()?.Trim();
                                    if(!string.IsNullOrEmpty(text)){videoError=text[..Math.Min(text.Length,1024)];if(value.TryGetProperty("maxFps",out var unavailable)&&unavailable.ValueKind==JsonValueKind.Number&&unavailable.TryGetInt32(out int limit)&&limit==0)maxFPS=0;}
                                }
                                if(value.TryGetProperty("transferToken",out var transfer)&&transfer.ValueKind==JsonValueKind.String&&transfer.GetString() is string capability&&capability.Length==64&&capability.All(c=>c is >= '0' and <= '9' or >= 'a' and <= 'f'))
                                    transferTarget=new TransferTarget(transferEndpoint!,fingerprint!,capability,stop.Token);
                                phase=Paired;
                            }
                            if(t.GetString()=="ready"){if(phase!=Starting)throw new InvalidDataException("Unexpected stream response.");phase=Ready;}
                            if(t.GetString()=="captureAlive"&&phase==Ready)NativeAlive=Stopwatch.GetTimestamp();
                            if(t.GetString()=="error"&&value.TryGetProperty("message",out var error)&&error.ValueKind==JsonValueKind.String)
                            {
                                var text=error.GetString()?.Trim();
                                if(!string.IsNullOrEmpty(text))remoteError=text[..Math.Min(text.Length,1024)];
                            }
                        }
                    }
                    Message?.Invoke(value);
                }
            }
        }
        catch(Exception ex)when(ex is WebSocketException or OperationCanceledException or JsonException or InvalidDataException){Abort("Connection stopped: "+ex.Message);}
    }
    public void Abort(string reason)
    {lock(lifecycle){if(phase==Stopped)return;phase=Stopped;if(remoteError!=null)reason="Mac stopped: "+remoteError;stop.Cancel();outgoing.Writer.TryComplete();socket.Abort();}Ended?.Invoke(reason);}
    public async ValueTask DisposeAsync()
    {Abort("Disconnected; keys released by Mac watchdog.");try{await Task.WhenAll(reader??Task.CompletedTask,writer??Task.CompletedTask);}catch(OperationCanceledException){}socket.Dispose();stop.Dispose();}
}
