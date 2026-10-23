$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
# Build a public compatibility host and use those exact bytes in the app.
& "$PSScriptRoot/package-macos-preview.ps1" -PublicUpdate
$metadata=Get-Content -LiteralPath 'artifacts/update-public/macos-preview-build.json' -Raw | ConvertFrom-Json
$config=Get-Content -LiteralPath 'host/internal/updater/config.json' -Raw | ConvertFrom-Json
$root=Join-Path $PWD ('artifacts/macos-menu-'+[guid]::NewGuid().ToString('N'))
$bundle=Join-Path $root 'mac-lan.app'
$contents=Join-Path $bundle 'Contents'
$resources=Join-Path $contents 'Resources'
New-Item -ItemType Directory -Force -Path $resources,(Join-Path $contents 'MacOS'),(Join-Path $contents 'Helpers') | Out-Null
Copy-Item -LiteralPath (Join-Path $metadata.packageRoot 'MacDowHost') -Destination (Join-Path $contents 'MacOS/mac-lan')
Copy-Item -LiteralPath (Join-Path $metadata.packageRoot 'MacDowHost') -Destination (Join-Path $contents 'Helpers/MacDowHost')
Copy-Item -LiteralPath 'README.md','LICENSE','THIRD_PARTY_NOTICES.md' -Destination $resources
Copy-Item -LiteralPath 'licenses' -Destination $resources -Recurse
Copy-Item -LiteralPath (Join-Path $metadata.packageRoot 'BUILD-PROVENANCE.json'),(Join-Path $metadata.packageRoot 'COMMIT.txt') -Destination $resources
$provenance=Get-Content -LiteralPath (Join-Path $resources 'BUILD-PROVENANCE.json') -Raw | ConvertFrom-Json
$provenance.appBundle='menu-bar .app with cross-compiled Go launcher; AppKit compiled from embedded source on first launch, UNRUN'
$provenance | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $resources 'BUILD-PROVENANCE.json')
@{product='MAC-DOW';repositoryId=$config.repositoryId;version=$config.version;os='darwin';arch='arm64';layout='app-bundle';commit=$metadata.commit} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $resources 'MAC-DOW-INSTALL.json')
$plist=[IO.File]::ReadAllText((Join-Path $PWD 'native/Info.plist')).Replace('@NUMERIC_VERSION@',($config.version -split '-')[0])
[IO.File]::WriteAllText((Join-Path $contents 'Info.plist'),$plist,[Text.UTF8Encoding]::new($false))
python scripts/package-macos-app.py $bundle artifacts/update-public/mac-lan-macos-arm64.tar.gz
if($LASTEXITCODE){throw 'Mac app archive verification failed'}
# Alpha.14 strictly recognizes this existing alias. The signed feed uses it;
# the canonical manual download has the same bytes, not a separate update feed.
Copy-Item -LiteralPath 'artifacts/update-public/mac-lan-macos-arm64.tar.gz' -Destination 'artifacts/update-public/MAC-DOW-macos-arm64-unsigned.tar.gz' -Force
@{version=$config.version;commit=$metadata.commit;dirty=$metadata.dirty;nativeMacValidated=$false;captureSourceSha256=$metadata.captureSourceSha256;menuSourceSha256=$metadata.menuSourceSha256;packageRoot=$bundle;publicUpdate=$true;format='app-bundle'} | ConvertTo-Json | Set-Content -LiteralPath 'artifacts/update-public/macos-app-build.json'
Write-Output 'Mac menu-bar app packaged. Native AppKit compilation/execution remain first-launch checks on the Mac.'
