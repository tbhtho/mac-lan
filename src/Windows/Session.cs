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
    Task? reader,writer;
    public event Action<byte[]>? Frame;
    public event Action<JsonElement>? Message;
    public event Action<string>? Ended;
    public bool IsReady { get;private set; }
    public long NativeAlive {get;private set;}
    public long LastFrame {get;private set;}
    public async Task Connect(string host,string port,string fingerprint,string token)
    {
        if(!Pairing.Validate(host,port,fingerprint,token))throw new ArgumentException("Enter private IPv4, port, 64-digit fingerprint and temporary token from the Mac.");
        socket.Options.RemoteCertificateValidationCallback=(_,cert,_,_)=>cert!=null&&Pin(cert,fingerprint);

        socket.Options.Proxy=null;
        using var timeout=CancellationTokenSource.CreateLinkedTokenSource(stop.Token);timeout.CancelAfter(TimeSpan.FromSeconds(8));
        await socket.ConnectAsync(new Uri($"wss://{host}:{port}/session"),timeout.Token);
        writer=WriteLoop();reader=ReadLoop();Send(new {type="auth",token});
    }
    static bool Pin(X509Certificate cert,string expected)
    {byte[] actual=SHA256.HashData(cert.GetRawCertData());return CryptographicOperations.FixedTimeEquals(actual,Convert.FromHexString(expected));}
    public void Send(object value)
    {
        if(!stop.IsCancellationRequested&&!outgoing.Writer.TryWrite(JsonSerializer.Serialize(value)))Abort("Input queue overflow; disconnected safely.");
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
                if(result.MessageType==WebSocketMessageType.Binary){LastFrame=Stopwatch.GetTimestamp();Frame?.Invoke(message.ToArray());}
                else {using var doc=JsonDocument.Parse(message.ToArray());var value=doc.RootElement.Clone();if(value.TryGetProperty("type",out var t)){if(t.GetString()=="ready")IsReady=true;if(t.GetString()=="captureAlive")NativeAlive=Stopwatch.GetTimestamp();}Message?.Invoke(value);}
            }
        }
        catch(Exception ex)when(ex is WebSocketException or OperationCanceledException or JsonException or InvalidDataException){Abort("Connection stopped: "+ex.Message);}
    }
    public void Abort(string reason)
    {if(stop.IsCancellationRequested)return;IsReady=false;stop.Cancel();outgoing.Writer.TryComplete();socket.Abort();Ended?.Invoke(reason);}
    public async ValueTask DisposeAsync()
    {Abort("Disconnected; keys released by Mac watchdog.");try{await Task.WhenAll(reader??Task.CompletedTask,writer??Task.CompletedTask);}catch(OperationCanceledException){}socket.Dispose();stop.Dispose();}
}
