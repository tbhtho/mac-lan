using System.Net;
using System.Net.Http;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using MacDow.Windows;

internal static class ReceiveChecks
{
    public static async Task Run()
    {
        int checks=0;
        void Check(bool ok,string why){if(!ok)throw new Exception(why);checks++;}
        async Task Reject(Func<Task> action){try{await action();}catch(Exception ex)when(ex is InvalidDataException or IOException or OperationCanceledException or HttpRequestException){checks++;return;}throw new Exception("Expected rejection");}
        FileOffer Offer(byte[] bytes)=>new(new string('a',32),"café 文.txt",bytes.Length,Convert.ToHexString(SHA256.HashData(bytes)).ToLowerInvariant());
        foreach(int length in new[]{0,700019,16*1024*1024})
        {
            byte[] bytes=new byte[length];Random.Shared.NextBytes(bytes);var offer=Offer(bytes);
            using var input=new MemoryStream(bytes);using var output=new MemoryStream();
            await FileReceiveClient.CopyVerifiedAsync(input,output,offer,null,CancellationToken.None);
            Check(bytes.AsSpan().SequenceEqual(output.ToArray()),"exact verified bytes");
        }
        foreach(string name in new[]{"../bad","a/b","a\\b","x:stream","NUL","CON.txt","LPT1.log","COM¹","trailing.","space ","a?b","\u0001bad"})
            await Reject(()=>{FileReceiveClient.ValidateOffer(Offer([]) with{Name=name});return Task.CompletedTask;});
        FileReceiveClient.ValidateOffer(Offer([]));checks++;
        await Reject(()=>FileReceiveClient.CopyVerifiedAsync(new MemoryStream([1]),Stream.Null,Offer([1,2]),null,CancellationToken.None));
        await Reject(()=>FileReceiveClient.CopyVerifiedAsync(new MemoryStream([1,2]),Stream.Null,Offer([1]),null,CancellationToken.None));
        await Reject(()=>FileReceiveClient.CopyVerifiedAsync(new MemoryStream([2]),Stream.Null,Offer([1]),null,CancellationToken.None));
        using(var cancel=new CancellationTokenSource()) {cancel.Cancel();await Reject(()=>FileReceiveClient.CopyVerifiedAsync(new MemoryStream([1]),Stream.Null,Offer([1]),null,cancel.Token));}
        string root=Path.Combine(Path.GetTempPath(),"receive-checks-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(root);
        try
        {
            foreach(bool lostAck in new[]{false,true})
            {
                byte[] data=Encoding.UTF8.GetBytes("hello from the Mac 文");var offer=Offer(data);
                await using var peer=new ReceivePeer(offer,data,lostAck);
                using var client=new FileReceiveClient(peer.Target);
                Check(await client.GetOfferAsync(default)==offer,"metadata via pinned TLS");
                string path=Path.Combine(root,Guid.NewGuid()+".txt");
                Check(await client.ReceiveAsync(offer,path,null,default)==!lostAck,"ack outcome");
                Check(File.ReadAllBytes(path).AsSpan().SequenceEqual(data),"network download saved verified data");
                await Reject(()=>client.ReceiveAsync(offer,path,null,default));
                Check(!Directory.EnumerateFiles(root,"*.partial").Any(),"no temporary files remain");
            }
            foreach(bool disconnect in new[]{false,true})
            {
                byte[] bytes=new byte[700019];var offer=Offer(bytes);
                using var cancel=new CancellationTokenSource();
                await using var peer=new ReceivePeer(offer,bytes,false);
                using var client=new FileReceiveClient(peer.Target with{SessionEnded=disconnect?cancel.Token:default});
                string path=Path.Combine(root,"cancel-"+disconnect+".bin");
                await Reject(()=>client.ReceiveAsync(offer,path,new CancelProgress(cancel),disconnect?default:cancel.Token));
                Check(!File.Exists(path),"cancel/disconnect never publishes partial file");
            }
            var good=Offer([1,2,3]);
            await using(var peer=new ReceivePeer(good,[3,2,1],false))
            using(var client=new FileReceiveClient(peer.Target))
            {
                string path=Path.Combine(root,"corrupt.txt");await Reject(()=>client.ReceiveAsync(good,path,null,default));Check(!File.Exists(path),"corrupt file never published");
            }
            using(var ended=new CancellationTokenSource())
            await using(var peer=new ReceivePeer(good,[1,2,3],false))
            using(var client=new FileReceiveClient(peer.Target with{SessionEnded=ended.Token}))
            {ended.Cancel();await Reject(()=>client.GetOfferAsync(default));}
            Check(!Directory.EnumerateFiles(root,"*.partial").Any(),"failed transfers cleaned staging");
        }
        finally{Directory.Delete(root,true);}
        Console.WriteLine($"{checks} reverse transfer checks passed (real client, loopback TLS, synthetic files).");
    }
    sealed class CancelProgress(CancellationTokenSource cancel) : IProgress<TransferProgress>
    { public void Report(TransferProgress value)=>cancel.Cancel(); }
    sealed class ReceivePeer : IAsyncDisposable
    {
        readonly TcpListener listener=new(IPAddress.Loopback,0);
        readonly CancellationTokenSource stop=new(TimeSpan.FromSeconds(20));
        readonly X509Certificate2 certificate;
        readonly Task server;
        readonly FileOffer offer;readonly byte[] body;readonly bool lostAck;
        public TransferTarget Target {get;}
        public ReceivePeer(FileOffer offer,byte[] body,bool lostAck)
        {
            this.offer=offer;this.body=body;this.lostAck=lostAck;
            using var key=RSA.Create(2048);var request=new CertificateRequest("CN=localhost",key,HashAlgorithmName.SHA256,RSASignaturePadding.Pkcs1);
            certificate=request.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-1),DateTimeOffset.UtcNow.AddHours(1));
            listener.Start();Target=new(new Uri($"https://127.0.0.1:{((IPEndPoint)listener.LocalEndpoint).Port}/transfer"),Convert.ToHexString(SHA256.HashData(certificate.RawData)).ToLowerInvariant(),new string('b',64),default);server=Serve();
        }
        async Task Serve()
        {
            try {while(!stop.IsCancellationRequested)
            {
                using var tcp=await listener.AcceptTcpClientAsync(stop.Token);using var tls=new SslStream(tcp.GetStream());
                await tls.AuthenticateAsServerAsync(new SslServerAuthenticationOptions{ServerCertificate=certificate},stop.Token);
                var headers=new List<byte>();var one=new byte[1];
                while(headers.Count<8192){if(await tls.ReadAsync(one,stop.Token)!=1)throw new IOException();headers.Add(one[0]);if(headers.Count>=4&&Encoding.ASCII.GetString(headers.ToArray()).EndsWith("\r\n\r\n"))break;}
                string text=Encoding.ASCII.GetString(headers.ToArray());
                if(!text.Contains("Authorization: Bearer "+Target.Token))throw new IOException("Missing pairing token");
                bool ack=text.StartsWith("POST "),meta=text.StartsWith("GET /transfer/outgoing HTTP");
                if(ack&&lostAck)continue;
                if(!meta&&!text.StartsWith((ack?"POST ":"GET ")+"/transfer/outgoing/"+offer.Id+" HTTP"))throw new IOException("Wrong offer ID");
                if(ack&&!text.Contains(offer.Sha256))throw new IOException("Missing receipt digest");
                byte[] payload=ack?[]:meta?JsonSerializer.SerializeToUtf8Bytes(offer):body;
                await tls.WriteAsync(Encoding.ASCII.GetBytes($"HTTP/1.1 {(ack?"204 No Content":"200 OK")}\r\nContent-Length: {payload.Length}\r\nConnection: close\r\n\r\n"),stop.Token);
                if(payload.Length>0)await tls.WriteAsync(payload,stop.Token);
            }}
            catch(Exception ex)when(stop.IsCancellationRequested&&(ex is OperationCanceledException or SocketException or IOException or ObjectDisposedException)){}
        }
        public async ValueTask DisposeAsync(){stop.Cancel();listener.Stop();await server;certificate.Dispose();stop.Dispose();}
    }
}
