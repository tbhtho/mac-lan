using System.IO;
using System.Text.Json;

namespace MacDow.Windows;

internal sealed class BarPreferencesStore(string path)
{
    public static BarPreferencesStore CurrentUser()=>new(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"MAC-DOW","bar-preferences.json"));
    sealed record Preferences(bool AutoHide,bool MatchDisplay=false);
    Preferences Load()
    {
        try{return JsonSerializer.Deserialize<Preferences>(File.ReadAllText(path))??new(false);}
        catch(Exception error)when(error is IOException or UnauthorizedAccessException or JsonException or System.Security.SecurityException){return new(false);}
    }
    public bool LoadAutoHide()=>Load().AutoHide;
    // Asks the Mac for a desktop shaped like this display, so the picture has no bars.
    public bool LoadMatchDisplay()=>Load().MatchDisplay;
    public void SaveAutoHide(bool enabled)=>Save(Load() with{AutoHide=enabled});
    public void SaveMatchDisplay(bool enabled)=>Save(Load() with{MatchDisplay=enabled});
    void Save(Preferences preferences)
    {
        string destination=Path.GetFullPath(path),temporary=destination+"."+Guid.NewGuid().ToString("N")+".tmp";
        Directory.CreateDirectory(Path.GetDirectoryName(destination)!);
        try{File.WriteAllText(temporary,JsonSerializer.Serialize(preferences));File.Move(temporary,destination,true);}
        finally{if(File.Exists(temporary))File.Delete(temporary);}
    }
}
