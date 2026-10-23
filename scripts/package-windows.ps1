$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$env:DOTNET_CLI_HOME=Join-Path $PWD '.cache\dotnet'
$env:NUGET_PACKAGES=Join-Path $PWD '.cache\nuget'
$env:DOTNET_GENERATE_ASPNET_CERTIFICATE='false'
$env:DOTNET_CLI_TELEMETRY_OPTOUT='1'
$env:GOCACHE=Join-Path $PWD '.cache\go-build'
$env:GOMODCACHE=Join-Path $PWD '.cache\go-mod'
$env:GOMAXPROCS='2'
$env:CGO_ENABLED='0'
$env:GOOS='windows'
$env:GOARCH='amd64'
New-Item -ItemType Directory -Force -Path '.cache/package-input' | Out-Null
Copy-Item -LiteralPath 'C:\Program Files\dotnet\LICENSE.txt' -Destination '.cache/package-input/dotnet-LICENSE.txt'
Copy-Item -LiteralPath 'C:\Program Files\dotnet\ThirdPartyNotices.txt' -Destination '.cache/package-input/dotnet-ThirdPartyNotices.txt'
go build -C host -trimpath -ldflags '-s -w' -o ../.cache/package-input/MAC-DOW-Update.exe ./cmd/macdow-update
if($LASTEXITCODE){throw 'Bundled updater build failed'}
$packagePath=Join-Path $PWD ('artifacts/standalone-windows-'+[guid]::NewGuid().ToString('N'))
dotnet publish src/Windows -c Release -r win-x64 --self-contained true -o $packagePath -m:1 -p:BuildInParallel=false -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -p:DebugType=embedded -p:PublishDocumentationFiles=false -p:PublishReferencesDocumentationFiles=false -p:RestoreLockedMode=true
if($LASTEXITCODE){throw 'Single EXE publish failed'}
$config=Get-Content host/internal/updater/config.json -Raw | ConvertFrom-Json
$sourceCommit=git rev-parse HEAD
if($LASTEXITCODE){throw 'Cannot identify source commit'}
$isDirty=[bool](git status --porcelain)
$identity=@{product='MAC-DOW';repositoryId=$config.repositoryId;version=$config.version;os='windows';arch='amd64';commit=$sourceCommit;layout='single-exe';dirty=$isDirty} | ConvertTo-Json -Compress
$packageExe=Join-Path $packagePath 'MAC-DOW.exe'
$identityBytes=[Text.Encoding]::UTF8.GetBytes($identity)
$stream=[IO.File]::Open($packageExe,[IO.FileMode]::Append,[IO.FileAccess]::Write)
try {
    $stream.Write($identityBytes,0,$identityBytes.Length)
    $lengthBytes=[BitConverter]::GetBytes([uint32]$identityBytes.Length)
    $stream.Write($lengthBytes,0,$lengthBytes.Length)
    $magicBytes=[Text.Encoding]::ASCII.GetBytes('MCDOWPK1')
    $stream.Write($magicBytes,0,$magicBytes.Length)
} finally {$stream.Dispose()}
$visibleFiles=@(Get-ChildItem -LiteralPath $packagePath -File -Recurse)
if($visibleFiles.Count -ne 1 -or $visibleFiles[0].Name -ne 'MAC-DOW.exe'){throw 'Package must contain exactly one EXE with no loose dependencies'}
Copy-Item -LiteralPath $packageExe -Destination 'artifacts/MAC-DOW.exe' -Force
@{version=$config.version;commit=$sourceCommit;dirty=$isDirty;format='single-exe';nativeMacValidated=$false} | ConvertTo-Json | Set-Content -LiteralPath 'artifacts/standalone-build.json'
Get-FileHash -LiteralPath 'artifacts/MAC-DOW.exe' -Algorithm SHA256
Write-Output 'Single EXE: artifacts/MAC-DOW.exe. Existing ZIP/old builds were preserved. Dirty builds are local candidates, not published releases.'
