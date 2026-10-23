using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Net.WebSockets;
using System.Reflection;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
using MacDow.Core;
using MacDow.Windows;

int checks=0;
void Check(bool condition,string message){if(!condition)throw new Exception(message);checks++;Console.WriteLine("PASS "+message);}

await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    var catalog=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    var ready=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Message+=m=>{if(m.GetProperty("type").GetString()=="catalog")catalog.TrySetResult();if(m.GetProperty("type").GetString()=="ready")ready.TrySetResult();};
    session.Send(new{type="release"});
    session.Send(new{type="auth",token="caller cannot supply auth"});
    await peer.Connect(session);
    Check((await peer.Next()).GetProperty("type").GetString()=="pair","pairing is the first wire message");
    Check(!session.IsPaired&&!session.IsStarting&&!session.IsReady,"transport connection alone never reports pairing or readiness");
    session.Send(new{type="release"});
    session.Send(new{type="lease",active=false});
    session.Send(new{type="key",code=0,down=true});
    session.Send(new{type="move",x=.5,y=.5});
    session.Send(new{type="button",button=0,down=true});
    session.Send(new{type="scroll",x=0,y=1});
    session.Send(new{type="start",window=0});
    await peer.Send(new{type="catalog",maxFps=60,windows=Array.Empty<object>()});
    await catalog.Task.WaitAsync(TimeSpan.FromSeconds(3));
    Check(session.IsPaired&&!session.IsStarting&&!session.IsReady,"catalog establishes pairing without implying an active stream");
    session.Send(new{type="release"});
    session.Send(StreamProfile.DefaultHardware.CreateStart(0));
    Check(session.IsPaired&&session.IsStarting&&!session.IsReady,"start stays pending while Mac approval is awaited");
    var first=await peer.Next();
    Check(first.GetProperty("type").GetString()=="start","focus release and input never replace the pending stream start");
    Check(first.GetProperty("codec").GetString()=="h264"&&first.GetProperty("width").GetInt32()==1920&&first.GetProperty("fps").GetInt32()==60&&first.GetProperty("quality").GetInt32()==70,"default stream requests full HD H.264 60fps before Mac approval");
    Parallel.For(0,20,_=>{session.Send(new{type="release"});session.Send(new{type="start",window=0});});
    await peer.Send(new{type="ready"});
    await ready.Task.WaitAsync(TimeSpan.FromSeconds(3));
    Check(session.IsPaired&&!session.IsStarting&&session.IsReady,"ready completes the pending stream state");
    session.Send(new{type="lease",active=false});
    Check((await peer.Next()).GetProperty("type").GetString()=="lease","repeated tab clicks produce one stream start, and no pre-ready release");
    session.Send(new{type="start",window=0});session.Send(new{type="release"});
    Check((await peer.Next()).GetProperty("type").GetString()=="release","ready sessions still send release immediately");
    session.Send(new{type="key",code=0,down=true});
    Check((await peer.Next()).GetProperty("type").GetString()=="key","ready sessions accept routed input");
    session.Abort("test ended");
    Check(!session.IsPaired&&!session.IsStarting&&!session.IsReady,"abort clears all lifecycle state");
}

await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    var catalog=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Message+=m=>{if(m.GetProperty("type").GetString()=="catalog")catalog.TrySetResult();};
    await peer.Connect(session);await peer.Next();
    await peer.Send(new{type="catalog",maxFps=60,windows=Array.Empty<object>()});
    await catalog.Task.WaitAsync(TimeSpan.FromSeconds(3));
    foreach(int requested in new[]{5,10,15,30,59})
    {
        bool blocked=false;
        try{session.Send(StreamProfile.DefaultHardware.CreateStart(0,fps:requested));}
        catch(InvalidOperationException e){blocked=e.Message.Contains("60 fps",StringComparison.Ordinal);}
        Check(blocked&&session.IsPaired&&!session.IsStarting,"sub60 start remains paired without requesting a lower stream: "+requested);
    }
    await Task.Delay(100);
    Check(!peer.HasPendingMessage,"sub60 start never goes onto the wire even when host advertises 60fps");
}

foreach(var advertised in new object?[]{null,30,0,61,"60",60.5})
await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    var catalog=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Message+=m=>{if(m.GetProperty("type").GetString()=="catalog")catalog.TrySetResult();};
    await peer.Connect(session);await peer.Next();
    if(advertised==null)await peer.Send(new{type="catalog",windows=Array.Empty<object>()});
    else await peer.Send(new{type="catalog",maxFps=advertised,windows=Array.Empty<object>()});
    await catalog.Task.WaitAsync(TimeSpan.FromSeconds(3));
    bool blocked=false;
    try{session.Send(StreamProfile.DefaultHardware.CreateStart(0));}
    catch(InvalidOperationException e){blocked=e.Message.Contains("update",StringComparison.OrdinalIgnoreCase)&&e.Message.Contains("Mac",StringComparison.Ordinal);}
    Check(blocked&&session.IsPaired&&!session.IsStarting,"old or invalid host capability refuses 60fps before stream start: "+(advertised??"absent"));
}

await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    var ended=new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Ended+=reason=>ended.TrySetResult(reason);
    await peer.Connect(session);await peer.Next();
    await peer.Send(new{type="ready"});
    var reason=await ended.Task.WaitAsync(TimeSpan.FromSeconds(3));
    Check(reason.Contains("Unexpected stream response",StringComparison.Ordinal)&&!session.IsPaired&&!session.IsStarting&&!session.IsReady,"unsolicited ready fails closed and cannot enable control");
}

await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    var errorReceived=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    var ended=new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Message+=m=>{if(m.GetProperty("type").GetString()=="error")errorReceived.TrySetResult();};
    session.Ended+=reason=>ended.TrySetResult(reason);
    await peer.Connect(session);await peer.Next();
    await peer.Send(new{type="error",message="Mac user did not consent"});
    await errorReceived.Task.WaitAsync(TimeSpan.FromSeconds(3));
    peer.Abort();
    var reason=await ended.Task.WaitAsync(TimeSpan.FromSeconds(3));
    Check(reason=="Mac stopped: Mac user did not consent","unclean WebSocket close preserves the actual Mac error");
}
foreach(var capability in new string?[]{null,"bad token",new string('A',64)})
await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    var catalog=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Message+=m=>{if(m.GetProperty("type").GetString()=="catalog")catalog.TrySetResult();};
    await peer.Connect(session);await peer.Next();
    await peer.Send(new{type="catalog",maxFps=60,transferToken=capability,windows=Array.Empty<object>()});
    await catalog.Task.WaitAsync(TimeSpan.FromSeconds(3));
    session.Send(new{type="lease",active=false,view=false});session.Send(new{type="release"});
    await Task.Delay(100);
    Check(session.IsPaired&&!session.CanTransfer&&session.TransferTarget==null&&!peer.HasPendingMessage,"old or invalid transfer capability cannot enable idle keepalive traffic");
}

await using(var peer=new LocalPeer())
await using(var session=new Session())
{
    const string capability="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
    var catalog=new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
    session.Message+=m=>{if(m.GetProperty("type").GetString()=="catalog")catalog.TrySetResult();};
    await peer.Connect(session);await peer.Next();
    Check(!session.CanTransfer&&session.TransferTarget==null,"transfer capability is unavailable before paired catalog arrives");
    await peer.Send(new{type="catalog",maxFps=0,videoError="Native capture preparation failed",transferToken=capability,windows=Array.Empty<object>()});
    await catalog.Task.WaitAsync(TimeSpan.FromSeconds(3));
    var target=session.TransferTarget;
    Check(session.IsPaired&&!session.IsReady&&session.CanTransfer&&target?.Token==capability&&target.Endpoint.Scheme=="https"&&target.Endpoint.AbsolutePath=="/transfer","paired catalog enables a separate HTTPS transfer endpoint without a video stream");
    Check(session.MaxFPS==0&&session.VideoError=="Native capture preparation failed","video-unavailable catalog preserves capture failure while file transfer stays paired");
    session.Send(new{type="lease",active=true,view=false});session.Send(new{type="lease",active=false,view=true});
    session.Send(new{type="release"});session.Send(new{type="key",code=0,down=true});
    session.Send(new{type="lease",active=false,view=false});
    Check((await peer.Next()).GetProperty("type").GetString()=="lease","only an inactive non-viewing lease is sent while paired for file transfers");
    await Task.Delay(100);Check(!peer.HasPendingMessage,"transfer keepalive never allows input or release before video readiness");
    session.Abort("transfer test ended");
    Check(!session.CanTransfer&&session.TransferTarget==null&&target!.SessionEnded.IsCancellationRequested,"disconnect revokes the transfer target and cancels its session token");
}
Console.WriteLine($"{checks} session checks passed using synthetic loopback TLS WebSockets; no UI, capture, hooks, or webhook.");

sealed class LocalPeer : IAsyncDisposable
{
    readonly TcpListener listener=new(IPAddress.Loopback,0);
    readonly CancellationTokenSource stop=new(TimeSpan.FromSeconds(15));
    readonly Channel<JsonElement> incoming=Channel.CreateUnbounded<JsonElement>();
    readonly X509Certificate2 certificate;
    WebSocket? socket;
    TcpClient? tcp;
    Task? receiver;
    public LocalPeer()
    {
        using var key=RSA.Create(2048);
        var request=new CertificateRequest("CN=localhost",key,HashAlgorithmName.SHA256,RSASignaturePadding.Pkcs1);
        request.CertificateExtensions.Add(new X509BasicConstraintsExtension(false,false,0,false));
        using var generated=request.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-1),DateTimeOffset.UtcNow.AddHours(1));
        certificate=X509CertificateLoader.LoadPkcs12(generated.Export(X509ContentType.Pkcs12),null,X509KeyStorageFlags.DefaultKeySet);
        listener.Start();
    }
    public async Task Connect(Session session)
    {
        var accept=Accept();
        var connect=typeof(Session).GetMethod("ConnectCore",BindingFlags.NonPublic|BindingFlags.Instance)!;
        var port=((IPEndPoint)listener.LocalEndpoint).Port.ToString(System.Globalization.CultureInfo.InvariantCulture);
        var fingerprint=Convert.ToHexString(SHA256.HashData(certificate.RawData));
        try{await (Task)connect.Invoke(session,new object[]{"127.0.0.1",port,fingerprint,new{type="pair",code="123456"}})!;}
        catch{if(accept.IsFaulted)await accept;throw;}
        await accept;
    }
    async Task Accept()
    {
        tcp=await listener.AcceptTcpClientAsync(stop.Token);
        var stream=new SslStream(tcp.GetStream(),false);
        await stream.AuthenticateAsServerAsync(new SslServerAuthenticationOptions{ServerCertificate=certificate,EnabledSslProtocols=SslProtocols.Tls12|SslProtocols.Tls13},stop.Token);
        var header=new List<byte>();var next=new byte[1];
        while(header.Count<8192)
        {
            if(await stream.ReadAsync(next,stop.Token)!=1)throw new IOException("Upgrade ended early.");
            header.Add(next[0]);
            if(header.Count>=4&&header[^4]==13&&header[^3]==10&&header[^2]==13&&header[^1]==10)break;
        }
        var key=Encoding.ASCII.GetString(header.ToArray()).Split("\r\n").Single(l=>l.StartsWith("Sec-WebSocket-Key:",StringComparison.OrdinalIgnoreCase)).Split(':',2)[1].Trim();
        var response=Convert.ToBase64String(SHA1.HashData(Encoding.ASCII.GetBytes(key+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")));
        await stream.WriteAsync(Encoding.ASCII.GetBytes($"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {response}\r\n\r\n"),stop.Token);
        socket=WebSocket.CreateFromStream(stream,true,null,TimeSpan.FromMinutes(1));
        receiver=Read();
    }
    async Task Read()
    {
        try
        {
            var bytes=new byte[8192];
            while(!stop.IsCancellationRequested)
            {
                using var data=new MemoryStream();WebSocketReceiveResult part;
                do{part=await socket!.ReceiveAsync(bytes,stop.Token);if(part.MessageType==WebSocketMessageType.Close)return;data.Write(bytes,0,part.Count);}while(!part.EndOfMessage);
                using var doc=JsonDocument.Parse(data.ToArray());await incoming.Writer.WriteAsync(doc.RootElement.Clone(),stop.Token);
            }
        }
        catch(Exception ex)when(ex is OperationCanceledException or WebSocketException or IOException){}
    }
    public async Task Send(object value)=>await socket!.SendAsync(Encoding.UTF8.GetBytes(JsonSerializer.Serialize(value)),WebSocketMessageType.Text,true,stop.Token);
    public void Abort()=>socket!.Abort();
    public async Task<JsonElement> Next()=>await incoming.Reader.ReadAsync(stop.Token).AsTask().WaitAsync(TimeSpan.FromSeconds(3));
    public bool HasPendingMessage=>incoming.Reader.TryPeek(out _);
    public async ValueTask DisposeAsync(){stop.Cancel();socket?.Abort();if(receiver!=null)await receiver;socket?.Dispose();tcp?.Dispose();listener.Stop();certificate.Dispose();stop.Dispose();}
}
