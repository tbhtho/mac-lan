using System.Text.Json;
using MacDow.Windows;

internal static partial class Program
{
    static int checks;
    static readonly JsonElement available=Json(new{ok=true,status="available",plan=new{tag="v0.2.0"}});
    static readonly JsonElement blocked=Json(new{ok=false,status="blocked",message="Signature check failed"});
    static JsonElement Json(object value)=>JsonSerializer.SerializeToElement(value);
    static JsonElement Ready(string tag="v0.2.0",string directory="verified-stage")=>Json(new{ok=true,status="ready",stage=new{directory,canApply=true,plan=new{tag}}});
    static JsonElement Staged(bool canApply=true)=>Json(new{ok=true,status="staged",stage=new{directory="new-stage",canApply,plan=new{tag="v0.2.0"}}});
    static void Check(bool actual,string name){if(!actual)throw new Exception(name);Console.WriteLine("PASS "+name);checks++;}
    static async Task<(JsonElement Result,List<string[]> Calls,List<string> Saved)> Run(string? cached,params JsonElement[] results)=>await RunHeld(cached,null,results);
    static async Task<(JsonElement Result,List<string[]> Calls,List<string> Saved)> RunHeld(string? cached,string? held,params JsonElement[] results)
    {
        var responses=new Queue<JsonElement>(results);var calls=new List<string[]>();var saved=new List<string>();
        var result=await UpdateDownload.Run("preview",cached,"installed.exe",arguments=>
        {calls.Add(arguments);if(responses.Count==0)throw new Exception("Unexpected updater call: "+string.Join(' ',arguments));return Task.FromResult(responses.Dequeue());},saved.Add,null,held);
        if(responses.Count!=0)throw new Exception("Expected updater calls were skipped.");return(result,calls,saved);
    }
    static async Task Main()
    {
        var current=await Run("old-stage",Json(new{ok=true,status="current"}));
        Check(current.Calls.Count==1&&!UpdateDownload.Ready(current.Result),"An up-to-date install does not reuse an old cached update");

        var fresh=await Run(null,available,Staged(),Ready(directory:"new-stage"));
        Check(string.Join(',',fresh.Calls.Select(c=>c[0]))=="check,stage,preflight"&&fresh.Saved.SequenceEqual(new[]{"new-stage"})&&UpdateDownload.Ready(fresh.Result),"A new download is preflighted before Restart becomes available");
        Check(fresh.Calls.Last().SequenceEqual(new[]{"preflight","--stage","new-stage","--install","installed.exe"}),"Preflight verifies the actual executable that will be replaced");

        var versioned=Json(new{ok=true,status="available",plan=new{tag="v0.2.0",version="0.2.0"}});
        var held=await RunHeld("old-stage","0.2.0",versioned);
        Check(held.Calls.Count==1&&held.Saved.Count==0&&!UpdateDownload.Ready(held.Result)&&held.Result.GetProperty("status").GetString()=="held","A release the user rolled back from is not downloaded or installed automatically");
        var other=await RunHeld(null,"0.1.9",versioned,Staged(),Ready(directory:"new-stage"));
        Check(UpdateDownload.Ready(other.Result),"A hold on an older version does not block a different release");

        var cached=await Run("old-stage",available,Ready());
        Check(cached.Calls.Count==2&&cached.Saved.Count==0&&UpdateDownload.Ready(cached.Result),"Cached trust is replaced by a fresh signed-feed preflight result");

        var stale=await Run("old-stage",available,blocked,Staged(),Ready(directory:"new-stage"));
        Check(stale.Calls.Select(c=>c[0]).SequenceEqual(new[]{"check","preflight","stage","preflight"})&&UpdateDownload.Ready(stale.Result),"A formerly blocked cached stage is re-downloaded and reverified");

        var wrongVersion=await Run("old-stage",available,Ready("v0.1.0"),Staged(),Ready(directory:"new-stage"));
        Check(wrongVersion.Saved.Count==1&&UpdateDownload.Ready(wrongVersion.Result,"v0.2.0"),"A cached older version cannot hide the newest available update");

        var unsigned=await Run(null,available,Staged(false));
        Check(!UpdateDownload.Ready(unsigned.Result)&&unsigned.Calls.Count==2,"An unsigned download cannot enable Restart");

        var changed=await Run(null,available,Staged(),blocked);
        Check(!UpdateDownload.Ready(changed.Result),"A download that fails fresh verification stays blocked");
        Check(!UpdateDownload.Ready(Ready(),"v0.3.0"),"Apply can require the exact version presented to the user");
        await AutomationChecks();
        Console.WriteLine($"{checks} updater workflow checks passed; no network, process replacement or UI.");
    }
}
