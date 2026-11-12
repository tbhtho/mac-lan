using System.IO;
using System.Runtime.InteropServices;
using System.Text.Json;

namespace MacDow.Windows;

/// <summary>A Mac that approved this PC: its pinned identity and this PC's credential for it.</summary>
internal sealed record TrustedMac(string Name,string Fingerprint,string Device,string Token);

/// <summary>
/// Remembered Macs for this Windows account. The Mac's per-PC secret is encrypted with
/// Windows DPAPI for the current user, so another account or a copied file cannot use it.
/// </summary>
internal sealed class TrustedMacs(string path,Func<byte[],byte[]> protect,Func<byte[],byte[]> unprotect)
{
    sealed record Entry(string Name,string Fingerprint,string Device,string Secret);
    public static TrustedMacs ForCurrentUser()=>new(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","trusted-macs.json"),Dpapi.Protect,Dpapi.Unprotect);
    static bool Hex(string? value,int length)=>value?.Length==length&&value.All(c=>c is >= '0' and <= '9' or >= 'a' and <= 'f');
    public static bool Valid(string? fingerprint,string? device,string? token)=>Hex(fingerprint,64)&&Hex(device,32)&&Hex(token,64);

    public IReadOnlyList<TrustedMac> Load()
    {
        var result=new List<TrustedMac>();
        try
        {
            if(!File.Exists(path)||new FileInfo(path).Length>256*1024)return result;
            foreach(var e in JsonSerializer.Deserialize<List<Entry?>>(File.ReadAllText(path))??[])
            {
                // A damaged entry (missing fields, another account's secret) is skipped, never fatal.
                if(e?.Secret is not string secret||e.Fingerprint is null||e.Device is null)continue;
                string token;
                try{token=System.Text.Encoding.UTF8.GetString(unprotect(Convert.FromBase64String(secret)));}catch(Exception ex)when(ex is FormatException or CryptographicFailure or ArgumentException){continue;}
                string name=e.Name??"Mac";
                if(Valid(e.Fingerprint,e.Device,token))result.Add(new(name.Length>64?name[..64]:name,e.Fingerprint,e.Device,token));
            }
        }
        catch(Exception ex)when(ex is IOException or UnauthorizedAccessException or JsonException or NotSupportedException){}
        return result;
    }
    public TrustedMac? Find(string fingerprint)=>Load().FirstOrDefault(m=>m.Fingerprint==fingerprint);
    public void Remember(TrustedMac mac)
    {
        if(!Valid(mac.Fingerprint,mac.Device,mac.Token))return;
        var all=Load().Where(m=>m.Fingerprint!=mac.Fingerprint).Append(mac).TakeLast(16).ToList();
        _=Save(all);
    }
    public void Forget(string fingerprint)=>_=Save(Load().Where(m=>m.Fingerprint!=fingerprint).ToList());
    public int ForgetAll(){int count=Load().Count;return Save([])?count:-1;}
    /// <summary>True when at least one Mac was approved before; mac-lan then connects by itself.</summary>
    public bool Any()=>Load().Count>0;
    // A locked or full disk must not stop mac-lan; the change simply is not saved.
    bool Save(List<TrustedMac> all)
    {
        string temporary=path+"."+Guid.NewGuid().ToString("N")+".tmp";
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(path)!);
            var entries=all.Select(m=>new Entry(m.Name,m.Fingerprint,m.Device,Convert.ToBase64String(protect(System.Text.Encoding.UTF8.GetBytes(m.Token))))).ToList();
            File.WriteAllText(temporary,JsonSerializer.Serialize(entries));File.Move(temporary,path,true);return true;
        }
        catch(Exception ex)when(ex is IOException or UnauthorizedAccessException or CryptographicFailure){return false;}
        finally{try{if(File.Exists(temporary))File.Delete(temporary);}catch(Exception ex)when(ex is IOException or UnauthorizedAccessException){}}
    }
}

internal sealed class CryptographicFailure(string message):Exception(message);

/// <summary>Current-user DPAPI through crypt32, without an extra package.</summary>
internal static class Dpapi
{
    [StructLayout(LayoutKind.Sequential)] struct Blob {public int Size;public nint Data;}
    [DllImport("crypt32.dll",SetLastError=true,CharSet=CharSet.Unicode)] static extern bool CryptProtectData(ref Blob input,string? description,nint entropy,nint reserved,nint prompt,int flags,out Blob output);
    [DllImport("crypt32.dll",SetLastError=true,CharSet=CharSet.Unicode)] static extern bool CryptUnprotectData(ref Blob input,nint description,nint entropy,nint reserved,nint prompt,int flags,out Blob output);
    [DllImport("kernel32.dll")] static extern nint LocalFree(nint memory);
    const int UiForbidden=0x1;
    public static byte[] Protect(byte[] data)=>Run(data,true);
    public static byte[] Unprotect(byte[] data)=>Run(data,false);
    static byte[] Run(byte[] data,bool protect)
    {
        var handle=GCHandle.Alloc(data,GCHandleType.Pinned);
        try
        {
            var input=new Blob{Size=data.Length,Data=handle.AddrOfPinnedObject()};
            bool ok=protect?CryptProtectData(ref input,"mac-lan",0,0,0,UiForbidden,out var output):CryptUnprotectData(ref input,0,0,0,0,UiForbidden,out output);
            if(!ok)throw new CryptographicFailure("DPAPI failed with error "+Marshal.GetLastWin32Error());
            try{var result=new byte[output.Size];Marshal.Copy(output.Data,result,0,output.Size);return result;}
            finally{LocalFree(output.Data);}
        }
        finally{handle.Free();}
    }
}
