namespace MacDow.Core;

// Audio packets are independent PCM chunks; they may be shed when the UI is busy.
public sealed class AudioDispatchQueue
{
    const int MaximumPackets=24,MaximumBytes=96*1024,MaximumAgeMilliseconds=250,BatchPackets=8;
    readonly object sync=new();
    readonly Queue<Entry> pending=new();
    readonly Action<Action> schedule;
    readonly Action<byte[]> play;
    readonly Func<long> clock;
    record Entry(byte[] Packet,long Received);
    int bytes;
    bool scheduled;
    public AudioDispatchQueue(Action<Action> schedule,Action<byte[]> play,Func<long>? clock=null)
    {this.schedule=schedule;this.play=play;this.clock=clock??(()=>Environment.TickCount64);}
    public int Count{get{lock(sync)return pending.Count;}}
    public void Enqueue(byte[] packet)
    {
        if(packet.Length<=9||packet.Length>MaximumBytes||packet[0]!=4)return;
        bool dispatch=false;
        lock(sync)
        {
            long now=clock();Expire(now);
            pending.Enqueue(new(packet,now));bytes+=packet.Length;
            while(pending.Count>MaximumPackets||bytes>MaximumBytes)Drop();
            if(!scheduled){scheduled=true;dispatch=true;}
        }
        if(dispatch)schedule(Drain);
    }
    void Drop(){bytes-=pending.Dequeue().Packet.Length;}
    void Expire(long now)
    {while(pending.TryPeek(out var next)&&now-next.Received>MaximumAgeMilliseconds)Drop();}
    void Drain()
    {
        var batch=new List<Entry>(BatchPackets);
        lock(sync)
        {
            Expire(clock());
            while(pending.Count>0&&batch.Count<BatchPackets)
            {var next=pending.Dequeue();bytes-=next.Packet.Length;batch.Add(next);}
        }
        try
        {
            foreach(var next in batch)
                if(clock()-next.Received<=MaximumAgeMilliseconds)play(next.Packet);
        }
        finally
        {
            bool again;
            lock(sync){again=pending.Count>0;if(!again)scheduled=false;}
            if(again)schedule(Drain);
        }
    }
}
