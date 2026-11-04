using MacDow.Windows;
using System.Text.Json;

internal static partial class Program
{
    static async Task AutomationChecks()
    {
        var events=new List<string>();bool enabled=true;
        JsonElement preflight=Ready();string[]? worker=null;
        var automation=new AutomaticUpdate(args=>
        {
            events.Add(string.Join(' ',args));return Task.FromResult(preflight);
        },()=>{events.Add("release-input-and-cancel-transfers");return Task.CompletedTask;},
        directory=>events.Add("remember "+directory),args=>{worker=args;events.Add("worker");},()=>events.Add("shutdown"));
        Check(await automation.Apply(Ready(),"installed.exe",123,()=>enabled),"A ready automatic update installs without a restart dialog");
        Check(events.SequenceEqual(new[]{"preflight --stage verified-stage --install installed.exe","release-input-and-cancel-transfers","remember verified-stage","worker","shutdown"}),"Fresh verification precedes cleanup and worker creation; shutdown follows a successful worker start");
        Check(worker!.SequenceEqual(new[]{"apply","--stage","verified-stage","--install","installed.exe","--wait-pid","123","--confirm","APPLY v0.2.0","--result-file",System.IO.Path.Combine("verified-stage","worker-result.json"),"--relaunch"}),"Automatic installation uses the verified worker with the real parent PID and relaunch enabled");
        events.Clear();Check(!await automation.Apply(Ready(),"installed.exe",123,()=>enabled)&&events.Count==0,"A scheduled worker cannot be started twice while shutdown is pending");

        foreach(var invalid in new[]{blocked,Json(new{ok=true,status="current"}),Staged(false)})
        {
            events.Clear();var rejected=new AutomaticUpdate(_=>throw new Exception("Unexpected preflight"),()=>throw new Exception("Unexpected cleanup"),_=>throw new Exception("Unexpected state write"),_=>throw new Exception("Unexpected worker"),()=>throw new Exception("Unexpected shutdown"));
            Check(!await rejected.Apply(invalid,"installed.exe",123,()=>true),"Unavailable or unsigned automatic candidates cannot trigger installation");
        }
        foreach(bool failVerification in new[]{true,false})
        {
            events.Clear();enabled=true;
            var rejected=new AutomaticUpdate(_=>{if(!failVerification)enabled=false;return Task.FromResult(failVerification?blocked:Ready());},()=>throw new Exception("Rejected update must not stop input"),_=>{},_=>throw new Exception("Rejected update must not start worker"),()=>throw new Exception("Rejected update must not quit"));
            Check(!await rejected.Apply(Ready(),"installed.exe",123,()=>enabled),"Failed fresh verification or disabling automatic updates during verification leaves the app running");
        }
        int attempts=0,quits=0;
        var retry=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>Task.CompletedTask,_=>{},_=>{if(++attempts==1)throw new System.IO.IOException("fixture worker failed");},()=>quits++);
        try{await retry.Apply(Ready(),"installed.exe",123,()=>true);throw new Exception("Expected worker failure");}catch(System.IO.IOException){}
        Check(quits==0&&await retry.Apply(Ready(),"installed.exe",123,()=>true)&&quits==1,"Worker failure keeps the app open and permits a later automatic retry");
        var off=new AutomaticUpdate(_=>throw new Exception("Disabled auto-update checked"),()=>throw new Exception("Disabled auto-update stopped input"),_=>{},_=>{},()=>{});
        Check(!await off.Apply(Ready(),"installed.exe",123,()=>false),"Turning automatic updates off prevents installation");
        var changedTag=new AutomaticUpdate(_=>Task.FromResult(Ready("v0.3.0")),()=>throw new Exception("Different tag stopped input"),_=>{},_=>{},()=>{});
        Check(!await changedTag.Apply(Ready(),"installed.exe",123,()=>true),"A preflight for another version cannot authorize the selected update");
        bool continueAfterCleanup=true;
        var disabledDuringCleanup=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>{continueAfterCleanup=false;return Task.CompletedTask;},_=>throw new Exception("Disabled update wrote install state"),_=>throw new Exception("Disabled update started a worker"),()=>throw new Exception("Disabled update quit"));
        Check(!await disabledDuringCleanup.Apply(Ready(),"installed.exe",123,()=>continueAfterCleanup),"Disabling updates during transfer cleanup prevents replacement and shutdown");
        var pending=new TaskCompletionSource<JsonElement>();int starts=0;
        var concurrent=new AutomaticUpdate(_=>pending.Task,()=>Task.CompletedTask,_=>{},_=>starts++,()=>{});
        var firstApply=concurrent.Apply(Ready(),"installed.exe",123,()=>true);
        Check(!await concurrent.Apply(Ready(),"installed.exe",123,()=>true)&&starts==0,"Overlapping checks cannot schedule two workers during preflight");
        pending.SetResult(Ready());Check(await firstApply&&starts==1,"The original check schedules exactly one worker when verification completes");
        string[]? manualArguments=null;
        var manual=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>Task.CompletedTask,_=>{},args=>manualArguments=args,()=>{});
        Check(await manual.Apply(Ready(),"installed.exe",123,()=>true,relaunch:false)&&!manualArguments!.Contains("--relaunch"),"Manual installation preserves the choice not to reopen the app");
        bool active=true;long clock=1_000_000;int deferredStarts=0;
        var deferring=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>throw new Exception("Deferred update cancelled transfers"),_=>{},_=>deferredStarts++,()=>{},()=>active,()=>clock);
        Check(!await deferring.Apply(Ready(),"installed.exe",123,()=>true)&&deferring.Deferred&&deferredStarts==0,"An automatic update waits while a session is streaming, controlling or transferring");
        active=false;clock+=59_000;
        Check(!await deferring.Apply(Ready(),"installed.exe",123,()=>true)&&deferring.Deferred&&deferredStarts==0,"An automatic update waits for the idle grace period after disconnecting");
        deferring.Observe();active=true;deferring.Observe();active=false;clock+=30_000;
        Check(!await deferring.Apply(Ready(),"installed.exe",123,()=>true)&&deferredStarts==0,"A short session observed between checks restarts the idle grace period");
        clock+=30_000;
        var idleCleanup=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>Task.CompletedTask,_=>{},_=>deferredStarts++,()=>{},()=>active,()=>clock);
        Check(await idleCleanup.Apply(Ready(),"installed.exe",123,()=>true)&&!idleCleanup.Deferred&&deferredStarts==1,"An automatic update installs once the app has had no session");
        var idleAfterSession=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>Task.CompletedTask,_=>{},_=>deferredStarts++,()=>{},()=>active,()=>clock);
        active=true;idleAfterSession.Observe();active=false;clock+=(long)AutomaticUpdate.IdleGrace.TotalMilliseconds;
        Check(await idleAfterSession.Apply(Ready(),"installed.exe",123,()=>true)&&deferredStarts==2,"An automatic update installs after the session has been idle for the grace period");
        active=true;
        var startedDuringPreflight=new AutomaticUpdate(_=>{active=true;return Task.FromResult(Ready());},()=>throw new Exception("Session started during preflight was interrupted"),_=>{},_=>throw new Exception("Worker started"),()=>{},()=>active,()=>clock);
        active=false;
        Check(!await startedDuringPreflight.Apply(Ready(),"installed.exe",123,()=>true)&&startedDuringPreflight.Deferred,"A session that starts during fresh verification defers the automatic install");
        active=true;int manualStarts=0;
        var manualWhileActive=new AutomaticUpdate(_=>Task.FromResult(Ready()),()=>Task.CompletedTask,_=>{},_=>manualStarts++,()=>{},()=>active,()=>clock);
        Check(await manualWhileActive.Apply(Ready(),"installed.exe",123,()=>true,automatic:false)&&manualStarts==1,"Restart to update installs immediately even during a session");
        foreach(var (seconds,delay) in new[]{(0,5),(29,5),(30,30),(3600,30)})
            Check(AutomaticUpdate.CheckInterval(TimeSpan.FromSeconds(seconds))==TimeSpan.FromSeconds(delay),"Polling uses launch warmup then steady cadence at uptime "+seconds);
    }
}
