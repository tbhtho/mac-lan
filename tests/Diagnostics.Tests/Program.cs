using System.Text.Json;
using MacDow.Windows;

int checks=0;
void Check(bool value,string message){if(!value)throw new Exception(message);checks++;Console.WriteLine("PASS "+message);}
string root=Path.Combine(Path.GetTempPath(),"mac-lan-diagnostics-test-"+Guid.NewGuid().ToString("N"));
Directory.CreateDirectory(root);
try
{
    string basic=Path.Combine(root,"basic");
    await using(var log=new DiagnosticLog(basic,"test-preview"))
    {
        log.Record("session-ended","remote-close",new{closeStatus=1001,lastVideoAgeMs=750},"connection-a");
    }
    string path=Path.Combine(basic,"windows-local.jsonl");
    Check(File.Exists(path),"a disconnect is persisted to a Windows JSONL file before shutdown finishes");
    using(var doc=JsonDocument.Parse(File.ReadAllLines(path).Single()))
    {
        var row=doc.RootElement;
        Check(row.GetProperty("version").GetString()=="test-preview"&&row.GetProperty("role").GetString()=="windows","records identify the application version and Windows role");
        Check(row.GetProperty("session").GetString()=="connection-a"&&row.GetProperty("run").GetString()!.Length>0,"disconnect records retain connection and process-run correlation");
        Check(row.GetProperty("state").GetProperty("lastVideoAgeMs").GetInt32()==750&&row.GetProperty("code").GetString()=="remote-close","disconnect timing and transport reason survive serialization");
        Check(row.GetProperty("uptimeMs").GetDouble()>=0&&row.GetProperty("time").GetDateTimeOffset().Offset==TimeSpan.Zero,"records include monotonic elapsed time and UTC time");
    }
    string rotating=Path.Combine(root,"rotating");
    await using(var log=new DiagnosticLog(rotating,"test",maxBytes:1100,retainedFiles:3))
    {
        for(int i=0;i<24;i++)log.Record("health","ok",new{sample=i},"connection-b");
    }
    var files=Directory.GetFiles(rotating);
    Check(files.Length==3&&files.All(f=>new FileInfo(f).Length<=1100),"rotation bounds total retained files and file sizes");
    using(var doc=JsonDocument.Parse(File.ReadAllLines(Path.Combine(rotating,"windows-local.jsonl")).Last()))
        Check(doc.RootElement.GetProperty("state").GetProperty("sample").GetInt32()==23,"rotation keeps the newest diagnostic evidence");
    await using(var log=new DiagnosticLog(Path.Combine(root,"parallel"),"test"))
        Parallel.For(0,100,i=>log.Record("parallel","ok",new{sample=i}));
    var parallel=File.ReadAllLines(Path.Combine(root,"parallel","windows-local.jsonl"));
    Check(parallel.Length==100&&parallel.Select(line=>JsonDocument.Parse(line).RootElement.GetProperty("state").GetProperty("sample").GetInt32()).Distinct().Count()==100,"concurrent producers preserve complete, non-interleaved records");
    string blocked=Path.Combine(root,"not-a-directory");File.WriteAllText(blocked,"preserve me");
    await using(var log=new DiagnosticLog(blocked,"test")){log.Record("session-ended","remote-close");}
    Check(File.ReadAllText(blocked)=="preserve me","an unwritable diagnostics destination does not crash or replace user files");
    string fault=Path.Combine(root,"fault");
    await using(var log=new DiagnosticLog(fault,"test"))
        log.Record("transport-failed","receive",error:new IOException("SECRET-TOKEN clipboard text /Users/private-name"));
    string faultText=File.ReadAllText(Path.Combine(fault,"windows-local.jsonl"));
    Check(faultText.Contains("IOException")&&faultText.Contains("hresult")&&!faultText.Contains("SECRET-TOKEN")&&!faultText.Contains("private-name")&&!faultText.Contains("clipboard text"),"exception diagnostics retain type and error code without arbitrary exception contents");
    using(var message=JsonDocument.Parse("{\"type\":\"controlStatus\",\"code\":\"SECRET-TOKEN\",\"controlPermission\":true,\"active\":false,\"message\":\"clipboard text\",\"width\":1920,\"codec\":\"PRIVATE-DOCUMENT\",\"png\":\"PRIVATE-PIXELS\"}"))
    {
        string safe=JsonSerializer.Serialize(DiagnosticHostFields.Select(message.RootElement));
        Check(safe.Contains("1920")&&!safe.Contains("PRIVATE-")&&!safe.Contains("SECRET-TOKEN")&&!safe.Contains("clipboard"),"host metadata selection keeps dimensions and flags while excluding unknown text and pixels");
        Check(DiagnosticHostFields.Code(message.RootElement)=="unknown-control-state","unknown native control codes cannot carry arbitrary text into the log");
    }
    using(var message=JsonDocument.Parse("{\"fps\":59.5,\"queueFrames\":5,\"encodeMs\":2,\"text\":\"private contents\"}"))
    {
        var fields=DiagnosticHostFields.Viewer(message.RootElement);
        Check(fields.Count==3&&fields["fps"]==59.5&&fields["queueFrames"]==5,"viewer health retains numeric frame rate and backlog without arbitrary payload fields");
    }
    Console.WriteLine($"{checks} diagnostic checks passed; temporary local files only.");
}
finally{Directory.Delete(root,true);}
