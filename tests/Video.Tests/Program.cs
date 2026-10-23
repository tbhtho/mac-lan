using System.Buffers.Binary;
using System.Text.Json;
using MacDow.Core;

byte[] Packet(byte kind,uint sequence,int bytes=16)
{
    var packet=new byte[bytes];packet[0]=kind;BinaryPrimitives.WriteUInt32BigEndian(packet.AsSpan(1,4),sequence);return packet;
}
uint Sequence(byte[] packet)=>BinaryPrimitives.ReadUInt32BigEndian(packet.AsSpan(1,4));
object SlowBridge(int acknowledgmentMilliseconds)
{
    var frames=new EncodedFrameQueue();var delivered=new List<byte[]>();double busyUntil=-1;
    for(uint sequence=1;sequence<=600;sequence++)
    {
        double now=(sequence-1)*1000d/60;
        frames.Enqueue(Packet((sequence-1)%30==0?(byte)2:(byte)3,sequence),(long)now);
        if(now<busyUntil)continue;
        delivered.AddRange(frames.Drain((long)now));busyUntil=now+acknowledgmentMilliseconds;
    }
    delivered.AddRange(frames.Drain(10_000));
    return new{acknowledgmentMilliseconds,inputFrames=600,frames=delivered.Select(b=>new{kind=b[0],sequence=Sequence(b)}).ToArray()};
}
if(args.Contains("--fixture")){Console.WriteLine(JsonSerializer.Serialize(new[]{SlowBridge(18),SlowBridge(25),SlowBridge(40)}));return;}
int checks=0;
void Check(bool condition,string description){if(!condition)throw new Exception(description);checks++;Console.WriteLine("PASS "+description);}
{
    var queue=new EncodedFrameQueue();queue.Enqueue(Packet(3,1),0);
    Check(queue.Count==0,"H264 deltas cannot enter before an independent keyframe");
    queue.Enqueue(Packet(2,2),1);queue.Enqueue(Packet(3,3),2);queue.Enqueue(Packet(3,4),3);
    Check(queue.Drain(4).Select(Sequence).SequenceEqual(new uint[]{2,3,4}),"pending H264 dependencies drain together in wire order");
    queue.Enqueue(Packet(3,6),5);queue.Enqueue(Packet(3,7),6);
    Check(queue.Count==0,"a real sequence gap discards dependent deltas until another keyframe");
    queue.Enqueue(Packet(2,8),7);queue.Enqueue(Packet(3,9),8);
    Check(queue.Drain(9).Select(Sequence).SequenceEqual(new uint[]{8,9}),"an independent keyframe recovers after packet loss");
}
{
    var queue=new EncodedFrameQueue();for(uint i=1;i<=20;i++)queue.Enqueue(Packet(1,i),i);
    Check(queue.Count==1&&Sequence(queue.Drain(20).Single())==20,"independent JPEG pictures retain only the newest picture");
    queue.Enqueue(Packet(3,21),21);Check(queue.Count==0,"codec changes cannot treat a JPEG as an H264 reference");
}
{
    var queue=new EncodedFrameQueue(maxFrames:3);
    queue.Enqueue(Packet(2,1),0);queue.Enqueue(Packet(3,2),1);queue.Enqueue(Packet(3,3),2);queue.Enqueue(Packet(2,4),3);
    Check(queue.Count==1&&Sequence(queue.Drain(4).Single())==4,"count overflow recovers the newest complete key segment");
    queue.Enqueue(Packet(2,5),5);queue.Enqueue(Packet(3,6),6);queue.Enqueue(Packet(3,7),7);queue.Enqueue(Packet(3,8),8);
    Check(queue.Count==0,"overflow without a newer keyframe drops the whole broken segment");
    queue.Enqueue(Packet(3,9),9);queue.Enqueue(Packet(2,10),10);
    Check(Sequence(queue.Drain(11).Single())==10,"overflow never resumes from an interdependent delta");
}
{
    var queue=new EncodedFrameQueue(maxBytes:1000);
    queue.Enqueue(Packet(2,1,600),0);queue.Enqueue(Packet(3,2,600),1);
    Check(queue.Count==0&&queue.Bytes==0,"encoded queue bytes remain bounded independently of frame count");
    queue.Enqueue(Packet(2,3,1001),2);Check(queue.Count==0,"a single over-limit keyframe cannot bypass byte bounds");
}
{
    var queue=new EncodedFrameQueue();queue.Enqueue(Packet(2,1),0);queue.Enqueue(Packet(3,2),10);
    Check(queue.Drain(251).Count==0,"stalled UI cannot replay a queued segment older than250ms");
    queue.Enqueue(Packet(3,3),252);Check(queue.Count==0,"age expiry requires a fresh independent frame");
    queue.Enqueue(Packet(2,4),253);queue.Enqueue(Packet(3,5),254);
    Check(queue.Drain(255).Count==2,"fresh keyframe and its dependency survive recovery from a UI stall");
}
{
    var queue=new EncodedFrameQueue();for(uint i=1;i<=20;i++)queue.Enqueue(Packet(i==1?(byte)2:(byte)3,i),i);
    Check(queue.Drain(20).Count==8&&queue.Count==12,"one bridge batch is capped at8 frames without dropping dependencies");
    queue.Clear();queue.Enqueue(Packet(3,21),21);Check(queue.Count==0,"stream clear cannot carry references across epochs");
}
{
    var queue=new EncodedFrameQueue();queue.Enqueue(Packet(2,uint.MaxValue),0);queue.Enqueue(Packet(3,0),1);
    Check(queue.Drain(2).Count==2,"sequence wrap preserves contiguous dependencies");
    queue.Enqueue(new byte[5],3);queue.Enqueue(Packet(3,1),4);
    Check(queue.Count==0,"invalid packet revokes dependent references");
}
foreach(int delay in new[]{18,25,40})
{
    using var fixture=JsonDocument.Parse(JsonSerializer.Serialize(SlowBridge(delay)));
    var frames=fixture.RootElement.GetProperty("frames").EnumerateArray().ToArray();
    Check(frames.Length==600&&frames.Select(f=>f.GetProperty("sequence").GetUInt32()).SequenceEqual(Enumerable.Range(1,600).Select(i=>(uint)i)),"bridge acknowledgment at"+delay+"ms preserves all600 encoded dependencies over10 seconds");
}
{
    var bridge=new VideoBatchGate();long epoch=bridge.Epoch,batch=bridge.Begin(0)!.Value;
    Check(bridge.Begin(1)==null,"only one encoded batch may cross the UI bridge at once");
    Check(!bridge.Acknowledge(epoch,batch+1)&&!bridge.Acknowledge(epoch+1,batch),"wrong batch or stream epoch cannot acknowledge queued data");
    Check(!bridge.Expired(250)&&bridge.Expired(251),"a missing bridge acknowledgment has a250ms age bound");
    bridge.Reset();
    Check(bridge.Begin(252)==null&&!bridge.AcknowledgeReset(epoch),"reset blocks new batches until the browser acknowledges the current epoch");
    Check(bridge.AcknowledgeReset(bridge.Epoch)&&!bridge.AcknowledgeReset(bridge.Epoch),"one matching clear acknowledgment opens the new stream exactly once");
    long next=bridge.Begin(252)!.Value;
    Check(!bridge.IsCurrent(epoch)&&!bridge.Acknowledge(epoch,batch),"late presentation and acknowledgment from a cleared stream are rejected");
    Check(bridge.Acknowledge(bridge.Epoch,next)&&!bridge.Acknowledge(bridge.Epoch,next),"each matching acknowledgment releases one batch exactly once");
}
Console.WriteLine(checks+" video queue checks passed; synthetic packets only.");
