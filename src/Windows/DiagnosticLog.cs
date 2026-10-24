using System.Diagnostics;
using System.IO;
using System.Net.Sockets;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
namespace MacDow.Windows;

internal sealed class DiagnosticLog : IAsyncDisposable
{
    readonly Channel<string> pending=Channel.CreateBounded<string>(new BoundedChannelOptions(512){SingleReader=true,FullMode=BoundedChannelFullMode.Wait});
    readonly string directory,version,run=Guid.NewGuid().ToString("N");
    readonly long started=Stopwatch.GetTimestamp();
    readonly int maxBytes,retainedFiles;
    readonly Task writer;
    long dropped;
    internal DiagnosticLog(string directory,string version,int maxBytes=2*1024*1024,int retainedFiles=4)
    {
        this.directory=directory;this.version=version;this.maxBytes=maxBytes;this.retainedFiles=retainedFiles;
        if(maxBytes<1024||retainedFiles<1)throw new ArgumentOutOfRangeException();
        writer=Task.Run(WriteLoop);
    }
    // Only small, explicitly selected metadata belongs in state. Never pass wire
    // messages, clipboard contents, input events, tokens, pixels or transfer names.
    internal void Record(string name,string code="ok",object? state=null,string? session=null,Exception? error=null)
    {
        string line=JsonSerializer.Serialize(new{time=DateTimeOffset.UtcNow,uptimeMs=Math.Round(Stopwatch.GetElapsedTime(started).TotalMilliseconds,1),product="mac-lan",version,role="windows",run,pid=Environment.ProcessId,session,name,code,state,error=ExceptionDetails(error),dropped=Interlocked.Read(ref dropped)});
        if(Encoding.UTF8.GetByteCount(line)+1>maxBytes||!pending.Writer.TryWrite(line))Interlocked.Increment(ref dropped);
    }
    static object? ExceptionDetails(Exception? error)
    {
        if(error==null)return null;
        var chain=new List<object>();
        for(int i=0;error!=null&&i<4;i++,error=error.InnerException)
            chain.Add(new{type=error.GetType().FullName,hresult=error.HResult,webSocketCode=error is WebSocketException ws?ws.WebSocketErrorCode.ToString():null,socketCode=error is SocketException socket?socket.SocketErrorCode.ToString():null,
                // Method names provide call sites without exception text, paths or arguments.
                methods=new StackTrace(error,false).GetFrames()?.Take(12).Select(frame=>{var method=frame.GetMethod();return method?.DeclaringType?.FullName+"."+method?.Name;}).ToArray()});
        return chain;
    }
    async Task WriteLoop()
    {
        string path=Path.Combine(directory,"windows-local.jsonl");
        await foreach(string line in pending.Reader.ReadAllAsync())
        {
            try
            {
                Directory.CreateDirectory(directory);
                int size=Encoding.UTF8.GetByteCount(line)+1;
                if(File.Exists(path)&&new FileInfo(path).Length+size>maxBytes)
                {
                    for(int i=retainedFiles-1;i>=1;i--)
                    {
                        string source=i==1?path:path+"."+(i-1);
                        if(File.Exists(source))File.Move(source,path+"."+i,true);
                    }
                    if(retainedFiles==1)File.Delete(path);
                }
                // Close after each event: evidence survives a crash without a user flush.
                await File.AppendAllTextAsync(path,line+"\n",new UTF8Encoding(false));
            }
            catch(Exception e)when(e is IOException or UnauthorizedAccessException or System.Security.SecurityException){Interlocked.Increment(ref dropped);}
        }
    }
    public async ValueTask DisposeAsync(){pending.Writer.TryComplete();await writer.ConfigureAwait(false);}
}
