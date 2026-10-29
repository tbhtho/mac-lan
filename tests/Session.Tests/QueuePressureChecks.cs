using System.Reflection;
using System.Text.Json;
using System.Threading.Channels;
using MacDow.Windows;

// Exercises actual Session.Send and its actual bounded channel. The transport
// writer is deliberately not started; the test owns a deterministic consumer.
internal static class QueuePressureChecks
{
    static FieldInfo Field(string name)=>typeof(Session).GetField(name,BindingFlags.Instance|BindingFlags.Static|BindingFlags.NonPublic)!;
    static Channel<string> Ready(Session session)
    {
        Field("phase").SetValue(session,(int)Field("Ready").GetRawConstantValue()!);
        return (Channel<string>)Field("outgoing").GetValue(session)!;
    }
    static List<JsonElement> Drain(Channel<string> queue)
    {
        var result=new List<JsonElement>();
        while(queue.Reader.TryRead(out var line)){using var parsed=JsonDocument.Parse(line);result.Add(parsed.RootElement.Clone());}
        return result;
    }
    static string Kind(JsonElement value)=>value.GetProperty("type").GetString()!;
    static void Check(bool value,string message){if(!value)throw new Exception(message);Console.WriteLine("PASS "+message);}
    internal static async Task Run()
    {
        string root=Path.Combine(Path.GetTempPath(),"mac-lan-queue-test-"+Guid.NewGuid().ToString("N"));
        await using var log=new DiagnosticLog(root,"synthetic-queue-test");
        foreach(string edge in new[]{"key","button","release","lease"})
        await using(var session=new Session(log))
        {
            var queue=Ready(session);
            for(int round=0;round<3;round++)
            {
                session.Send(new{type="key",code=55,down=true});
                Check(Kind(Drain(queue).Single())=="key","fixture consumer accepts press before congestion");
                for(int i=0;i<64;i++)session.Send(new{type="move",x=.5,y=.5});
                session.Send(new{type=edge,code=55,button=0,down=false,active=false,view=false});
                var recovered=Drain(queue);
                Check(recovered.Count==1&&Kind(recovered[0])=="release",$"{edge} overflow round {round}: release replaces stale backlog");
                session.Send(new{type="lease",active=true,view=true,generation=1});
                Check(Kind(Drain(queue).Single())=="lease","renewed lease follows recovery release");
                Check(session.IsReady,"input pressure preserves ready session");
            }
        }
        await using(var session=new Session(log))
        {
            var queue=Ready(session);
            Field("<SupportsKeyframes>k__BackingField").SetValue(session,true);
            for(int i=0;i<3;i++)session.Send(new{type="keyframe"});
            for(int i=0;i<61;i++)session.Send(new{type="move",x=.5,y=.5});
            session.Send(new{type="key",code=55,down=false});
            Check(Drain(queue).Select(Kind).SequenceEqual(new[]{"release","keyframe"}),"input recovery retains one independent queued video-recovery request");
        }
        foreach(string phase in new[]{"New","Authenticating","Starting"})
        await using(var session=new Session(log))
        {
            var queue=(Channel<string>)Field("outgoing").GetValue(session)!;
            Field("phase").SetValue(session,(int)Field(phase).GetRawConstantValue()!);
            queue.Writer.TryWrite("{\"type\":\"auth\"}");queue.Writer.TryWrite("{\"type\":\"start\"}");
            for(int i=0;i<100;i++)session.Send(new{type="key",code=55,down=false});
            Check(Drain(queue).Select(Kind).SequenceEqual(new[]{"auth","start"}),$"{phase}: input cannot discard unrelated handshake messages");
        }
        await using(var session=new Session(log))
        {
            var queue=Ready(session);
            foreach(string type in new[]{"key","move","button","button","key","scroll","release","lease"})session.Send(new{type,code=55,button=0,down=false});
            Check(Drain(queue).Select(Kind).SequenceEqual(new[]{"key","move","button","button","key","scroll","release","lease"}),"uncongested input retains exact order");
            session.Send(new{type="button",button=2,down=false});
            for(int i=0;i<64;i++)session.Send(new{type="move",x=.5,y=.5});
            var messages=Drain(queue);
            Check(messages.Count==64&&Kind(messages[0])=="button","dropping excess movement preserves queued discrete edges and capacity");
            session.Abort("synthetic abort");
            session.Send(new{type="key",code=55,down=true});session.Send(new{type="lease",active=true});
            Check(Drain(queue).Count==0&&!session.IsReady,"stopped sessions cannot enqueue new input");
        }
        // Same production channel with concurrent Send callers and a mock of
        // the transport's sole consumer. Every retained producer sequence must
        // remain ordered even when Send concurrently drains stale entries.
        await using(var session=new Session(log))
        using(var finished=new CancellationTokenSource())
        {
            var queue=Ready(session);
            var consumer=Task.Run(async()=>{
                var last=new Dictionary<int,int>();int consumed=0;
                while(!finished.IsCancellationRequested||queue.Reader.TryPeek(out _))
                {
                    if(!queue.Reader.TryRead(out var line)){await Task.Yield();continue;}
                    using var doc=JsonDocument.Parse(line);var value=doc.RootElement;
                    if(value.TryGetProperty("producer",out var p))
                    {
                        int producer=p.GetInt32(),sequence=value.GetProperty("sequence").GetInt32();
                        if(last.TryGetValue(producer,out var previous)&&sequence<=previous)throw new Exception("producer sequence reordered");
                        last[producer]=sequence;
                    }
                    consumed++;
                }
                return consumed;
            });
            try
            {
                await Task.WhenAll(Enumerable.Range(0,4).Select(producer=>Task.Run(()=>{
                    for(int sequence=0;sequence<1000;sequence++)
                    {
                        session.Send(new{type=sequence%25==0?"key":"move",producer,sequence,code=55,down=false,x=.5,y=.5});
                        if(queue.Reader.Count>64)throw new Exception("queue capacity exceeded");
                    }
                }))).WaitAsync(TimeSpan.FromSeconds(5));
            }
            finally{finished.Cancel();}
            Check(await consumer.WaitAsync(TimeSpan.FromSeconds(5))>0&&queue.Reader.Count==0,"four producers and concurrent consumer remain bounded, ordered and drainable");
        }
        await using(var fresh=new Session(log))
        {
            var queue=Ready(fresh);
            Check(queue.Reader.Count==0,"fresh session inherits no queued releases or input");
        }
    }
}
