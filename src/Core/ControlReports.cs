namespace MacDow.Core;

// The Mac's release reports (lease expired, stale input, generation revoked) name the control
// session they are about. After a network stall the Mac answers the queued messages of a
// session Windows has already replaced; a report about that earlier session must not end the
// new one, or one stall releases and re-takes control several times in a row.
public static class ControlReports
{
    // A report without a generation comes from an older Mac app and is taken at face value.
    public static bool AboutEarlierSession(ulong? reported,ulong current)=>reported is ulong generation&&generation!=0&&generation<current;
}
