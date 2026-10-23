using System.IO;
using System.Text.Json;
namespace MacDow.Windows;

internal static class Telemetry
{
    static readonly object sync=new();
    // Windows diagnostics stay on this computer. Only the Mac reporter uploads.
    internal static void Report(string name,string code="ok",int hosts=0)
    {
        try {lock(sync){string root=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","diagnostics");Directory.CreateDirectory(root);string path=Path.Combine(root,"windows-local.jsonl");if(File.Exists(path)&&new FileInfo(path).Length>256*1024)File.Move(path,path+".previous",true);File.AppendAllText(path,JsonSerializer.Serialize(new{time=DateTime.UtcNow,name,code,hosts})+Environment.NewLine);}}
        catch(Exception e)when(e is IOException or UnauthorizedAccessException){}
    }
}
