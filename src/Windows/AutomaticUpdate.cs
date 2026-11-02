using System.Text.Json;

namespace MacDow.Windows;

// Keeps automatic installation on the same verified worker path as manual updates.
internal sealed class AutomaticUpdate(
    Func<string[],Task<JsonElement>> call, Func<Task> prepareRestart,
    Action<string> rememberApply, Action<string[]> startWorker, Action shutdown)
{
    bool busy,scheduled;
    internal bool Scheduled=>scheduled;
    internal static TimeSpan CheckInterval(TimeSpan uptime)=>TimeSpan.FromSeconds(uptime<TimeSpan.FromSeconds(30)?5:30);
    internal async Task<bool> Apply(JsonElement candidate,string install,int processId,Func<bool> enabled,bool relaunch=true)
    {
        if(busy||scheduled||!enabled()||!UpdateDownload.Ready(candidate))return false;
        var stage=candidate.GetProperty("stage");
        string? path=stage.GetProperty("directory").GetString(),tag=stage.GetProperty("plan").GetProperty("tag").GetString();
        if(string.IsNullOrWhiteSpace(path)||string.IsNullOrWhiteSpace(tag))return false;
        busy=true;
        try
        {
            var fresh=await call(new[]{"preflight","--stage",path,"--install",install});
            if(!UpdateDownload.Ready(fresh,tag)||!enabled())return false;
            await prepareRestart();
            if(!enabled())return false;
            rememberApply(path);
            var arguments=new List<string>{"apply","--stage",path,"--install",install,"--wait-pid",processId.ToString(System.Globalization.CultureInfo.InvariantCulture),"--confirm","APPLY "+tag,"--result-file",System.IO.Path.Combine(path,"worker-result.json")};
            if(relaunch)arguments.Add("--relaunch");
            startWorker(arguments.ToArray());
            scheduled=true;
            shutdown();
            return true;
        }
        finally{busy=false;}
    }
}
