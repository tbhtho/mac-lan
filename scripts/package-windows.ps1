$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$env:DOTNET_CLI_HOME=Join-Path $PWD '.cache\dotnet'
$env:NUGET_PACKAGES=Join-Path $PWD '.cache\nuget'
$env:DOTNET_GENERATE_ASPNET_CERTIFICATE='false'
$env:DOTNET_CLI_TELEMETRY_OPTOUT='1'
dotnet publish src/Windows -c Release -r win-x64 --self-contained true -o artifacts/windows-x64 -m:1 -p:BuildInParallel=false -p:PublishSingleFile=false -p:RestoreLockedMode=true
if($LASTEXITCODE){throw 'Windows publish failed'}
$env:GOCACHE=Join-Path $PWD '.cache\go-build'
$env:GOMODCACHE=Join-Path $PWD '.cache\go-mod'
$env:GOMAXPROCS='2'
$env:CGO_ENABLED='0'
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -C host -trimpath -o ../artifacts/windows-x64/MAC-DOW-Update.exe ./cmd/macdow-update
if($LASTEXITCODE){throw 'Updater publish failed'}
$config=Get-Content host/internal/updater/config.json -Raw | ConvertFrom-Json
$marker=@{product='MAC-DOW';repositoryId=$config.repositoryId;version=$config.version;os='windows';arch='amd64'} | ConvertTo-Json
[IO.File]::WriteAllText((Join-Path $PWD 'artifacts/windows-x64/MAC-DOW-INSTALL.json'),$marker,(New-Object Text.UTF8Encoding($false)))
Copy-Item README.md,LICENSE,THIRD_PARTY_NOTICES.md -Destination artifacts/windows-x64/
Copy-Item -Recurse -LiteralPath docs -Destination artifacts/windows-x64/ -Force
Copy-Item -Recurse -LiteralPath licenses -Destination artifacts/windows-x64/ -Force
Copy-Item -LiteralPath 'C:\Program Files\dotnet\LICENSE.txt' -Destination artifacts/windows-x64/licenses/dotnet-LICENSE.txt
Copy-Item -LiteralPath 'C:\Program Files\dotnet\ThirdPartyNotices.txt' -Destination artifacts/windows-x64/licenses/dotnet-ThirdPartyNotices.txt
$commit=git rev-parse HEAD
if($LASTEXITCODE){throw 'Commit source before packaging'}
$commit | Set-Content artifacts/windows-x64/COMMIT.txt -Encoding ascii
Compress-Archive -Path artifacts/windows-x64/* -DestinationPath artifacts/MAC-DOW-windows-x64-unsigned.zip -Force
Get-FileHash artifacts/MAC-DOW-windows-x64-unsigned.zip -Algorithm SHA256
