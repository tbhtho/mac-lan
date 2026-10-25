using System.IO;
using System.Text.Json;

namespace MacDow.Windows;

internal sealed class BarPreferencesStore(string path)
{
    public static BarPreferencesStore CurrentUser()=>new(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","bar-preferences.json"));
    sealed record Preferences(bool AutoHide);
    public bool LoadAutoHide()
    {
        try{return JsonSerializer.Deserialize<Preferences>(File.ReadAllText(path))?.AutoHide??false;}
        catch(Exception error)when(error is IOException or UnauthorizedAccessException or JsonException or System.Security.SecurityException){return false;}
    }
    public void SaveAutoHide(bool enabled)
    {
        string destination=Path.GetFullPath(path),temporary=destination+"."+Guid.NewGuid().ToString("N")+".tmp";
        Directory.CreateDirectory(Path.GetDirectoryName(destination)!);
        try{File.WriteAllText(temporary,JsonSerializer.Serialize(new Preferences(enabled)));File.Move(temporary,destination,true);}
        finally{if(File.Exists(temporary))File.Delete(temporary);}
    }
}
