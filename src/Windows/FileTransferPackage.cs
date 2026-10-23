using System.IO;
using System.IO.Compression;
using System.Text;

namespace MacDow.Windows;

// Files stay on disk throughout preparation and upload. Only folder transfers need a temporary archive.
internal sealed class FileTransferPackage : IDisposable
{
    internal const long MaxBytes=100L*1024*1024*1024;
    readonly string path;
    readonly bool temporary;
    bool disposed;
    public string Name {get;}
    public string Kind {get;}
    public long Length {get;}
    FileTransferPackage(string path,string name,string kind,long length,bool temporary)
    {this.path=path;Name=name;Kind=kind;Length=length;this.temporary=temporary;}
    internal static void ValidateName(string name)
    {
        if(string.IsNullOrWhiteSpace(name)||name is "." or ".."||Encoding.UTF8.GetByteCount(name)>240||name.Any(c=>char.IsControl(c)||c is '/' or '\\'))
            throw new InvalidDataException("Choose a file or folder with a normal name of up to 240 UTF-8 bytes.");
    }
    static FileAttributes Attributes(string path)
    {
        var attributes=File.GetAttributes(path);
        if((attributes&FileAttributes.ReparsePoint)!=0)throw new InvalidDataException("Shortcuts, symbolic links and linked folders cannot be sent. Choose the original files instead.");
        return attributes;
    }
    static void ValidateAncestors(string path)
    {for(string? current=Path.GetFullPath(path);current!=null;current=Path.GetDirectoryName(current))Attributes(current);}
    public static FileTransferPackage CreateFile(string path)
    {
        path=Path.GetFullPath(path);string name=Path.GetFileName(path);ValidateName(name);
        ValidateAncestors(path);if((Attributes(path)&FileAttributes.Directory)!=0)throw new InvalidDataException("Choose Add folder to send this folder.");
        long length=new FileInfo(path).Length;
        if(length>MaxBytes)throw new InvalidDataException("Each transfer must be 100 GB or smaller.");
        return new(path,name,"file",length,false);
    }
    public static Task<FileTransferPackage> CreateFolderAsync(string path,IProgress<string>? progress,CancellationToken token)=>Task.Run(async()=>
    {
        path=Path.TrimEndingDirectorySeparator(Path.GetFullPath(path));string name=Path.GetFileName(path);ValidateName(name);
        ValidateAncestors(path);if((Attributes(path)&FileAttributes.Directory)==0)throw new InvalidDataException("The folder is no longer available.");
        string archive=Path.Combine(Path.GetTempPath(),"MAC-DOW-"+Guid.NewGuid().ToString("N")+".zip");
        try
        {
            var entries=new List<(string Path,string Relative,bool Directory,long Length)>();long total=0,actualTotal=0;
            void Visit(string directory,string prefix,int depth)
            {
                token.ThrowIfCancellationRequested();
                if(depth>32)throw new InvalidDataException("This folder has more than 32 levels. Send a smaller folder instead.");
                Attributes(directory);
                foreach(string child in Directory.EnumerateFileSystemEntries(directory))
                {
                    token.ThrowIfCancellationRequested();string basename=Path.GetFileName(child);ValidateName(basename);
                    string relative=prefix+basename;bool isDirectory=(Attributes(child)&FileAttributes.Directory)!=0;
                    if(relative.Split('/').Length>32)throw new InvalidDataException("This folder has more than 32 levels. Send a smaller folder instead.");
                    if(entries.Count>=10000)throw new InvalidDataException("A folder transfer can contain up to 10,000 items. Send a smaller folder instead.");
                    long length=isDirectory?0:new FileInfo(child).Length;entries.Add((child,relative,isDirectory,length));
                    if(isDirectory)Visit(child,relative+"/",depth+1);
                    else{total=checked(total+length);if(total>MaxBytes)throw new InvalidDataException("Each transfer must be 100 GB or smaller.");}
                }
            }
            progress?.Report("Checking folder...");Visit(path,"",0);
            using(var destination=new FileStream(archive,FileMode.CreateNew,FileAccess.Write,FileShare.None,256*1024,FileOptions.Asynchronous|FileOptions.SequentialScan))
            using(var zip=new ZipArchive(destination,ZipArchiveMode.Create,true))
            {
                int completed=0;byte[] buffer=new byte[256*1024];
                foreach(var entry in entries)
                {
                    token.ThrowIfCancellationRequested();ValidateAncestors(entry.Path);
                    var item=zip.CreateEntry(entry.Relative+(entry.Directory?"/":""),CompressionLevel.NoCompression);
                    if(!entry.Directory)
                    {
                        await using var input=new FileStream(entry.Path,FileMode.Open,FileAccess.Read,FileShare.Read,256*1024,FileOptions.Asynchronous|FileOptions.SequentialScan);
                        if(input.Length!=entry.Length)throw new IOException("A file changed while preparing the folder. Retry when it has finished saving.");
                        await using var output=item.Open();long copied=0;
                        while(true)
                        {
                            int count=await input.ReadAsync(buffer,token);if(count==0)break;
                            copied+=count;actualTotal+=count;
                            if(copied>entry.Length||actualTotal>MaxBytes)throw new IOException("A file changed while preparing the folder. Retry when it has finished saving.");
                            await output.WriteAsync(buffer.AsMemory(0,count),token);
                        }
                        if(copied!=entry.Length)throw new IOException("A file changed while preparing the folder. Retry when it has finished saving.");
                    }
                    completed++;if(completed==entries.Count||completed%16==0)progress?.Report($"Preparing folder: {completed:N0} of {entries.Count:N0} items");
                    if(destination.Length>MaxBytes)throw new InvalidDataException("The prepared folder exceeds the 100 GB transfer limit.");
                }
            }
            token.ThrowIfCancellationRequested();long length=new FileInfo(archive).Length;
            if(length>MaxBytes)throw new InvalidDataException("The prepared folder exceeds the 100 GB transfer limit.");
            return new FileTransferPackage(archive,name,"folder",length,true);
        }
        catch{try{File.Delete(archive);}catch(IOException){}catch(UnauthorizedAccessException){}throw;}
    },token);
    public Stream OpenRead()
    {
        ObjectDisposedException.ThrowIf(disposed,this);ValidateAncestors(path);
        var stream=new FileStream(path,FileMode.Open,FileAccess.Read,FileShare.Read,256*1024,FileOptions.Asynchronous|FileOptions.SequentialScan);
        if(stream.Length!=Length){stream.Dispose();throw new IOException("The file changed after it was selected. Add it again and retry.");}
        return stream;
    }
    public void Dispose()
    {
        if(disposed)return;disposed=true;
        if(temporary)try{File.Delete(path);}catch(IOException){}catch(UnauthorizedAccessException){}
    }
}
