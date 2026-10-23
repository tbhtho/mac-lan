using System.Net;
namespace MacDow.Core;
public record MonitorArea(string Id, int Left, int Top, int Width, int Height, uint Dpi, bool Primary)
{
    public double DipWidth => Width * 96.0 / Dpi;
    public double DipHeight => Height * 96.0 / Dpi;
    public string DisplayName => Id.Replace("\\\\.\\DISPLAY","Display ")+(Primary?" (main)":"")+$" - {Width} x {Height}";
}
public static class DisplayLayout
{
    public static MonitorArea Select(IReadOnlyList<MonitorArea> monitors, string? preferred = null) =>
        monitors.FirstOrDefault(m => m.Id == preferred) ?? monitors.FirstOrDefault(m => !m.Primary) ?? monitors.First();
}
public static class TabBarLayout
{
    public static int HeightPixels(uint dpi)=>(int)Math.Ceiling(48*Math.Max(96,dpi)/96.0);
}
public static class Viewport
{
    public static (double X,double Y)? Map(double x,double y,double width,double height,double imageWidth,double imageHeight)
    {
        if (width <= 0 || height <= 0 || imageWidth <= 0 || imageHeight <= 0) return null;
        double scale = Math.Min(width/imageWidth,height/imageHeight);
        double w=imageWidth*scale,h=imageHeight*scale;
        x -= (width-w)/2; y -= (height-h)/2;
        if (x<0 || y<0 || x>=w || y>=h) return null;
        return (x/w,y/h);
    }
}
public static class Pairing
{
    public static bool IsPrivate(IPAddress ip)
    {
        byte[] b=ip.GetAddressBytes();
        return b.Length==4 && (b[0]==10 || b[0]==172 && b[1]>=16 && b[1]<=31 || b[0]==192 && b[1]==168);
    }
    public static string IdentityCode(string fingerprint)
    {
        if(fingerprint.Length!=64||!fingerprint.All(Uri.IsHexDigit))throw new ArgumentException("Invalid fingerprint");
        return string.Join("-",Enumerable.Range(0,8).Select(i=>fingerprint.Substring(i*4,4).ToUpperInvariant()));
    }
    public static bool Validate(string host,string port,string fingerprint,string token) =>
        IPAddress.TryParse(host,out var ip) && IsPrivate(ip) && int.TryParse(port,out var p) && p>0 && p<=65535 &&
        fingerprint.Length==64 && token.Length==64 && fingerprint.All(Uri.IsHexDigit) && token.All(Uri.IsHexDigit);
}
