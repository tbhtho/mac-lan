using System.Text.Json;

namespace MacDow.Windows;

// Keeps automatic installation on the same verified worker path as manual updates.
// An automatic install waits while a session is connected, controlling or
// transferring files, and until that has been idle for IdleGrace. Manual
// installation (automatic:false) is never deferred.
internal sealed class AutomaticUpdate(
    Func<string[],Task<JsonElement>> call, Func<Task> prepareRestart,
    Action<string> rememberApply, Action<string[]> startWorker, Action shutdown,
    Func<bool>? sessionActive=null, Func<long>? clock=null)
{
    internal static readonly TimeSpan IdleGrace=TimeSpan.FromSeconds(60);
    readonly Func<long> now=clock??(()=>Environment.TickCount64);
    bool busy,scheduled,everActive;
    long lastActive;
    internal bool Scheduled=>scheduled;
    // True when the last automatic attempt found a ready update but waited for the session to go idle.
    internal bool Deferred{get;private set;}
    internal static TimeSpan CheckInterval(TimeSpan uptime)=>TimeSpan.FromSeconds(uptime<TimeSpan.FromSeconds(30)?5:30);
    // Records session use; call regularly so a short session between checks still restarts the grace period.
    internal void Observe(){if(sessionActive?.Invoke()==true){everActive=true;lastActive=now();}}
    internal bool Idle()
    {
        Observe();
        if(sessionActive?.Invoke()==true)return false;
        return !everActive||now()-lastActive>=(long)IdleGrace.TotalMilliseconds;
    }
    internal async Task<bool> Apply(JsonElement candidate,string install,int processId,Func<bool> enabled,bool relaunch=true,bool automatic=true)
    {
        if(busy||scheduled||!enabled()||!UpdateDownload.Ready(candidate))return false;
        if(automatic&&!Idle()){Deferred=true;return false;}
        Deferred=false;
        var stage=candidate.GetProperty("stage");
        string? path=stage.GetProperty("directory").GetString(),tag=stage.GetProperty("plan").GetProperty("tag").GetString();
        if(string.IsNullOrWhiteSpace(path)||string.IsNullOrWhiteSpace(tag))return false;
        busy=true;
        try
        {
            var fresh=await call(new[]{"preflight","--stage",path,"--install",install});
            if(!UpdateDownload.Ready(fresh,tag)||!enabled())return false;
            // A session may have started during the fresh preflight.
            if(automatic&&!Idle()){Deferred=true;return false;}
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
