namespace MacDow.Core;
public sealed record DiscoveredHost(string Name,string Address,int Port,string Fingerprint)
{
    public string Label => Name+" ("+Address+")";
    public static bool TryCreate(string name,string address,int port,string fingerprint,out DiscoveredHost? host)
    {
        host=null;
        if(name.Length is <1 or >63 || name.Any(char.IsControl) || port is <1024 or >65535 ||
            !Pairing.Validate(address,port.ToString(System.Globalization.CultureInfo.InvariantCulture),fingerprint,new string('0',64)))return false;
        host=new(name,address,port,fingerprint.ToLowerInvariant());return true;
    }
}
