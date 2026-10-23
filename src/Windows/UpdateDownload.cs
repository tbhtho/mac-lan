using System.Text.Json;

namespace MacDow.Windows;

// Cached stage metadata is a hint. Only the helper's fresh, signed-feed preflight
// can make a candidate eligible for the Restart action.
internal static class UpdateDownload
{
    internal static async Task<JsonElement> Run(string channel,string? existingStage,string install,Func<string[],Task<JsonElement>> call,Action<string> rememberStage,Action<string>? progress=null)
    {
        progress?.Invoke("Checking for updates…");
        var check=await call(new[]{"check","--channel",channel});
        if(!IsOK(check)||!check.TryGetProperty("status",out var status)||status.GetString()!="available")return check;
        string? tag=check.GetProperty("plan").GetProperty("tag").GetString();
        if(!string.IsNullOrWhiteSpace(existingStage))
        {
            progress?.Invoke("Verifying your downloaded update…");
            var cached=await call(new[]{"preflight","--stage",existingStage,"--install",install});
            if(Ready(cached,tag))return cached;
        }
        progress?.Invoke("Downloading "+tag+"… You can keep using mac-lan.");
        var download=await call(new[]{"stage","--channel",channel});
        if(!IsOK(download)||!download.TryGetProperty("stage",out var stage))return download;
        string directory=stage.GetProperty("directory").GetString()??throw new InvalidOperationException("Update helper returned no download location.");
        rememberStage(directory);
        if(!stage.GetProperty("canApply").GetBoolean())return download;
        progress?.Invoke("Verifying the update before restart…");
        return await call(new[]{"preflight","--stage",directory,"--install",install});
    }
    internal static bool IsOK(JsonElement result)=>result.TryGetProperty("ok",out var ok)&&ok.ValueKind==JsonValueKind.True;
    internal static bool Ready(JsonElement result,string? expectedTag=null)=>IsOK(result)&&result.TryGetProperty("stage",out var stage)&&stage.TryGetProperty("canApply",out var allowed)&&allowed.ValueKind==JsonValueKind.True&&stage.TryGetProperty("plan",out var plan)&&plan.TryGetProperty("tag",out var tag)&&tag.ValueKind==JsonValueKind.String&&(expectedTag==null||tag.GetString()==expectedTag);
}
