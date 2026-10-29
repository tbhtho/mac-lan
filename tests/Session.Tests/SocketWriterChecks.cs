using System.Net;
using System.Net.Sockets;
using System.Net.WebSockets;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
using MacDow.Windows;

// Plain loopback only: exercises the real ClientWebSocket and WriteLoop without
// generating certificates, capturing input, or contacting a configured Mac.
internal static class SocketWriterChecks
{
    static FieldInfo Field(string name)=>typeof(Session).GetField(name,BindingFlags.Instance|BindingFlags.Static|BindingFlags.NonPublic)!;
    static void Check(bool value,string message){if(!value)throw new Exception(message);Console.WriteLine("PASS "+message);}
    static Task StartWriter(Session session)
    {
        Field("phase").SetValue(session,(int)Field("Ready").GetRawConstantValue()!);
        var task=(Task)typeof(Session).GetMethod("WriteLoop",BindingFlags.Instance|BindingFlags.NonPublic)!.Invoke(session,null)!;
        Field("writer").SetValue(session,task);
        return task;
    }
    internal static async Task Run()
    {
        await using var log=new DiagnosticLog(Path.Combine(Path.GetTempPath(),"mac-lan-writer-test-"+Guid.NewGuid().ToString("N")),"synthetic-socket-test");
        // Prepare pressure before the real writer starts, then observe its wire
        // output. Lost keyframe requests are recoverable, but a key-up is not.
        for(int attempt=0;attempt<2;attempt++)
        await using(var peer=new Peer())
        await using(var session=new Session(log))
        {
            await peer.Connect(session);
            Field("phase").SetValue(session,(int)Field("Ready").GetRawConstantValue()!);
            var queue=(Channel<string>)Field("outgoing").GetValue(session)!;
            Check(queue.Reader.Count==0,"new transport starts without another session's queued input");
            if(attempt==1)
            {
                Field("<SupportsKeyframes>k__BackingField").SetValue(session,true);
                for(int i=0;i<3;i++)session.Send(new{type="keyframe"});
            }
            for(int i=0;i<64;i++)session.Send(new{type="move",x=.5,y=.5});
            session.Send(new{type="button",button=0,down=false});
            session.Send(new{type="lease",active=true,view=true,generation=attempt+1});
            var writer=StartWriter(session);
            using var first=JsonDocument.Parse(await peer.Next());
            if(attempt==1)
            {
                using var recovery=JsonDocument.Parse(await peer.Next());
                Check(recovery.RootElement.GetProperty("type").GetString()=="keyframe","actual writer preserves a queued video-recovery request across input pressure");
            }
            using var second=JsonDocument.Parse(await peer.Next());
            Check(first.RootElement.GetProperty("type").GetString()=="release"&&second.RootElement.GetProperty("type").GetString()=="lease","actual socket writer sends recovery release before subsequent lease");
            session.Abort("synthetic close");await writer.WaitAsync(TimeSpan.FromSeconds(3));
            Check(!session.IsReady,"socket writer terminates cleanly at session boundary");
        }
        await using(var peer=new Peer())
        await using(var session=new Session(log))
        {
            await peer.Connect(session);
            Field("phase").SetValue(session,(int)Field("Ready").GetRawConstantValue()!);
            // Oversized fixture payload forces local TCP backpressure. This is
            // not a valid Mac input packet or a production-size benchmark.
            session.Send(new{type="move",x=.5,y=.5,fixturePadding=new string('x',16*1024*1024)});
            var writer=StartWriter(session);
            await peer.WaitForSendByte(); // The real writer has entered SendAsync.
            session.Send(new{type="release"});
            Check((long)Field("sentMessages").GetValue(session)! == 0&&!writer.IsCompleted,"non-reading local peer leaves a real send pending");
            session.Abort("cancel pending synthetic send");
            await writer.WaitAsync(TimeSpan.FromSeconds(3));
            Check(!session.IsReady,"abort cancels pending socket send without draining stale queued input");
        }
    }
    sealed class Peer:IAsyncDisposable
    {
        readonly TcpListener listener=new(IPAddress.Loopback,0);
        readonly CancellationTokenSource timeout=new(TimeSpan.FromSeconds(10));
        // Windows can complete an overlapped send after buffering the entire
        // fixture payload. Disable buffering on this test socket so completion
        // requires the non-reading peer to make progress.
        readonly HttpMessageInvoker transport=new(new SocketsHttpHandler{
            UseProxy=false,
            ConnectCallback=async(context,cancellationToken)=>{
                var connection=new Socket(SocketType.Stream,ProtocolType.Tcp){SendBufferSize=0,NoDelay=true};
                try
                {
                    await connection.ConnectAsync(context.DnsEndPoint,cancellationToken);
                    return new NetworkStream(connection,ownsSocket:true);
                }
                catch{connection.Dispose();throw;}
            }
        });
        TcpClient? connection;NetworkStream? stream;WebSocket? socket;
        internal Peer(){listener.Start();}
        internal async Task Connect(Session session)
        {
            var client=(ClientWebSocket)Field("socket").GetValue(session)!;
            client.Options.Proxy=null;
            var connecting=client.ConnectAsync(new Uri($"ws://127.0.0.1:{((IPEndPoint)listener.LocalEndpoint).Port}/"),transport,timeout.Token);
            connection=await listener.AcceptTcpClientAsync(timeout.Token);connection.ReceiveBufferSize=1024;
            stream=connection.GetStream();var bytes=new List<byte>();var one=new byte[1];
            while(bytes.Count<8192)
            {
                if(await stream.ReadAsync(one,timeout.Token)!=1)throw new IOException("upgrade ended early");
                bytes.Add(one[0]);if(bytes.Count>=4&&bytes[^4]==13&&bytes[^3]==10&&bytes[^2]==13&&bytes[^1]==10)break;
            }
            string key=Encoding.ASCII.GetString(bytes.ToArray()).Split("\r\n").Single(l=>l.StartsWith("Sec-WebSocket-Key:",StringComparison.OrdinalIgnoreCase)).Split(':',2)[1].Trim();
            string accept=Convert.ToBase64String(SHA1.HashData(Encoding.ASCII.GetBytes(key+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")));
            await stream.WriteAsync(Encoding.ASCII.GetBytes($"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\n\r\n"),timeout.Token);
            await connecting;
        }
        internal async Task<string> Next()
        {
            socket??=WebSocket.CreateFromStream(stream!,true,null,Timeout.InfiniteTimeSpan);
            using var data=new MemoryStream();var bytes=new byte[4096];WebSocketReceiveResult part;
            do{part=await socket.ReceiveAsync(bytes,timeout.Token);data.Write(bytes,0,part.Count);}while(!part.EndOfMessage);
            return Encoding.UTF8.GetString(data.ToArray());
        }
        internal async Task WaitForSendByte(){var one=new byte[1];if(await stream!.ReadAsync(one,timeout.Token)!=1)throw new IOException("send did not start");}
        public ValueTask DisposeAsync(){timeout.Cancel();socket?.Abort();socket?.Dispose();connection?.Dispose();listener.Stop();transport.Dispose();timeout.Dispose();return ValueTask.CompletedTask;}
    }
}
