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
    readonly DiagnosticLog diagnostics;
    internal string DiagnosticId {get;}=Guid.NewGuid().ToString("N");
    System.Threading.Timer? diagnosticTimer;
    long videoPackets,audioPackets,receivedBytes,sentMessages,lastPong,lastLeaseQueued,lastLeaseSent,maxLeaseSendGap,lastSendDuration,lastRtt,droppedInput,lastInputDrop;
    bool? loggedLeaseActive,loggedLeaseView;
    ulong loggedGeneration;
    long lastUiTick=Stopwatch.GetTimestamp(),lastHealth;
    int uiStalled;
    internal int VideoQueueFrames=0,VideoQueueBytes=0,PendingAudioDispatches=0;
    internal Session(DiagnosticLog? diagnostics=null){this.diagnostics=diagnostics??Telemetry.Log;Log("session-created");}
    internal void Log(string name,string code="ok",object? state=null,Exception? error=null)=>diagnostics.Record(name,code,state??Snapshot(),DiagnosticId,error);
    internal void UiTick()
    {
        long previous=Interlocked.Exchange(ref lastUiTick,Stopwatch.GetTimestamp());
        if(previous>0&&Stopwatch.GetElapsedTime(previous).TotalMilliseconds>=750)Log("ui-heartbeat-gap","delayed",new{gapMs=Age(previous)});
    }
    void SampleHealth()
    {
        if(Volatile.Read(ref phase)==Stopped)return;
        bool stalled=Age(Interlocked.Read(ref lastUiTick)) is double age&&age>=1000;
        int was=Interlocked.Exchange(ref uiStalled,stalled?1:0);
        if(stalled&&was==0)Log("ui-heartbeat-stalled","delayed");
        if(!stalled&&was==1)Log("ui-heartbeat-resumed");
        if(lastHealth==0||Stopwatch.GetElapsedTime(lastHealth).TotalSeconds>=5)
        {
            lastHealth=Stopwatch.GetTimestamp();Log("transport-health");
            try
            {
                using var process=Process.GetCurrentProcess();
                Log("process-health",state:new{workingSetBytes=process.WorkingSet64,privateBytes=process.PrivateMemorySize64,cpuTotalMs=process.TotalProcessorTime.TotalMilliseconds,managedBytes=GC.GetTotalMemory(false),gen0=GC.CollectionCount(0),gen1=GC.CollectionCount(1),gen2=GC.CollectionCount(2),threadPoolPending=ThreadPool.PendingWorkItemCount,threadPoolThreads=ThreadPool.ThreadCount});
            }
            catch(Exception ex)when(ex is InvalidOperationException or System.ComponentModel.Win32Exception){Log("process-health-unavailable","sample",new{unavailable=true},ex);}
        }
    }
    static double? Age(long stamp)=>stamp>0?Math.Round(Stopwatch.GetElapsedTime(stamp).TotalMilliseconds,1):null;
    object Snapshot()=>new{phase=Volatile.Read(ref phase),socketState=socket.State.ToString(),closeStatus=(int?)socket.CloseStatus,closeDescriptionLength=socket.CloseStatusDescription?.Length??0,
        videoPackets=Interlocked.Read(ref videoPackets),audioPackets=Interlocked.Read(ref audioPackets),receivedBytes=Interlocked.Read(ref receivedBytes),sentMessages=Interlocked.Read(ref sentMessages),sendQueueDepth=outgoing.Reader.Count,
        lastVideoAgeMs=Age(LastFrame),lastNativeAgeMs=Age(NativeAlive),lastPongAgeMs=Age(Interlocked.Read(ref lastPong)),lastLeaseQueuedAgeMs=Age(Interlocked.Read(ref lastLeaseQueued)),lastLeaseSentAgeMs=Age(Interlocked.Read(ref lastLeaseSent)),
        maxLeaseSendGapMs=Interlocked.Read(ref maxLeaseSendGap),lastSendDurationMs=Interlocked.Read(ref lastSendDuration),lastRttMs=Interlocked.Read(ref lastRtt),droppedInput=Interlocked.Read(ref droppedInput),uiTickAgeMs=Age(Interlocked.Read(ref lastUiTick)),videoQueueFrames=Volatile.Read(ref VideoQueueFrames),videoQueueBytes=Volatile.Read(ref VideoQueueBytes),pendingAudioDispatches=Volatile.Read(ref PendingAudioDispatches),hostError=DiagnosticHostFields.Failure(remoteError)};
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
        Log("connect-start");
        socket.Options.RemoteCertificateValidationCallback=(_,cert,_,_)=>cert!=null&&Pin(cert,fingerprint);
        socket.Options.Proxy=null;
        using var timeout=CancellationTokenSource.CreateLinkedTokenSource(stop.Token);timeout.CancelAfter(TimeSpan.FromSeconds(8));
        try{await socket.ConnectAsync(new Uri($"wss://{host}:{port}/session"),timeout.Token).ConfigureAwait(false);}
        catch(Exception ex){Log("transport-failed","connect",error:ex);throw;}
        Log("transport-connected");
        // Authentication must lead the wire even if UI focus changes during ConnectAsync.
        lock(lifecycle)
        {
            if(phase==Stopped)return;outgoing.Writer.TryWrite(JsonSerializer.Serialize(authentication));
            diagnosticTimer=new System.Threading.Timer(_=>SampleHealth(),null,TimeSpan.FromSeconds(1),TimeSpan.FromSeconds(1));
        }
        writer=WriteLoop();reader=ReadLoop();
    }
    static bool Pin(X509Certificate cert,string expected)
    {byte[] actual=SHA256.HashData(cert.GetRawCertData());return CryptographicOperations.FixedTimeEquals(actual,Convert.FromHexString(expected));}
    public void Send(object value)
    {
        var message=JsonSerializer.SerializeToElement(value);
        if(!message.TryGetProperty("type",out var kind)||kind.ValueKind!=JsonValueKind.String)return;
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
                Log("stream-request",state:DiagnosticHostFields.Stream(message));
            }
            else if(type=="lease"&&phase==Paired&&transferTarget!=null)
            {
                // A new host accepts idle keepalives so large file transfers do not expire before video starts.
                if(!message.TryGetProperty("active",out var active)||active.ValueKind!=JsonValueKind.False||!message.TryGetProperty("view",out var view)||view.ValueKind!=JsonValueKind.False)return;
            }
            else if(phase!=Ready||type is not ("release" or "lease" or "key" or "move" or "button" or "scroll"))return;
            if(!outgoing.Writer.TryWrite(message.GetRawText()))
            {
                // Input pressure never ends an approved stream: a move is an absolute
                // position and the next lease repeats control state, so dropped input
                // corrects itself on the next 250 ms heartbeat.
                Interlocked.Increment(ref droppedInput);
                long previous=Interlocked.Read(ref lastInputDrop);
                if(previous==0||Stopwatch.GetElapsedTime(previous).TotalMilliseconds>=1000)
                {
                    Interlocked.Exchange(ref lastInputDrop,Stopwatch.GetTimestamp());
                    Log("input-dropped","queue-full",new{type,dropped=Interlocked.Read(ref droppedInput),sendQueueDepth=outgoing.Reader.Count});
                }
            }
            if(type=="lease")
            {
                Interlocked.Exchange(ref lastLeaseQueued,Stopwatch.GetTimestamp());
                bool active=message.TryGetProperty("active",out var a)&&a.ValueKind==JsonValueKind.True;
                bool view=message.TryGetProperty("view",out var v)&&v.ValueKind==JsonValueKind.True;
                ulong generation=message.TryGetProperty("generation",out var g)&&g.TryGetUInt64(out ulong n)?n:0;
                if(loggedLeaseActive!=active||loggedLeaseView!=view||loggedGeneration!=generation)
                {loggedLeaseActive=active;loggedLeaseView=view;loggedGeneration=generation;Log("lease-state",state:new{active,view,generation});}
            }
        }
    }
    async Task WriteLoop()
    {
        try
        {
            await foreach(var line in outgoing.Reader.ReadAllAsync(stop.Token).ConfigureAwait(false))
            {
                using var metadata=JsonDocument.Parse(line);bool lease=metadata.RootElement.GetProperty("type").GetString()=="lease";
                long began=Stopwatch.GetTimestamp();
                using var deadline=CancellationTokenSource.CreateLinkedTokenSource(stop.Token);deadline.CancelAfter(1000);
                await socket.SendAsync(Encoding.UTF8.GetBytes(line),WebSocketMessageType.Text,true,deadline.Token).ConfigureAwait(false);
                long elapsed=(long)Stopwatch.GetElapsedTime(began).TotalMilliseconds;Interlocked.Exchange(ref lastSendDuration,elapsed);Interlocked.Increment(ref sentMessages);
                if(lease)
                {
                    long previous=Interlocked.Exchange(ref lastLeaseSent,Stopwatch.GetTimestamp());
                    long gap=previous>0?(long)Stopwatch.GetElapsedTime(previous).TotalMilliseconds:0;
                    if(gap>Interlocked.Read(ref maxLeaseSendGap))Interlocked.Exchange(ref maxLeaseSendGap,gap);
                    if(gap>=750)Log("lease-send-gap","delayed",new{gapMs=gap,sendDurationMs=elapsed,sendQueueDepth=outgoing.Reader.Count});
                }
                if(elapsed>=250)Log("transport-send-slow","delayed",new{sendDurationMs=elapsed,sendQueueDepth=outgoing.Reader.Count});
            }
        }
        catch(Exception ex)when(ex is WebSocketException or OperationCanceledException){if(!stop.IsCancellationRequested)Abort("Connection ended.",ex is OperationCanceledException?"send-timeout":"send-failed",ex);}
    }
    async Task ReadLoop()
    {
        try
        {
            var buffer=new byte[65536];
            while(!stop.IsCancellationRequested)
            {
                using var message=new MemoryStream();WebSocketReceiveResult result;
                do{result=await socket.ReceiveAsync(buffer,stop.Token).ConfigureAwait(false);if(result.MessageType==WebSocketMessageType.Close){Abort("Mac disconnected.","remote-close");return;}message.Write(buffer,0,result.Count);if(message.Length>4*1024*1024)throw new InvalidDataException("Oversized frame.");}while(!result.EndOfMessage);
                Interlocked.Add(ref receivedBytes,message.Length);
                if(result.MessageType==WebSocketMessageType.Binary){if(IsReady){var data=message.ToArray();if(data.Length>0&&data[0]!=4){LastFrame=Stopwatch.GetTimestamp();if(Interlocked.Increment(ref videoPackets)==1)Log("first-video-packet");}else Interlocked.Increment(ref audioPackets);Frame?.Invoke(data);}}
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
                            if(t.GetString()=="pong")
                            {
                                Interlocked.Exchange(ref lastPong,Stopwatch.GetTimestamp());
                                if(value.TryGetProperty("stamp",out var stamp)&&stamp.TryGetInt64(out long sent)&&sent>0&&sent<=Stopwatch.GetTimestamp())
                                    Interlocked.Exchange(ref lastRtt,(long)Stopwatch.GetElapsedTime(sent).TotalMilliseconds);
                            }
                            if(t.GetString()=="error"&&value.TryGetProperty("message",out var error)&&error.ValueKind==JsonValueKind.String)
                            {
                                var text=error.GetString()?.Trim();
                                if(!string.IsNullOrEmpty(text))remoteError=text[..Math.Min(text.Length,1024)];
                            }
                        }
                        if(t.GetString() is "catalog" or "ready" or "video" or "controlStatus" or "status" or "error")
                            Log(t.GetString()=="controlStatus"?"host-control":"host-"+t.GetString(),DiagnosticHostFields.Code(value),DiagnosticHostFields.Select(value));
                    }
                    Message?.Invoke(value);
                }
            }
        }
        catch(Exception ex)when(ex is WebSocketException or OperationCanceledException or JsonException or InvalidDataException){if(!stop.IsCancellationRequested)Abort("Connection stopped: "+ex.Message,ex is WebSocketException?"receive-failed":"receive-invalid",ex);}
    }
    public void Abort(string reason,string code="local-abort",Exception? error=null)
    {lock(lifecycle){if(phase==Stopped)return;Log("session-ended",code,error:error);phase=Stopped;diagnosticTimer?.Dispose();if(remoteError!=null)reason="Mac stopped: "+remoteError;stop.Cancel();outgoing.Writer.TryComplete();socket.Abort();}Ended?.Invoke(reason);}
    public async ValueTask DisposeAsync()
    {Abort("Disconnected; keys released by Mac watchdog.","disposed");try{await Task.WhenAll(reader??Task.CompletedTask,writer??Task.CompletedTask).ConfigureAwait(false);}catch(OperationCanceledException){}socket.Dispose();stop.Dispose();}
}
