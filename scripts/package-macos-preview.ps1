param([switch]$PublicUpdate,[switch]$PrivateBootstrap)
if($PublicUpdate -and $PrivateBootstrap){throw 'Choose a public build or a private bootstrap, not both'}
$PublicUpdate=-not $PrivateBootstrap
$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$env:GOCACHE=Join-Path $PWD '.cache/go-build'
$env:GOMODCACHE=Join-Path $PWD '.cache/go-mod'
$env:GOMAXPROCS='2'
$env:CGO_ENABLED='0'
$env:GOOS='darwin'
$env:GOARCH='arm64'
$diagnosticDestination=$env:MACDOW_DIAGNOSTICS_WEBHOOK
if(-not $diagnosticDestination -and (Test-Path -LiteralPath '.cache/discord-webhook.txt')){$diagnosticDestination=[IO.File]::ReadAllText((Join-Path $PWD '.cache/discord-webhook.txt')).Trim()}
if(-not $PublicUpdate -and $diagnosticDestination -notmatch '^https://discord\.com/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]+$'){throw 'Private Mac diagnostic destination missing or invalid'}
$outputRoot=if($PublicUpdate){Join-Path $PWD 'artifacts/update-public'}else{Join-Path $PWD 'artifacts/private-bootstrap'}
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$captureSource=[IO.File]::ReadAllBytes((Join-Path $PWD 'native/MacDowCapture.swift'))
$sourceHash=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($captureSource)).ToLowerInvariant()
$compressedSource=New-Object IO.MemoryStream
$compressor=New-Object IO.Compression.GZipStream($compressedSource,[IO.Compression.CompressionLevel]::SmallestSize,$true)
try{$compressor.Write($captureSource,0,$captureSource.Length)}finally{$compressor.Dispose()}
$encodedSource=[Convert]::ToBase64String($compressedSource.ToArray())
$compressedSource.Dispose()
if($encodedSource.Length -gt 24000){throw 'Embedded native source exceeds bounded linker configuration'}
[byte[]]$menuSource=[IO.File]::ReadAllBytes((Join-Path $PWD 'native/HostUpdater.swift')) + [Text.Encoding]::UTF8.GetBytes("`n") + [IO.File]::ReadAllBytes((Join-Path $PWD 'native/MacDowHostApp.swift'))
$menuHash=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($menuSource)).ToLowerInvariant()
$menuStream=[IO.MemoryStream]::new()
$menuCompressor=[IO.Compression.GZipStream]::new($menuStream,[IO.Compression.CompressionLevel]::SmallestSize,$true)
try{$menuCompressor.Write($menuSource,0,$menuSource.Length)}finally{$menuCompressor.Dispose()}
$encodedMenu=[Convert]::ToBase64String($menuStream.ToArray());$menuStream.Dispose()
$encodedInfo=[Convert]::ToBase64String([IO.File]::ReadAllBytes((Join-Path $PWD 'native/Info.plist')))
$config=Get-Content -LiteralPath 'host/internal/updater/config.json' -Raw | ConvertFrom-Json
$sourceCommit=(git rev-parse HEAD).Trim()
if($LASTEXITCODE){throw 'Cannot identify source commit'}
$isDirty=[bool](git status --porcelain)
$packageRoot=Join-Path $PWD ('artifacts/macos-preview-'+[guid]::NewGuid().ToString('N'))
$macRoot=Join-Path $packageRoot 'macos-arm64'
New-Item -ItemType Directory -Force -Path $macRoot | Out-Null
$linkFlags="-s -w -X main.NativeCaptureSourceGzip=$encodedSource -X main.NativeCaptureSourceSHA256=$sourceHash"
$linkFlags+=" -X main.NativeMenuSourceGzip=$encodedMenu -X main.NativeMenuSourceSHA256=$menuHash -X main.NativeMenuInfoBase64=$encodedInfo"
if(-not $PublicUpdate){$linkFlags+=" -X github.com/tbhtho/MAC-DOW/host/internal/diagnostics.Webhook=$diagnosticDestination"}
if($linkFlags.Length -gt 29000){throw 'Embedded source linker options exceed the Windows command-line budget'}
go build -C host -trimpath -ldflags $linkFlags -o (Join-Path $macRoot 'MacDowHost') .
if($LASTEXITCODE){throw 'Mac host cross-build failed'}
Copy-Item -LiteralPath 'README.md','LICENSE','THIRD_PARTY_NOTICES.md' -Destination $macRoot
Copy-Item -LiteralPath 'licenses' -Destination $macRoot -Recurse
[IO.File]::WriteAllText((Join-Path $macRoot 'COMMIT.txt'),$sourceCommit)
@{product='MAC-DOW';repositoryId=$config.repositoryId;version=$config.version;os='darwin';arch='arm64';layout='commandline-preview';commit=$sourceCommit} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $macRoot 'MAC-DOW-INSTALL.json')
@{hostCommit=$sourceCommit;dirty=$isDirty;host='cross-compiled Windows to darwin/arm64, UNRUN';nativeHelper='built on first Mac launch with existing Apple command-line tools';captureSourceSha256=$sourceHash;menuSourceSha256=$menuHash;nativeMacValidated=$false;realCapture='unverified';appBundle='installs menu-bar app on normal launch';publicUpdate=[bool]$PublicUpdate;diagnosticConfig=if($PublicUpdate){'private per-user configuration; no embedded destination'}else{'private bootstrap persists its destination for later updates'}} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $macRoot 'BUILD-PROVENANCE.json')
# Inspect privately; do not print linker settings containing the destination.
$binaryText=[Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes((Join-Path $macRoot 'MacDowHost')))
if(-not $binaryText.Contains($encodedSource) -or -not $binaryText.Contains($sourceHash) -or -not $binaryText.Contains($encodedMenu) -or -not $binaryText.Contains($menuHash) -or -not $binaryText.Contains($encodedInfo)){throw 'Mac embedded build configuration missing'}
if($PublicUpdate){if($diagnosticDestination -and $binaryText.Contains($diagnosticDestination)){throw 'Public Mac update contains the private diagnostic destination'}}
elseif(-not $binaryText.Contains($diagnosticDestination)){throw 'Private bootstrap diagnostic configuration missing'}
python scripts/package-macos-preview.py $macRoot (Join-Path $outputRoot 'mac-lan-macos-arm64-commandline-preview.tar.gz')
if($LASTEXITCODE){throw 'Mac archive verification failed'}
@{version=$config.version;commit=$sourceCommit;dirty=$isDirty;nativeMacValidated=$false;captureSourceSha256=$sourceHash;menuSourceSha256=$menuHash;packageRoot=$macRoot;publicUpdate=[bool]$PublicUpdate} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $outputRoot 'macos-preview-build.json')
Write-Output "Mac preview packaged with verified embedded source $sourceHash. Native build and 60 fps still require validation on the Mac."
