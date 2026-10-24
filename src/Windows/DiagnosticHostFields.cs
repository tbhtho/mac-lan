using System.Text.Json;
namespace MacDow.Windows;

// Select fields, never serialize the remote message: catalogs include window
// titles and transfer secrets; cursor messages contain an image.
internal static class DiagnosticHostFields
{
    internal static string Failure(string? text)
    {
        if(string.IsNullOrEmpty(text))return "none";
        while(text.StartsWith("Capture stopped: ",StringComparison.Ordinal))text=text[17..];
        foreach(var (prefix,code) in new[]{("Viewer heartbeat expired","viewer-heartbeat"),("Native input mailbox overflow","input-mailbox"),("Native input message invalid","input-invalid"),("Viewer transport pipe closed","viewer-pipe"),("Selected Mac window resized","window-resized"),("Selected window closed","window-closed"),("Main Mac display unavailable","display-unavailable"),("Hardware H.264 unavailable","h264-unavailable"),("Hardware encoder preparation failed","h264-prepare"),("Capture setup failed","capture-setup"),("Grant Screen Recording","screen-permission"),("Screen Recording is unavailable","screen-permission"),("Mac user did not consent","not-approved")})
            if(text.StartsWith(prefix,StringComparison.Ordinal))return code;
        return "host-message";
    }
    internal static string Code(JsonElement m)
    {
        if(m.TryGetProperty("type",out var type)&&type.GetString()=="controlStatus")
        {
            string? code=m.TryGetProperty("code",out var value)&&value.ValueKind==JsonValueKind.String?value.GetString():null;
            return code is "ready" or "view-only" or "accessibility-missing" or "scope-blocked" or "lease-expired" or "stale-input" or "generation-revoked" or "input-overflow"?code:"unknown-control-state";
        }
        return m.TryGetProperty("message",out var text)&&text.ValueKind==JsonValueKind.String?Failure(text.GetString()):"ok";
    }
    internal static object Stream(JsonElement m)=>Select(m);
    internal static Dictionary<string,double> Viewer(JsonElement m)
    {
        var fields=new Dictionary<string,double>();
        foreach(string key in new[]{"fps","encodeMs","decodeMs","queueFrames","bufferedBytes","decoderQueue","recoveries"})
            if(m.TryGetProperty(key,out var value)&&value.ValueKind==JsonValueKind.Number&&value.TryGetDouble(out double number)&&double.IsFinite(number)&&number>=0)fields[key]=number;
        return fields;
    }
    internal static Dictionary<string,object?> Select(JsonElement m)
    {
        var fields=new Dictionary<string,object?>();
        foreach(string key in new[]{"width","height","fps","maxFps","bitrate","quality"})
            if(m.TryGetProperty(key,out var value)&&value.ValueKind==JsonValueKind.Number&&value.TryGetInt64(out long number))fields[key]=number;
        foreach(string key in new[]{"active","view","controlPermission","paused","keyframes","audio"})
            if(m.TryGetProperty(key,out var value)&&value.ValueKind is JsonValueKind.True or JsonValueKind.False)fields[key]=value.GetBoolean();
        if(m.TryGetProperty("codec",out var codec)&&codec.ValueKind==JsonValueKind.String)
        {
            string? text=codec.GetString();
            fields["codec"]=text is "h264" or "jpeg"||text is {Length:11}&&text.StartsWith("avc1.",StringComparison.Ordinal)&&text[5..].All(Uri.IsHexDigit)?text:"other";
        }
        if(m.TryGetProperty("presentation",out var presentation)&&presentation.ValueKind==JsonValueKind.String)fields["presentation"]=presentation.GetString() is "fit" or "fill"?presentation.GetString():"other";
        if(m.TryGetProperty("window",out var window)&&window.ValueKind==JsonValueKind.Number)fields["singleWindow"]=window.TryGetUInt32(out uint id)&&id!=0;
        if(m.TryGetProperty("videoError",out var error)&&error.ValueKind==JsonValueKind.String)fields["videoError"]=Failure(error.GetString());
        return fields;
    }
}
