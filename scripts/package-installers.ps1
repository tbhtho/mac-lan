$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
# Wrap the already verified packages for first installs: a per-user Windows setup
# and a Mac disk image with an Applications link. Updates keep using the feed assets.
$config=Get-Content -LiteralPath 'host/internal/updater/config.json' -Raw | ConvertFrom-Json
$commit=(git rev-parse HEAD).Trim()
if($LASTEXITCODE){throw 'Cannot identify source commit'}
$dirty=[bool](git status --porcelain)
foreach($file in @('artifacts/standalone-build.json','artifacts/update-public/macos-app-build.json')){
    $build=Get-Content -LiteralPath $file -Raw | ConvertFrom-Json
    if($build.commit -ne $commit -or $build.version -ne $config.version -or $build.dirty -ne $dirty){throw "Package $file does not match this source commit; package Windows and Mac first"}
}
# Call python.exe explicitly: an extensionless 'python' earlier on PATH can shadow it in PowerShell.
$python=(Get-Command python.exe -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
$makensis=Get-Command makensis.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
$makensisPath=if($makensis){$makensis.Source}else{Join-Path ${env:ProgramFiles(x86)} 'NSIS\makensis.exe'}
if(-not (Test-Path -LiteralPath $makensisPath)){throw 'NSIS (makensis.exe) is required to build the Windows setup'}
$numeric=($config.version -replace '^(\d+\.\d+\.\d+)-[a-z]+\.(\d+)$','$1.$2')
if($numeric -notmatch '^\d+\.\d+\.\d+\.\d+$'){throw "Cannot derive a numeric version from $($config.version)"}
$exe=(Resolve-Path -LiteralPath 'artifacts/mac-lan.exe').Path
$setup=[IO.Path]::GetFullPath('artifacts/mac-lan-setup.exe')
Remove-Item -LiteralPath $setup -ErrorAction SilentlyContinue
& $makensisPath /V2 "/DVERSION=$($config.version)" "/DNUMERIC_VERSION=$numeric" "/DAPP_EXE=$exe" "/DOUT_FILE=$setup" 'installer/mac-lan-setup.nsi'
if($LASTEXITCODE -or -not (Test-Path -LiteralPath $setup)){throw 'Windows setup build failed'}
$dmg=[IO.Path]::GetFullPath('artifacts/update-public/mac-lan-macos-arm64.dmg')
& $python -I scripts/package-macos-dmg.py 'artifacts/update-public/mac-lan-macos-arm64.tar.gz' $dmg
if($LASTEXITCODE){throw 'Mac disk image build failed'}
@{version=$config.version;commit=$commit;dirty=$dirty;windowsSetup='mac-lan-setup.exe';macDiskImage='mac-lan-macos-arm64.dmg';macDiskImageFormat='ISO 9660 with Rock Ridge, written on Windows; macOS mounting is unverified until tested on a Mac'} | ConvertTo-Json | Set-Content -LiteralPath 'artifacts/installers-build.json'
Get-FileHash -LiteralPath $setup,$dmg -Algorithm SHA256 | Format-Table Hash,Path -AutoSize | Out-String -Width 200
