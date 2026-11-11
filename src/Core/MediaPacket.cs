using System.Buffers.Binary;

namespace MacDow.Core;

// Flagged packets retain the legacy kind/sequence/encode fields and add the
// shared capture clock before the payload. Zero is an unavailable timestamp.
public readonly record struct MediaPacket(byte Kind,uint Sequence,int PayloadOffset,ulong CaptureMicroseconds)
{
    public static bool TryRead(ReadOnlySpan<byte> packet,out MediaPacket media)
    {
        media=default;
        if(packet.Length<10)return false;
        byte kind=(byte)(packet[0]&0x7f);
        int header=(packet[0]&0x80)!=0?17:9;
        if(kind is <1 or >4||packet.Length<=header)return false;
        ulong capture=header==17?BinaryPrimitives.ReadUInt64BigEndian(packet.Slice(9,8)):0;
        if(capture>9007199254740991UL)return false;
        media=new(kind,BinaryPrimitives.ReadUInt32BigEndian(packet.Slice(1,4)),header,capture);
        return true;
    }
}
