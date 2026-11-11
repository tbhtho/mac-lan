using System.Buffers.Binary;

namespace MacDow.Core;

// Encoded H264 frames are a reference chain. A slow UI bridge may batch that
// chain, but cannot replace individual deltas with the newest packet.
public sealed class EncodedFrameQueue
{
    public const int MaximumPacketBytes=4*1024*1024;
    public const int MaximumBatchFrames=8;
    public const int MaximumBatchBytes=4*1024*1024;
    readonly object sync=new();
    readonly List<Entry> pending=new();
    readonly int maxFrames,maxBytes,maxAgeMilliseconds;
    int bytes;
    uint? lastSequence;
    bool waitingForKey=true,jpeg;
    record Entry(byte[] Packet,long Received);
    public EncodedFrameQueue(int maxFrames=64,int maxBytes=8*1024*1024,int maxAgeMilliseconds=250)
    {
        if(maxFrames<1||maxBytes<10||maxAgeMilliseconds<1)throw new ArgumentOutOfRangeException();
        this.maxFrames=maxFrames;this.maxBytes=maxBytes;this.maxAgeMilliseconds=maxAgeMilliseconds;
    }
    public int Count{get{lock(sync)return pending.Count;}}
    public int Bytes{get{lock(sync)return bytes;}}
    public void Clear(){lock(sync)Reset();}
    void Reset(){pending.Clear();bytes=0;lastSequence=null;waitingForKey=true;jpeg=false;}
    public void Enqueue(byte[] packet,long receivedMilliseconds)
    {
        lock(sync)
        {
            Expire(receivedMilliseconds);
            if(packet.Length<10||packet.Length>MaximumPacketBytes||packet.Length>maxBytes||packet[0] is <1 or >3){Reset();return;}
            byte kind=packet[0];
            if(kind==1)
            {
                Reset();jpeg=true;pending.Add(new(packet,receivedMilliseconds));bytes=packet.Length;return;
            }
            if(jpeg)Reset();
            uint sequence=BinaryPrimitives.ReadUInt32BigEndian(packet.AsSpan(1,4));
            if(lastSequence is uint previous&&sequence!=unchecked(previous+1))Reset();
            if(kind==2)waitingForKey=false;
            if(waitingForKey)return;
            lastSequence=sequence;pending.Add(new(packet,receivedMilliseconds));bytes+=packet.Length;
            if(pending.Count>maxFrames||bytes>maxBytes)Recover(receivedMilliseconds);
        }
    }
    public IReadOnlyList<byte[]> Drain(long nowMilliseconds)
    {
        lock(sync)
        {
            Expire(nowMilliseconds);
            var batch=new List<byte[]>(Math.Min(MaximumBatchFrames,pending.Count));int size=0;
            while(pending.Count>0&&batch.Count<MaximumBatchFrames)
            {
                var next=pending[0];if(size+next.Packet.Length>MaximumBatchBytes)break;
                pending.RemoveAt(0);bytes-=next.Packet.Length;size+=next.Packet.Length;batch.Add(next.Packet);
            }
            return batch;
        }
    }
    void Expire(long nowMilliseconds)
    {
        if(pending.Count>0&&nowMilliseconds-pending[0].Received>maxAgeMilliseconds)Recover(nowMilliseconds);
    }
    void Recover(long nowMilliseconds)
    {
        // Only a complete segment beginning at an independent frame can replace
        // old encoded data. Never replay a delta whose reference was discarded.
        int tailBytes=0;
        for(int i=pending.Count-1;i>=0;i--)
        {
            tailBytes+=pending[i].Packet.Length;
            if(pending.Count-i>maxFrames||tailBytes>maxBytes||nowMilliseconds-pending[i].Received>maxAgeMilliseconds)break;
            if(pending[i].Packet[0] is not (1 or 2))continue;
            pending.RemoveRange(0,i);bytes=tailBytes;return;
        }
        Reset();
    }
}
