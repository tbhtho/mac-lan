using System.IO;
using System.Reflection;
using System.Security.Cryptography;
using System.Text.Json;

namespace MacDow.Windows;

internal static class Payloads
{
    const string Prefix="MacDow.Payload.";
    static readonly Lazy<string> extracted=new(Extract);
    internal static string Root=>extracted.Value;
    internal static string Helper=>Path.Combine(Root,"MAC-DOW-Update.exe");
    internal static string Install=>Environment.ProcessPath??throw new IOException("Cannot locate MAC-DOW.exe.");
    internal static object Diagnose()
    {
        string root=Root;
        return new {executable=Install, payloadRoot=root, helperPresent=File.Exists(Helper),viewerPresent=new[]{"viewer.html","viewer.mjs","viewer-core.mjs"}.All(n=>File.Exists(Path.Combine(root,n))),uiStarted=false,hooksStarted=false,captureStarted=false};
    }
    static string Extract()
    {
        var assembly=Assembly.GetExecutingAssembly();
        var resources=assembly.GetManifestResourceNames().Where(n=>n.StartsWith(Prefix,StringComparison.Ordinal)).Order().ToArray();
        var files=new List<(string Name,byte[] Bytes)>();
        using var hash=IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
        foreach(string name in resources)
        {
            using var input=assembly.GetManifestResourceStream(name)??throw new IOException("Missing bundled resource.");
            using var data=new MemoryStream();input.CopyTo(data);byte[] bytes=data.ToArray();
            files.Add((name[Prefix.Length..],bytes));hash.AppendData(System.Text.Encoding.UTF8.GetBytes(name));hash.AppendData(bytes);
        }
        string root=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","runtime",Convert.ToHexString(hash.GetHashAndReset()));
        RejectLinks(root);Directory.CreateDirectory(root);RejectLinks(root);
        foreach(var (name,bytes) in files)
        {
            if(Path.GetFileName(name)!=name)throw new IOException("Invalid bundled resource name.");
            string path=Path.Combine(root,name);RejectLinks(path);
            if(File.Exists(path)&&SHA256.HashData(File.ReadAllBytes(path)).SequenceEqual(SHA256.HashData(bytes)))continue;
            string temporary=path+"."+Guid.NewGuid().ToString("N")+".tmp";
            try {File.WriteAllBytes(temporary,bytes);File.Move(temporary,path,true);}
            finally {if(File.Exists(temporary))File.Delete(temporary);}
        }
        // Normal development builds may use the separately built helper. Published builds embed it.
        string legacy=Path.Combine(AppContext.BaseDirectory,"MAC-DOW-Update.exe");
        if(!File.Exists(Path.Combine(root,"MAC-DOW-Update.exe"))&&File.Exists(legacy))return AppContext.BaseDirectory;
        return root;
    }
    static void RejectLinks(string path)
    {
        for(string? current=Path.GetFullPath(path);current!=null;current=Path.GetDirectoryName(current))
            if((File.Exists(current)||Directory.Exists(current))&&(File.GetAttributes(current)&FileAttributes.ReparsePoint)!=0)throw new IOException("Linked application cache paths are unsupported.");
    }
}
