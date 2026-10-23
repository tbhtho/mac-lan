using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace MacDow.Windows;

internal sealed record TransferProgress(long Sent,long Total);
internal sealed record TransferReceipt(string Name,string Sha256,long Bytes);

internal sealed class FileTransferClient
{
    readonly TransferTarget target;
    public FileTransferClient(TransferTarget target)
    {
        static bool Hex(string value)=>value.Length==64&&value.All(c=>c is >= '0' and <= '9' or >= 'a' and <= 'f');
        if(target.Endpoint.Scheme!="https"||target.Endpoint.AbsolutePath!="/transfer"||target.Endpoint.Query.Length!=0||target.Endpoint.Fragment.Length!=0||target.Endpoint.UserInfo.Length!=0||!Hex(target.Fingerprint)||!Hex(target.Token))
            throw new ArgumentException("The paired Mac did not provide a valid transfer connection.");
        this.target=target;
    }
    public async Task<TransferReceipt> SendAsync(FileTransferPackage package,IProgress<TransferProgress>? progress,CancellationToken cancellationToken)
    {
        using var linked=CancellationTokenSource.CreateLinkedTokenSource(cancellationToken,target.SessionEnded);
        using var handler=new HttpClientHandler{UseProxy=false,AllowAutoRedirect=false,AutomaticDecompression=DecompressionMethods.None};
        byte[] expected=Convert.FromHexString(target.Fingerprint);
        handler.ServerCertificateCustomValidationCallback=(_,certificate,_,_)=>certificate!=null&&CryptographicOperations.FixedTimeEquals(SHA256.HashData(certificate.RawData),expected);
        using var client=new HttpClient(handler){Timeout=Timeout.InfiniteTimeSpan};
        using var content=new TransferContent(package,progress);
        using var request=new HttpRequestMessage(HttpMethod.Post,target.Endpoint){Content=content,Version=HttpVersion.Version11,VersionPolicy=HttpVersionPolicy.RequestVersionExact};
        request.Headers.Authorization=new AuthenticationHeaderValue("Bearer",target.Token);
        request.Headers.Add("X-MacDow-Name",Convert.ToBase64String(Encoding.UTF8.GetBytes(package.Name)).TrimEnd('=').Replace('+','-').Replace('/','_'));
        request.Headers.Add("X-MacDow-Kind",package.Kind);
        using var response=await client.SendAsync(request,HttpCompletionOption.ResponseHeadersRead,linked.Token);
        if(!response.IsSuccessStatusCode)
        {
            throw new IOException(response.StatusCode switch
            {
                HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden=>"The Mac connection expired. Reconnect before sending files.",
                HttpStatusCode.Conflict or HttpStatusCode.TooManyRequests=>"Your Mac is already receiving a transfer. Wait for it to finish, then retry.",
                HttpStatusCode.RequestEntityTooLarge=>"This transfer is too large for your Mac.",
                HttpStatusCode.InsufficientStorage=>"Your Mac does not have enough free space for this transfer.",
                _=>"Your Mac could not save the transfer. Check its available space and retry."
            });
        }
        await using var stream=await response.Content.ReadAsStreamAsync(linked.Token);
        using var body=new MemoryStream();byte[] buffer=new byte[4096];
        while(true){int count=await stream.ReadAsync(buffer,linked.Token);if(count==0)break;if(body.Length+count>16*1024)throw new InvalidDataException("Your Mac returned an invalid transfer receipt.");body.Write(buffer,0,count);}
        using var json=JsonDocument.Parse(body.ToArray());var root=json.RootElement;
        if(root.ValueKind!=JsonValueKind.Object||!root.TryGetProperty("ok",out var ok)||ok.ValueKind!=JsonValueKind.True||!root.TryGetProperty("name",out var name)||name.ValueKind!=JsonValueKind.String||!root.TryGetProperty("sha256",out var hash)||hash.ValueKind!=JsonValueKind.String||!root.TryGetProperty("bytes",out var bytes)||bytes.ValueKind!=JsonValueKind.Number||!bytes.TryGetInt64(out long received))
            throw new InvalidDataException("Your Mac returned an invalid transfer receipt.");
        string savedName=name.GetString()!,savedHash=hash.GetString()!;FileTransferPackage.ValidateName(savedName);
        if(received!=package.Length||content.Hash==null||savedHash.Length!=64||!savedHash.All(Uri.IsHexDigit)||!CryptographicOperations.FixedTimeEquals(content.Hash,Convert.FromHexString(savedHash)))
            throw new InvalidDataException("The transfer could not be verified. Retry before removing the original file.");
        return new(savedName,savedHash.ToLowerInvariant(),received);
    }
    sealed class TransferContent : HttpContent
    {
        readonly FileTransferPackage package;
        readonly IProgress<TransferProgress>? progress;
        public byte[]? Hash {get;private set;}
        public TransferContent(FileTransferPackage package,IProgress<TransferProgress>? progress)
        {this.package=package;this.progress=progress;Headers.ContentType=new MediaTypeHeaderValue("application/octet-stream");Headers.ContentLength=package.Length;}
        protected override bool TryComputeLength(out long length){length=package.Length;return true;}
        protected override Task SerializeToStreamAsync(Stream stream,TransportContext? context)=>SerializeToStreamAsync(stream,context,CancellationToken.None);
        protected override async Task SerializeToStreamAsync(Stream stream,TransportContext? context,CancellationToken cancellationToken)
        {
            await using var source=package.OpenRead();using var hash=IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
            byte[] buffer=new byte[256*1024];long sent=0;long reported=Environment.TickCount64;
            progress?.Report(new(0,package.Length));
            while(true)
            {
                int count=await source.ReadAsync(buffer,cancellationToken);if(count==0)break;
                if(sent+count>package.Length)throw new IOException("The file changed while it was being sent. Add it again and retry.");
                await stream.WriteAsync(buffer.AsMemory(0,count),cancellationToken);hash.AppendData(buffer,0,count);sent+=count;
                long now=Environment.TickCount64;if(now-reported>=100||sent==package.Length){progress?.Report(new(sent,package.Length));reported=now;}
            }
            if(sent!=package.Length)throw new IOException("The file changed while it was being sent. Add it again and retry.");
            Hash=hash.GetHashAndReset();progress?.Report(new(sent,package.Length));
        }
    }
}
