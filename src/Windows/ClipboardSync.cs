using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text.Json;
using System.Windows;
using System.Windows.Threading;

namespace MacDow.Windows;

// Shares plain text with the paired Mac in both directions over the pinned transfer
// connection. Windows pushes on every clipboard change; the Mac is read once a second.
// The last synced text is remembered on both paths so a copy never echoes back.
internal sealed class ClipboardSync : IDisposable
{
    const int MaxBytes=256*1024;
    readonly TransferTarget target;
    readonly HttpClient client;
    readonly Uri endpoint;
    readonly DispatcherTimer timer=new(){Interval=TimeSpan.FromSeconds(1)};
    string? last;
    bool baseline,polling,disposed;
    public ClipboardSync(TransferTarget target)
    {
        this.target=target;endpoint=new Uri(target.Endpoint,"/clipboard");
        var handler=new HttpClientHandler{UseProxy=false,AllowAutoRedirect=false,AutomaticDecompression=DecompressionMethods.None};
        byte[] expected=Convert.FromHexString(target.Fingerprint);
        handler.ServerCertificateCustomValidationCallback=(_,certificate,_,_)=>certificate!=null&&CryptographicOperations.FixedTimeEquals(SHA256.HashData(certificate.RawData),expected);
        client=new HttpClient(handler){Timeout=TimeSpan.FromSeconds(5)};
        timer.Tick+=async(_,_)=>await Poll();
        timer.Start();
    }
    HttpRequestMessage Request(HttpMethod method)
    {
        var request=new HttpRequestMessage(method,endpoint){Version=HttpVersion.Version11,VersionPolicy=HttpVersionPolicy.RequestVersionExact};
        request.Headers.Authorization=new AuthenticationHeaderValue("Bearer",target.Token);
        return request;
    }
    async Task Poll()
    {
        if(polling||disposed||target.SessionEnded.IsCancellationRequested)return;
        polling=true;
        try
        {
            using var response=await client.SendAsync(Request(HttpMethod.Get),target.SessionEnded);
            if(!response.IsSuccessStatusCode)return;
            using var doc=JsonDocument.Parse(await response.Content.ReadAsStringAsync(target.SessionEnded));
            string text=doc.RootElement.TryGetProperty("text",out var value)?value.GetString()??"":"";
            // Whatever is on the Mac at connection time stays there; only later copies are shared.
            if(!baseline){baseline=true;last??=text;return;}
            if(text.Length==0||text==last)return;
            last=text;
            try{Clipboard.SetText(text);}catch(System.Runtime.InteropServices.COMException){last=null;}
        }
        catch(Exception e)when(e is HttpRequestException or TaskCanceledException or JsonException or OperationCanceledException){}
        finally{polling=false;}
    }
    // Called on WM_CLIPBOARDUPDATE.
    public async void WindowsChanged()
    {
        if(disposed||target.SessionEnded.IsCancellationRequested)return;
        string text;
        try{if(!Clipboard.ContainsText())return;text=Clipboard.GetText();}
        catch(System.Runtime.InteropServices.COMException){return;}
        if(text.Length==0||text==last||System.Text.Encoding.UTF8.GetByteCount(text)>MaxBytes)return;
        last=text;baseline=true;
        try
        {
            using var request=Request(HttpMethod.Put);request.Content=JsonContent.Create(new{text});
            using var response=await client.SendAsync(request,target.SessionEnded);
        }
        catch(Exception e)when(e is HttpRequestException or TaskCanceledException or OperationCanceledException){}
    }
    public void Dispose(){if(disposed)return;disposed=true;timer.Stop();client.Dispose();}
}
