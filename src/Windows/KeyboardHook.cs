using System.Runtime.InteropServices;
using System.Windows.Threading;
using MacDow.Core;
using Microsoft.Win32;
namespace MacDow.Windows;

// The low-level keyboard hook lives on its own thread with its own message loop. Windows calls
// it for every keystroke on the PC and silently removes a hook that answers too slowly, so it
// must never wait on the WPF UI thread: it only routes the key, sends it and queues UI work.
internal sealed class KeyboardHook : IDisposable
{
    [StructLayout(LayoutKind.Sequential)] struct KeyInfo { public uint Vk,Scan,Flags,Time;public nint Extra; }
    [StructLayout(LayoutKind.Sequential)] struct Msg { public nint Hwnd;public uint Message;public nint W,L;public uint Time;public int X,Y,Private; }
    delegate nint HookProc(int code,nint message,nint data);
    delegate void WinEventProc(nint hook,uint evt,nint hwnd,int obj,int child,uint thread,uint time);
    [DllImport("user32.dll",SetLastError=true)] static extern nint SetWindowsHookEx(int id,HookProc callback,nint module,uint thread);
    [DllImport("user32.dll")] static extern bool UnhookWindowsHookEx(nint hook);
    [DllImport("user32.dll")] static extern nint CallNextHookEx(nint hook,int code,nint message,nint data);
    [DllImport("kernel32.dll",CharSet=CharSet.Unicode)] static extern nint GetModuleHandle(string? name);
    [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
    [DllImport("user32.dll")] static extern int GetMessage(out Msg msg,nint hwnd,uint min,uint max);
    [DllImport("user32.dll")] static extern bool PeekMessage(out Msg msg,nint hwnd,uint min,uint max,uint remove);
    [DllImport("user32.dll",SetLastError=true)] static extern bool PostThreadMessage(uint thread,uint message,nint w,nint l);
    [DllImport("user32.dll")] static extern nint SetWinEventHook(uint min,uint max,nint module,WinEventProc callback,uint process,uint thread,uint flags);
    [DllImport("user32.dll")] static extern bool UnhookWinEvent(nint hook);
    const uint InstallMessage=0x8001,UninstallMessage=0x8002,ForgetMessage=0x8003,QuitMessage=0x0012;
    const uint DesktopSwitch=0x0020;
    sealed class InstallRequest { public readonly ManualResetEventSlim Done=new();public int Error; }

    readonly HookProc callback;
    readonly WinEventProc desktopCallback;
    readonly Action<int,bool> send;
    readonly Action escape;
    readonly Dispatcher dispatcher;
    readonly object sync=new();
    // Router state belongs to the hook thread; the UI thread touches it only under sync.
    readonly KeyRouter router=new(v=>(Native.GetAsyncKeyState(v)&0x8000)!=0);
    readonly ManualResetEventSlim ready=new();
    Thread? thread;
    uint threadId;
    nint hook;
    nint owner;
    bool armed,uninstallPosted,disposed;
    InstallRequest? request;
    // Raised on the UI thread when a lock, secure desktop or session switch hid key releases from us.
    public event Action? InputLost;

    public KeyboardHook(Action<int,bool> send,Action escape)
    {
        this.send=send;this.escape=escape;dispatcher=Dispatcher.CurrentDispatcher;callback=Handle;desktopCallback=DesktopSwitched;
        SystemEvents.SessionSwitch+=SessionSwitched;
    }

    // Routes keys to the Mac while owner is the foreground window. Each arm installs a fresh hook,
    // so a hook Windows removed after a stall recovers the next time control is armed.
    public void Install(nint owner)
    {
        ObjectDisposedException.ThrowIf(disposed,this);
        Start();
        var pending=new InstallRequest();
        lock(sync)
        {
            // A lingering hook still owes swallowed releases for keys held when control ended; keep them.
            if(hook==0)router.Reset();
            this.owner=owner;armed=true;request=pending;
        }
        if(!PostThreadMessage(threadId,InstallMessage,0,0))pending.Error=Marshal.GetLastWin32Error();
        else if(!pending.Done.Wait(TimeSpan.FromSeconds(2)))pending.Error=1460; // ERROR_TIMEOUT
        if(pending.Error!=0){lock(sync)armed=false;throw new System.ComponentModel.Win32Exception(pending.Error);}
    }

    // Control ended: keep swallowing releases of keys the Mac received, so Windows never
    // sees a stray Windows-key or Alt release, then remove the hook once they are all up.
    public void Release()
    {
        lock(sync)
        {
            armed=false;router.ReleaseControl();
            if(hook!=0&&!router.OwnsKeys)PostUninstall();
        }
    }

    public void Dispose()
    {
        if(disposed)return;
        disposed=true;
        SystemEvents.SessionSwitch-=SessionSwitched;
        lock(sync)armed=false;
        if(thread!=null){PostThreadMessage(threadId,QuitMessage,0,0);thread.Join(TimeSpan.FromSeconds(2));}
    }

    void Start()
    {
        if(thread!=null)return;
        thread=new Thread(Run){IsBackground=true,Name="mac-lan keyboard hook",Priority=ThreadPriority.AboveNormal};
        thread.Start();
        if(!ready.Wait(TimeSpan.FromSeconds(2)))throw new TimeoutException("The keyboard hook thread did not start.");
    }

    void Run()
    {
        threadId=GetCurrentThreadId();
        PeekMessage(out _,0,0,0,0); // Create this thread's message queue before anyone posts to it.
        // Out-of-context WinEvents arrive through this thread's message loop.
        nint desktopHook=SetWinEventHook(DesktopSwitch,DesktopSwitch,0,desktopCallback,0,0,0);
        ready.Set();
        while(GetMessage(out var message,0,0,0)>0)
        {
            switch(message.Message)
            {
                case InstallMessage:InstallOnThread();break;
                case UninstallMessage:UninstallOnThread();break;
                case ForgetMessage:Forget();break;
            }
        }
        if(desktopHook!=0)UnhookWinEvent(desktopHook);
        nint last;
        lock(sync){last=hook;hook=0;}
        if(last!=0)UnhookWindowsHookEx(last);
    }

    void InstallOnThread()
    {
        InstallRequest? pending;
        lock(sync){pending=request;request=null;}
        if(pending==null)return;
        nint fresh=SetWindowsHookEx(13,callback,GetModuleHandle(null),0);
        if(fresh==0){pending.Error=Marshal.GetLastWin32Error();if(pending.Error==0)pending.Error=1428;pending.Done.Set();return;}
        nint old;
        lock(sync){old=hook;hook=fresh;}
        if(old!=0)UnhookWindowsHookEx(old);
        pending.Done.Set();
    }

    void PostUninstall()
    {
        if(uninstallPosted)return;
        uninstallPosted=PostThreadMessage(threadId,UninstallMessage,0,0);
    }

    void UninstallOnThread()
    {
        nint old;
        lock(sync)
        {
            uninstallPosted=false;
            if(armed||router.OwnsKeys||hook==0)return;
            old=hook;hook=0;
        }
        UnhookWindowsHookEx(old);
    }

    nint Handle(int code,nint message,nint data)
    {
        if(code<0)return CallNextHookEx(0,code,message,data);
        try
        {
            var key=Marshal.PtrToStructure<KeyInfo>(data);bool down=message==0x100||message==0x104;
            KeyDecision decision;
            lock(sync)
            {
                bool allowed=armed&&Native.GetForegroundWindow()==owner;
                decision=router.Route((int)key.Vk,(int)key.Scan,(key.Flags&1)!=0,down,allowed,key.Time);
                // Sending under sync keeps every key ahead of the release message the UI sends after Release().
                switch(decision.Route)
                {
                    case KeyRoute.Remote:send(decision.MacCode,down);break;
                    case KeyRoute.Sequence:foreach(var (mac,press) in decision.Keys!)send(mac,press);break;
                    case KeyRoute.Release:
                    case KeyRoute.ReleaseLocal:
                        // Stop routing at once; the UI thread finishes the release when it gets to it.
                        armed=false;router.ReleaseControl();break;
                }
                if(!armed&&!router.OwnsKeys)PostUninstall();
            }
            if(decision.Route is KeyRoute.Release or KeyRoute.ReleaseLocal)dispatcher.BeginInvoke(escape);
            if(decision.Route is KeyRoute.Remote or KeyRoute.Sequence or KeyRoute.Swallow or KeyRoute.Release)return 1;
        }
        catch(Exception ex)
        {
            // An exception must never escape a hook callback; let the key through instead.
            System.Diagnostics.Debug.WriteLine(ex);
        }
        return CallNextHookEx(0,code,message,data);
    }

    void DesktopSwitched(nint winEventHook,uint evt,nint hwnd,int obj,int child,uint eventThread,uint time)
    {
        if(evt==DesktopSwitch)Forget();
    }

    void SessionSwitched(object? sender,SessionSwitchEventArgs e)
    {
        if(thread!=null&&!disposed)PostThreadMessage(threadId,ForgetMessage,0,0);
    }

    // The secure desktop (Win+L, Ctrl+Alt+Delete, UAC) or another session took the keyboard, so
    // releases of held keys never reach this hook. Forget every held key, stop routing and drop
    // the hook; the UI disarms control, which tells the Mac to release its keys and buttons.
    void Forget()
    {
        bool wasArmed;nint old;
        lock(sync){wasArmed=armed;armed=false;router.ForgetPhysicalState();old=hook;hook=0;}
        if(old!=0)UnhookWindowsHookEx(old);
        if(wasArmed)dispatcher.BeginInvoke(()=>InputLost?.Invoke());
    }
}
