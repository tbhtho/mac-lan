namespace MacDow.Core;

// UI-thread state for the single outstanding WebView batch. Clearing a stream
// revokes old acknowledgments and decoded presentation notifications together.
public sealed class VideoBatchGate
{
    public long Epoch{get;private set;}=1;
    long nextBatch,sentAt;
    long? pending;
    public bool Ready{get;private set;}=true;
    public long? Begin(long nowMilliseconds)
    {
        if(!Ready||pending!=null)return null;
        sentAt=nowMilliseconds;pending=++nextBatch;return pending;
    }
    public bool Acknowledge(long epoch,long batch)
    {
        if(epoch!=Epoch||pending!=batch)return false;
        pending=null;return true;
    }
    public bool IsCurrent(long epoch)=>Ready&&epoch==Epoch;
    public bool Expired(long nowMilliseconds)=>pending!=null&&nowMilliseconds-sentAt>250;
    public bool HasPending=>pending!=null;
    public void Reset(){Epoch++;pending=null;Ready=false;}
    public bool AcknowledgeReset(long epoch)
    {
        if(Ready||epoch!=Epoch)return false;
        Ready=true;return true;
    }
}
