using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace MacDow.Windows;

internal sealed record FileOffer(string Id,string Name,long Bytes,string Sha256);
// Reverse transfers use the existing Mac listener and pairing, never a Windows listener.
internal sealed class FileReceiveClient : IDisposable
{
    readonly TransferTarget target;
    readonly HttpClient client;
    public FileReceiveClient(TransferTarget target)
    {
        _=new FileTransferClient(target); // Share capability and endpoint validation.
        this.target=target;
        var handler=new HttpClientHandler{UseProxy=false,AllowAutoRedirect=false,AutomaticDecompression=DecompressionMethods.None};
        byte[] expected=Convert.FromHexString(target.Fingerprint);
        handler.ServerCertificateCustomValidationCallback=(_,certificate,_,_)=>certificate!=null&&CryptographicOperations.FixedTimeEquals(SHA256.HashData(certificate.RawData),expected);
        client=new HttpClient(handler){Timeout=Timeout.InfiniteTimeSpan};
    }
    HttpRequestMessage Request(HttpMethod method,string suffix)
    {
        var request=new HttpRequestMessage(method,new Uri(target.Endpoint,"/transfer/outgoing"+suffix));
        request.Headers.Authorization=new AuthenticationHeaderValue("Bearer",target.Token);return request;
    }
    internal static void ValidateOffer(FileOffer offer)
    {
        static bool Hex(string value,int length)=>value!=null&&value.Length==length&&value.All(c=>c is >= '0' and <= '9' or >= 'a' and <= 'f');
        // Windows device names and alternate streams must never become destinations.
        string name=offer.Name??"",stem=name.Split('.')[0].ToUpperInvariant();
        if(!Hex(offer.Id,32)||!Hex(offer.Sha256,64)||offer.Bytes<0||offer.Bytes>FileTransferPackage.MaxBytes||
           string.IsNullOrWhiteSpace(name)||Encoding.UTF8.GetByteCount(name)>240||name.EndsWith('.')||name.EndsWith(' ')||
           name.Any(c=>char.IsControl(c)||"<>:\"/\\|?*".Contains(c))||stem is "CON" or "PRN" or "AUX" or "NUL"||
           (stem.Length==4&&(stem.StartsWith("COM")||stem.StartsWith("LPT"))&&"123456789¹²³".Contains(stem[3])))
            throw new InvalidDataException("The Mac file has an unsupported name or size. Rename it on the Mac and retry.");
    }
    public async Task<FileOffer> GetOfferAsync(CancellationToken token)
    {
        using var linked=CancellationTokenSource.CreateLinkedTokenSource(token,target.SessionEnded);
        using var request=Request(HttpMethod.Get,"");
        using var response=await client.SendAsync(request,HttpCompletionOption.ResponseHeadersRead,linked.Token);
        if(response.StatusCode==HttpStatusCode.NotFound)throw new InvalidDataException("On the Mac, choose mac-lan → Send File to Windows first. If that option is missing, update the Mac app.");
        response.EnsureSuccessStatusCode();
        await using var input=await response.Content.ReadAsStreamAsync(linked.Token);using var data=new MemoryStream();byte[] buffer=new byte[4096];
        while(true){int n=await input.ReadAsync(buffer,linked.Token);if(n==0)break;if(data.Length+n>4096)throw new InvalidDataException("Invalid file offer.");data.Write(buffer,0,n);}
        var offer=JsonSerializer.Deserialize<FileOffer>(data.ToArray(),new JsonSerializerOptions{PropertyNameCaseInsensitive=true})??throw new InvalidDataException("Invalid file offer.");
        ValidateOffer(offer);return offer;
    }
    // The caller supplies a locally chosen path; the remote name is never a path.
    public async Task<bool> ReceiveAsync(FileOffer offer,string destination,IProgress<TransferProgress>? progress,CancellationToken token)
    {
        ValidateOffer(offer);
        using var linked=CancellationTokenSource.CreateLinkedTokenSource(token,target.SessionEnded);
        string full=Path.GetFullPath(destination),directory=Path.GetDirectoryName(full)!;
        ValidateDirectory(directory);
        ValidateOffer(offer with{Name=Path.GetFileName(full)});
        if(File.Exists(full)||Directory.Exists(full))throw new InvalidDataException("That name already exists. Choose a new filename; existing files are never replaced.");
        string temporary=Path.Combine(directory,".mac-lan-"+Guid.NewGuid().ToString("N")+".partial");
        try
        {
            using var request=Request(HttpMethod.Get,"/"+offer.Id);
            using var response=await client.SendAsync(request,HttpCompletionOption.ResponseHeadersRead,linked.Token);response.EnsureSuccessStatusCode();
            if(response.Content.Headers.ContentLength!=offer.Bytes)throw new InvalidDataException("The file offer changed. Choose Receive from Mac again.");
            await using(var input=await response.Content.ReadAsStreamAsync(linked.Token))
            await using(var output=new FileStream(temporary,FileMode.CreateNew,FileAccess.Write,FileShare.None,256*1024,FileOptions.Asynchronous|FileOptions.SequentialScan))
                await CopyVerifiedAsync(input,output,offer,progress,linked.Token);
            linked.Token.ThrowIfCancellationRequested();ValidateDirectory(directory);
            File.Move(temporary,full,false);
            // A lost acknowledgment must not turn a verified saved file into a reported failure.
            try
            {
                using var timeout=CancellationTokenSource.CreateLinkedTokenSource(linked.Token);timeout.CancelAfter(TimeSpan.FromSeconds(10));
                using var ack=Request(HttpMethod.Post,"/"+offer.Id);ack.Headers.Add("X-MacDow-SHA256",offer.Sha256);
                using var responseAck=await client.SendAsync(ack,timeout.Token);return responseAck.IsSuccessStatusCode;
            }
            catch(Exception ex)when(ex is HttpRequestException or OperationCanceledException){return false;}
        }
        finally{try{File.Delete(temporary);}catch(IOException){}catch(UnauthorizedAccessException){}}
    }
    internal static void ValidateDirectory(string directory)
    {
        for(string? current=directory;current!=null;current=Path.GetDirectoryName(current))
            if((File.GetAttributes(current)&FileAttributes.ReparsePoint)!=0)throw new InvalidDataException("Choose a local destination without linked folders.");
    }
    internal static async Task CopyVerifiedAsync(Stream input,Stream output,FileOffer offer,IProgress<TransferProgress>? progress,CancellationToken token)
    {
        byte[] buffer=new byte[256*1024];using var hash=IncrementalHash.CreateHash(HashAlgorithmName.SHA256);long received=0;
        while(true)
        {
            int n=await input.ReadAsync(buffer,token);if(n==0)break;
            if(n>offer.Bytes-received)throw new InvalidDataException("The transfer exceeded its declared size.");
            await output.WriteAsync(buffer.AsMemory(0,n),token);hash.AppendData(buffer,0,n);received+=n;progress?.Report(new(received,offer.Bytes));
        }
        if(received!=offer.Bytes||!CryptographicOperations.FixedTimeEquals(hash.GetHashAndReset(),Convert.FromHexString(offer.Sha256)))throw new InvalidDataException("The transfer was incomplete or changed. Your original is still on the Mac; retry it.");
        await output.FlushAsync(token);progress?.Report(new(received,offer.Bytes));
    }
    public void Dispose()=>client.Dispose();
}
