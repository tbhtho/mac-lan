using System.IO;
using System.Text.Json;
namespace MacDow.Windows;

internal static class Telemetry
{
    static readonly Lazy<DiagnosticLog> log=new(()=>new DiagnosticLog(DirectoryPath,Version));
    internal static string DirectoryPath=>Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","diagnostics");
    internal static DiagnosticLog Log=>log.Value;
    static string Version
    {
        get
        {
            using var input=typeof(Telemetry).Assembly.GetManifestResourceStream("MacDow.Diagnostics.build-config.json");
            if(input==null)return "development";
            using var doc=JsonDocument.Parse(input);return doc.RootElement.GetProperty("version").GetString()??"development";
        }
    }
    // Windows diagnostics stay on this computer. Only the Mac reporter uploads.
    internal static void Report(string name,string code="ok",int hosts=0)
    {
        Log.Record(name,code,new{hosts});
    }
    internal static async Task Flush(){if(log.IsValueCreated)await Log.DisposeAsync().ConfigureAwait(false);}
}
