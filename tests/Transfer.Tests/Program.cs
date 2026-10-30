using System.IO.Compression;
using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using MacDow.Windows;

await ReceiveChecks.Run();

int checks=0;
void Check(bool condition,string message){if(!condition)throw new Exception(message);checks++;Console.WriteLine("PASS "+message);}
async Task<bool> Rejects(Func<Task> action){try{await action();return false;}catch(Exception ex)when(ex is not OutOfMemoryException){return true;}}
async Task<bool> RejectsAs<T>(Func<Task> action)where T:Exception{try{await action();return false;}catch(T){return true;}}
async Task<bool> Cancels(Task action){try{await action.WaitAsync(TimeSpan.FromSeconds(3));return false;}catch(OperationCanceledException){return true;}}
bool RejectsNow(Action action){try{action();return false;}catch(Exception ex)when(ex is not OutOfMemoryException){return true;}}

var temp=Path.Combine(Path.GetTempPath(),"macdow-transfer-tests-"+Guid.NewGuid().ToString("N"));
Directory.CreateDirectory(temp);
try
{
    var file=Path.Combine(temp,"clip café.mp4");
    var payload=new byte[700_019];Random.Shared.NextBytes(payload);await File.WriteAllBytesAsync(file,payload);
    using(var package=FileTransferPackage.CreateFile(file))
    {
        Check(package.Name=="clip café.mp4"&&package.Kind=="file"&&package.Length==payload.Length,"file package exposes only basename, kind, and exact stream length");
        await using(var input=package.OpenRead())Check(input.Length==payload.Length&&input.CanRead,"file package opens a readable payload stream");
        await using var peer=new TransferPeer();
        var progress=new ProgressLog();
        var result=await new FileTransferClient(peer.Target()).SendAsync(package,progress,CancellationToken.None);
        var request=await peer.Request.Task.WaitAsync(TimeSpan.FromSeconds(5));
        Check(request.Method=="POST"&&request.Path=="/transfer","file transfer uses a separate HTTPS POST connection");
        Check(request.Headers["Authorization"]=="Bearer "+TransferPeer.Token,"file transfer authenticates using the paired capability");
        Check(request.Headers["X-MacDow-Kind"]=="file"&&DecodeName(request.Headers["X-MacDow-Name"])==package.Name,"wire metadata encodes a Unicode basename and file kind");
        Check(request.Body.AsSpan().SequenceEqual(payload)&&long.Parse(request.Headers["Content-Length"])==payload.Length,"file bytes are streamed without changing the clip");
        Check(result.Bytes==payload.Length&&result.Sha256==Convert.ToHexStringLower(SHA256.HashData(payload)),"client accepts only a matching byte count and SHA-256 receipt");
        Check(progress.Values.Count>0&&progress.Values[^1].Sent==payload.Length&&progress.Values.All(p=>p.Total==payload.Length&&p.Sent>=0&&p.Sent<=p.Total),"progress reports bounded transferred bytes and completes at full length");
        Check(progress.Values.Zip(progress.Values.Skip(1)).All(p=>p.First.Sent<=p.Second.Sent),"file transfer progress never moves backwards");
    }
    Check(File.Exists(file),"disposing a file package preserves the original clip");

    using(var package=FileTransferPackage.CreateFile(file))
    {
        await using var peer=new TransferPeer(receiptHash:new string('0',64));
        Check(await Rejects(()=>new FileTransferClient(peer.Target()).SendAsync(package,null,CancellationToken.None)),"corrupt SHA-256 receipt is rejected");
    }
    using(var package=FileTransferPackage.CreateFile(file))
    {
        await using var peer=new TransferPeer(receiptBytes:payload.Length-1);
        Check(await Rejects(()=>new FileTransferClient(peer.Target()).SendAsync(package,null,CancellationToken.None)),"incorrect receipt byte count is rejected");
    }
    foreach(var malformed in new[]{
        (Json:"[]",Label:"receipt with an array root"),
        (Json:JsonSerializer.Serialize(new{ok=true,name="Received clip",sha256=Convert.ToHexStringLower(SHA256.HashData(payload)),bytes=payload.Length.ToString()}),Label:"receipt with a string byte count")})
    using(var package=FileTransferPackage.CreateFile(file))
    {
        await using var peer=new TransferPeer(receiptJson:malformed.Json);
        Check(await RejectsAs<InvalidDataException>(()=>new FileTransferClient(peer.Target()).SendAsync(package,null,CancellationToken.None)),malformed.Label+" reports a controlled invalid-receipt error");
    }
    using(var package=FileTransferPackage.CreateFile(file))
    {
        await using var peer=new TransferPeer();
        var target=peer.Target() with{Fingerprint=new string('0',64)};
        Check(await Rejects(()=>new FileTransferClient(target).SendAsync(package,null,CancellationToken.None)),"a mismatched Mac certificate pin blocks the transfer");
        Check(!peer.Request.Task.IsCompletedSuccessfully,"certificate mismatch prevents upload request delivery");
    }
    using(var package=FileTransferPackage.CreateFile(file))
    {
        await using var peer=new TransferPeer(holdResponse:true);
        using var cancel=new CancellationTokenSource();
        var send=new FileTransferClient(peer.Target()).SendAsync(package,null,cancel.Token);
        await peer.Request.Task.WaitAsync(TimeSpan.FromSeconds(5));cancel.Cancel();
        Check(await Cancels(send),"Cancel interrupts a transfer awaiting the Mac receipt");
    }
    using(var package=FileTransferPackage.CreateFile(file))
    {
        await using var peer=new TransferPeer(holdResponse:true);
        using var ended=new CancellationTokenSource();
        var send=new FileTransferClient(peer.Target(ended.Token)).SendAsync(package,null,CancellationToken.None);
        await peer.Request.Task.WaitAsync(TimeSpan.FromSeconds(5));ended.Cancel();
        Check(await Cancels(send),"ending the paired session cancels its in-flight transfer");
    }
    var largeClip=Path.Combine(temp,"body-cancel.mp4");await File.WriteAllBytesAsync(largeClip,new byte[8*1024*1024]);
    using(var package=FileTransferPackage.CreateFile(largeClip))
    {
        await using var peer=new TransferPeer(slowBody:true);using var cancel=new CancellationTokenSource();long cancelledAfter=0;
        var progress=new CallbackProgress(p=>{if(p.Sent>0&&p.Sent<p.Total&&Interlocked.CompareExchange(ref cancelledAfter,p.Sent,0)==0)cancel.Cancel();});
        var send=new FileTransferClient(peer.Target()).SendAsync(package,progress,cancel.Token);
        Check(await Cancels(send)&&cancelledAfter>0&&cancelledAfter<package.Length,"Cancel during a multi-buffer body upload interrupts serialization before the full clip is sent");
        Check(!peer.Request.Task.IsCompletedSuccessfully,"cancelled body upload cannot produce a successful full-file receipt");
    }

    var folder=Path.Combine(temp,"Clips");Directory.CreateDirectory(folder);
    Directory.CreateDirectory(Path.Combine(folder,"empty"));Directory.CreateDirectory(Path.Combine(folder,"nested"));
    await File.WriteAllTextAsync(Path.Combine(folder,"nested","notes.txt"),"transfer test");
    using var folderPackage=await FileTransferPackage.CreateFolderAsync(folder,null,CancellationToken.None);
    Check(folderPackage.Kind=="folder"&&folderPackage.Name=="Clips"&&folderPackage.Length>0,"folder package creates a named ZIP transfer without changing source files");
    string? packagedFile=null;
    await using(var input=folderPackage.OpenRead())
    {
        packagedFile=(input as FileStream)?.Name;
        using var zip=new ZipArchive(input,ZipArchiveMode.Read,leaveOpen:true);
        Check(zip.Entries.Any(e=>e.FullName.Replace('\\','/').TrimEnd('/').EndsWith("empty",StringComparison.Ordinal)&&e.FullName.EndsWith('/')),"folder ZIP preserves empty directories");
        var note=zip.Entries.Single(e=>e.FullName.Replace('\\','/').EndsWith("nested/notes.txt",StringComparison.Ordinal));
        using var read=new StreamReader(note.Open());Check(await read.ReadToEndAsync()=="transfer test","folder ZIP preserves nested file bytes");
        Check(zip.Entries.All(e=>!Path.IsPathRooted(e.FullName)&&!e.FullName.Replace('\\','/').Split('/').Contains("..")),"folder ZIP contains only relative paths");
    }
    folderPackage.Dispose();
    Check(packagedFile!=null&&!File.Exists(packagedFile),"disposing a folder package removes its temporary ZIP");
    Check(File.Exists(Path.Combine(folder,"nested","notes.txt"))&&Directory.Exists(Path.Combine(folder,"empty")),"temporary ZIP cleanup preserves the original folder");
    Check(RejectsNow(()=>FileTransferPackage.CreateFile(folder)),"file packaging rejects a directory");
    Check(await Rejects(async()=>{using var ignored=await FileTransferPackage.CreateFolderAsync(file,null,CancellationToken.None);}),"folder packaging rejects a file");
    Check(RejectsNow(()=>FileTransferPackage.CreateFile(Path.Combine(temp,"missing.mp4"))),"missing source files are rejected before upload");
    foreach(var name in new[]{".","..","../clip","nested/clip","nested\\clip","clip\n.mp4",new string('é',121)})
        Check(RejectsNow(()=>FileTransferPackage.ValidateName(name)),"unsafe or oversized transfer basename is rejected");
    var changing=Path.Combine(temp,"changing.bin");await File.WriteAllBytesAsync(changing,[1,2,3]);
    using(var package=FileTransferPackage.CreateFile(changing))
    {
        await File.WriteAllBytesAsync(changing,[1,2,3,4]);
        Check(RejectsNow(()=>{using var ignored=package.OpenRead();}),"files changed after selection are rejected before upload");
    }
    Check(RejectsNow(()=>{using var ignored=folderPackage.OpenRead();}),"disposed folder packages cannot reopen a removed archive");
    var deepFolder=Path.Combine(temp,"Deep");Directory.CreateDirectory(deepFolder);
    var deepest=deepFolder;
    for(int depth=0;depth<31;depth++){deepest=Path.Combine(deepest,"d");Directory.CreateDirectory(deepest);}
    var atLimit=Path.Combine(deepest,"clip.txt");await File.WriteAllTextAsync(atLimit,"32 path segments");
    using(var validDepth=await FileTransferPackage.CreateFolderAsync(deepFolder,null,CancellationToken.None))
    await using(var input=validDepth.OpenRead())
    using(var zip=new ZipArchive(input,ZipArchiveMode.Read))
        Check(zip.Entries.Any(e=>e.Name=="clip.txt"&&e.FullName.Split('/').Length==32),"folder transfer accepts a file with 32 relative path segments");
    var tooDeep=Path.Combine(deepest,"d");Directory.CreateDirectory(tooDeep);File.Move(atLimit,Path.Combine(tooDeep,"clip.txt"));
    Check(await RejectsAs<InvalidDataException>(async()=>{using var ignored=await FileTransferPackage.CreateFolderAsync(deepFolder,null,CancellationToken.None);}),"folder transfer rejects 32 nested directories plus a file with 33 path segments");
    using(var canceled=new CancellationTokenSource())
    {
        canceled.Cancel();Check(await Rejects(async()=>{using var ignored=await FileTransferPackage.CreateFolderAsync(folder,null,canceled.Token);}),"cancelled folder preparation does not create a usable package");
    }
    var link=Path.Combine(folder,"outside-link");
    try
    {
        Directory.CreateSymbolicLink(link,temp);
        Check(await Rejects(async()=>{using var ignored=await FileTransferPackage.CreateFolderAsync(folder,null,CancellationToken.None);}),"folder packaging rejects a linked directory instead of following it");
    }
    catch(Exception ex)when(ex is UnauthorizedAccessException or PlatformNotSupportedException or IOException){Console.WriteLine("SKIP linked-directory creation unavailable on this account: "+ex.GetType().Name);}
    finally{if(Directory.Exists(link))Directory.Delete(link);}
    var ancestorLink=Path.Combine(temp,"ancestor-link");
    try
    {
        Directory.CreateSymbolicLink(ancestorLink,folder);
        Check(RejectsNow(()=>FileTransferPackage.CreateFile(Path.Combine(ancestorLink,"nested","notes.txt"))),"file packaging rejects a symbolic link in an ancestor directory");
        Check(await RejectsAs<InvalidDataException>(async()=>{using var ignored=await FileTransferPackage.CreateFolderAsync(Path.Combine(ancestorLink,"nested"),null,CancellationToken.None);}),"folder packaging rejects a symbolic link in an ancestor directory");
    }
    catch(Exception ex)when(ex is UnauthorizedAccessException or PlatformNotSupportedException or IOException){Console.WriteLine("SKIP ancestor-link creation unavailable on this account: "+ex.GetType().Name);}
    finally{if(Directory.Exists(ancestorLink))Directory.Delete(ancestorLink);}

    await using(var peer=new TransferPeer())
    {
        foreach(var target in new[]{
            peer.Target() with{Endpoint=new Uri("http://127.0.0.1/transfer")},
            peer.Target() with{Endpoint=new Uri("https://127.0.0.1/transfer?token=secret")},
            peer.Target() with{Endpoint=new Uri("https://127.0.0.1/other")},
            peer.Target() with{Fingerprint="invalid"},
            peer.Target() with{Token="invalid"}})
        {
            using var package=FileTransferPackage.CreateFile(file);
            Check(await Rejects(()=>new FileTransferClient(target).SendAsync(package,null,CancellationToken.None)),"invalid transfer capability is rejected before HTTP: "+target.Endpoint.AbsolutePath);
        }
    }
}
finally{Directory.Delete(temp,true);}
Console.WriteLine($"{checks} transfer checks passed using loopback TLS and temporary files; no UI, capture, input hooks, or webhook.");

static string DecodeName(string encoded)
{
    encoded=encoded.Replace('-','+').Replace('_','/');encoded=encoded.PadRight((encoded.Length+3)/4*4,'=');
    return Encoding.UTF8.GetString(Convert.FromBase64String(encoded));
}

sealed class ProgressLog : IProgress<TransferProgress>
{
    public List<TransferProgress> Values {get;}=[];
    public void Report(TransferProgress value)=>Values.Add(value);
}
sealed class CallbackProgress(Action<TransferProgress> callback) : IProgress<TransferProgress>
{
    public void Report(TransferProgress value)=>callback(value);
}
sealed record TransferRequest(string Method,string Path,Dictionary<string,string> Headers,byte[] Body);

sealed class TransferPeer : IAsyncDisposable
{
    public const string Token="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
    readonly TcpListener listener=new(IPAddress.Loopback,0);
    readonly CancellationTokenSource stop=new(TimeSpan.FromSeconds(20));
    readonly X509Certificate2 certificate;
    readonly string? receiptHash;
    readonly long? receiptBytes;
    readonly string? receiptJson;
    readonly bool holdResponse;
    readonly bool slowBody;
    readonly Task server;
    TcpClient? tcp;
    public TaskCompletionSource<TransferRequest> Request {get;}=new(TaskCreationOptions.RunContinuationsAsynchronously);
    public TransferPeer(string? receiptHash=null,long? receiptBytes=null,bool holdResponse=false,string? receiptJson=null,bool slowBody=false)
    {
        this.receiptHash=receiptHash;this.receiptBytes=receiptBytes;this.holdResponse=holdResponse;this.receiptJson=receiptJson;this.slowBody=slowBody;
        using var key=RSA.Create(2048);
        var request=new CertificateRequest("CN=localhost",key,HashAlgorithmName.SHA256,RSASignaturePadding.Pkcs1);
        using var generated=request.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-1),DateTimeOffset.UtcNow.AddHours(1));
        certificate=X509CertificateLoader.LoadPkcs12(generated.Export(X509ContentType.Pkcs12),null,X509KeyStorageFlags.DefaultKeySet);
        listener.Start();server=Serve();
    }
    public TransferTarget Target(CancellationToken ended=default)=>new(new Uri($"https://127.0.0.1:{((IPEndPoint)listener.LocalEndpoint).Port}/transfer"),Convert.ToHexStringLower(SHA256.HashData(certificate.RawData)),Token,ended);
    async Task Serve()
    {
        try
        {
            tcp=await listener.AcceptTcpClientAsync(stop.Token);
            if(slowBody)tcp.ReceiveBufferSize=16*1024;
            using var tls=new SslStream(tcp.GetStream(),false);
            await tls.AuthenticateAsServerAsync(new SslServerAuthenticationOptions{ServerCertificate=certificate,EnabledSslProtocols=SslProtocols.Tls12|SslProtocols.Tls13},stop.Token);
            var head=new List<byte>();var next=new byte[1];
            while(head.Count<32_768)
            {
                if(await tls.ReadAsync(next,stop.Token)!=1)throw new IOException("Request ended before headers.");
                head.Add(next[0]);if(head.Count>=4&&head[^4]==13&&head[^3]==10&&head[^2]==13&&head[^1]==10)break;
            }
            var lines=Encoding.ASCII.GetString(head.ToArray()).Split("\r\n");var first=lines[0].Split(' ');
            var headers=new Dictionary<string,string>(StringComparer.OrdinalIgnoreCase);
            foreach(var line in lines.Skip(1).Where(l=>l.Contains(':'))){var item=line.Split(':',2);headers[item[0]]=item[1].Trim();}
            var length=int.Parse(headers["Content-Length"]);
            if(length<0||length>16*1024*1024)throw new IOException("Unexpected test payload length.");
            if(headers.TryGetValue("Expect",out var expect)&&expect.Contains("100-continue",StringComparison.OrdinalIgnoreCase))await tls.WriteAsync(Encoding.ASCII.GetBytes("HTTP/1.1 100 Continue\r\n\r\n"),stop.Token);
            var bytes=new byte[length];
            if(slowBody)
            {
                int offset=0;
                while(offset<length){int count=await tls.ReadAsync(bytes.AsMemory(offset,Math.Min(8192,length-offset)),stop.Token);if(count==0)throw new IOException("Upload cancelled before its body completed.");offset+=count;await Task.Delay(10,stop.Token);}
            }
            else await tls.ReadExactlyAsync(bytes,stop.Token);
            Request.TrySetResult(new(first[0],first[1],headers,bytes));
            if(holdResponse){await Task.Delay(Timeout.Infinite,stop.Token);return;}
            var body=receiptJson!=null?Encoding.UTF8.GetBytes(receiptJson):JsonSerializer.SerializeToUtf8Bytes(new{ok=true,name="Received clip",sha256=receiptHash??Convert.ToHexStringLower(SHA256.HashData(bytes)),bytes=receiptBytes??length});
            var response=Encoding.ASCII.GetBytes($"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {body.Length}\r\nConnection: close\r\n\r\n");
            await tls.WriteAsync(response,stop.Token);await tls.WriteAsync(body,stop.Token);await tls.FlushAsync(stop.Token);
        }
        catch(Exception ex)when(ex is OperationCanceledException or IOException or AuthenticationException or SocketException or ObjectDisposedException){if(!stop.IsCancellationRequested)Request.TrySetException(ex);}
    }
    public async ValueTask DisposeAsync(){stop.Cancel();tcp?.Dispose();listener.Stop();await server;certificate.Dispose();stop.Dispose();}
}
