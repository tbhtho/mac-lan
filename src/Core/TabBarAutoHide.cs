namespace MacDow.Core;

public sealed class TabBarAutoHide(bool enabled=false)
{
    public bool Enabled{get;private set;}=enabled;
    public bool Revealed{get;private set;}=!enabled;
    long? departed;
    public void Configure(bool enabled){Enabled=enabled;Revealed=true;departed=null;}
    public bool Update(bool atEdge,bool insideBar,bool interacting,long now)
    {
        bool previous=Revealed;
        if(!Enabled||atEdge||insideBar||interacting){Revealed=true;departed=null;}
        else if(Revealed){departed??=now;if(now-departed.Value>=600)Revealed=false;}
        return previous!=Revealed;
    }
}
