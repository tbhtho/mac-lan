namespace MacDow.Core;

public sealed record StreamProfile(string Label,string Codec)
{
    public static StreamProfile DefaultHardware {get;}=new("H.264 hardware","h264");
    public static StreamProfile Compatible {get;}=new("Compatible (JPEG)","jpeg");
    public static StreamProfile H264Experimental => DefaultHardware;
    public static IReadOnlyList<StreamProfile> Options {get;}=[DefaultHardware,Compatible];

    public object CreateStart(uint window,int width=1920,int fps=60,int bitrate=8)=>
        // audio: this Windows app plays Mac sound; older viewers would misread audio packets.
        new{type="start",window,width,fps,quality=70,codec=Codec,bitrate,audio=true};
}
