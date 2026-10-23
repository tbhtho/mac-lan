param(
    [Parameter(Mandatory=$true)][string]$NotesFile,
    [string]$PrivateKey='.release-private/updates.ed25519',
    [switch]$ValidateOnly
)
$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
function Invoke-Checked {
    param([string]$Command,[string[]]$Arguments)
    $result=& $Command @Arguments
    if($LASTEXITCODE){throw "$Command failed; publication stopped"}
    return $result
}
function Get-Releases {
    $json=(Invoke-Checked -Command gh -Arguments @('api',"repos/$repository/releases",'--paginate','--slurp')) -join "`n"
    # Windows PowerShell 5.1 keeps the slurped array whole; PowerShell 7 needs -NoEnumerate for the same result.
    $options=@{};if((Get-Command ConvertFrom-Json).Parameters.ContainsKey('NoEnumerate')){$options.NoEnumerate=$true}
    $pages=$json | ConvertFrom-Json @options
    foreach($page in $pages){foreach($entry in $page){$entry}}
}
$config=Get-Content -LiteralPath 'host/internal/updater/config.json' -Raw | ConvertFrom-Json
if($config.repository -ne 'tbhtho/mac-lan' -or $config.repositoryId -ne 1430459188){throw 'Unexpected release repository identity'}
$repository=$config.repository
$version=$config.version
$tag="v$version"
$commit=(Invoke-Checked -Command git -Arguments @('rev-parse','HEAD')).Trim()
if(Invoke-Checked -Command git -Arguments @('status','--porcelain')){throw 'Commit source changes before packaging and publishing'}
$notesPath=(Resolve-Path -LiteralPath $NotesFile).Path
$keyPath=(Resolve-Path -LiteralPath $PrivateKey).Path
$windowsPath=(Resolve-Path -LiteralPath 'artifacts/mac-lan.exe').Path
$macPath=(Resolve-Path -LiteralPath 'artifacts/update-public/mac-lan-macos-arm64-commandline-preview.tar.gz').Path
$appPath=(Resolve-Path -LiteralPath 'artifacts/update-public/mac-lan-macos-arm64.tar.gz').Path
$appAliasPath=(Resolve-Path -LiteralPath 'artifacts/update-public/MAC-DOW-macos-arm64-unsigned.tar.gz').Path
# First-install wrappers around the same packages; the signed feed never lists them.
$setupPath=(Resolve-Path -LiteralPath 'artifacts/mac-lan-setup.exe').Path
$dmgPath=(Resolve-Path -LiteralPath 'artifacts/update-public/mac-lan-macos-arm64.dmg').Path
$windowsBuild=Get-Content -LiteralPath 'artifacts/standalone-build.json' -Raw | ConvertFrom-Json
$macBuild=Get-Content -LiteralPath 'artifacts/update-public/macos-preview-build.json' -Raw | ConvertFrom-Json
$appBuild=Get-Content -LiteralPath 'artifacts/update-public/macos-app-build.json' -Raw | ConvertFrom-Json
$installerBuild=Get-Content -LiteralPath 'artifacts/installers-build.json' -Raw | ConvertFrom-Json
foreach($build in @($windowsBuild,$macBuild,$appBuild,$installerBuild)){
    if($build.commit -ne $commit -or $build.version -ne $version -or $build.dirty -ne $false){throw 'Build metadata must match the clean current source commit and version'}
}
if($macBuild.publicUpdate -ne $true -or $appBuild.publicUpdate -ne $true){throw 'Only public Mac packages may be published'}
if($appBuild.signature -ne 'certificate'){throw 'The Mac app must be signed with the mac-lan certificate so privacy grants survive updates'}
if((Get-FileHash -LiteralPath $appPath).Hash -ne (Get-FileHash -LiteralPath $appAliasPath).Hash){throw 'Mac app compatibility alias differs from canonical download'}
Invoke-Checked -Command python -Arguments @('scripts/verify-release-packages.py','--windows',$windowsPath,'--mac',$macPath,'--mac-app',$appPath,'--commit',$commit,'--version',$version)
$previous=if(Test-Path -LiteralPath 'updates/feed.json'){(Resolve-Path -LiteralPath 'updates/feed.json').Path}else{$null}
$feedArguments=@{WindowsArtifact=$windowsPath;MacArtifact=$macPath;MacAppArtifact=$appAliasPath;ReleaseTag=$tag;Commit=$commit;PrivateKey=$keyPath}
if($previous){$feedArguments.PreviousFeed=$previous}
# Validate the packages and signing key before creating any remote objects.
& "$PSScriptRoot/prepare-update-feed.ps1" @feedArguments -ReleaseId 1 -WindowsAssetId 2 -MacAssetId 3 -MacAppAssetId 4 -OutputDirectory 'artifacts/update-feed-review'
$sourcePath=[IO.Path]::GetFullPath("artifacts/mac-lan-source-$version.zip")
$sumsPath=[IO.Path]::GetFullPath("artifacts/SHA256SUMS-$version.txt")
Invoke-Checked -Command git -Arguments @('archive','--format=zip',"--output=$sourcePath",$commit) | Out-Null
$releasePaths=@($windowsPath,$setupPath,$macPath,$appPath,$dmgPath,$appAliasPath,$sourcePath,$sumsPath)
$sumLines=foreach($path in @($windowsPath,$setupPath,$macPath,$appPath,$dmgPath,$appAliasPath,$sourcePath)){
    '{0}  {1}' -f (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant(),[IO.Path]::GetFileName($path)
}
[IO.File]::WriteAllText($sumsPath,($sumLines -join "`n")+"`n",[Text.UTF8Encoding]::new($false))
if($ValidateOnly){Write-Output "Validated $tag at $commit; no remote changes made.";return}
$remote=Invoke-Checked -Command gh -Arguments @('api',"repos/$repository") | ConvertFrom-Json
if($remote.id -ne $config.repositoryId -or $remote.private){throw 'The intended existing repository must already be public'}
$remoteHead=(Invoke-Checked -Command git -Arguments @('ls-remote','origin','refs/heads/main')) -split '\s+'
if($remoteHead[0] -ne $commit){throw 'Push the packaged source commit to main before publishing'}
$releases=@(Get-Releases)
if($releases | Where-Object tag_name -eq $tag){throw 'This release tag already exists; do not replace immutable release assets'}
Invoke-Checked -Command gh -Arguments (@('release','create',$tag,'--repo',$repository,'--target',$commit,'--draft','--prerelease','--title',"mac-lan $version",'--notes-file',$notesPath)+$releasePaths) | Out-Null
$release=Get-Releases | Where-Object tag_name -eq $tag
if(@($release).Count -ne 1 -or -not $release.draft -or $release.target_commitish -ne $commit){throw 'Draft release identity mismatch; it has been left unpublished'}
$assets=@($release.assets)
$windowsAsset=@($assets | Where-Object name -eq ([IO.Path]::GetFileName($windowsPath)))
$macAsset=@($assets | Where-Object name -eq ([IO.Path]::GetFileName($macPath)))
$appAsset=@($assets | Where-Object name -eq ([IO.Path]::GetFileName($appAliasPath)))
if($assets.Count -ne $releasePaths.Count -or $windowsAsset.Count -ne 1 -or $macAsset.Count -ne 1 -or $appAsset.Count -ne 1){throw 'Draft release asset names differ from the intended packages'}
foreach($path in $releasePaths){
    $asset=@($assets | Where-Object name -eq ([IO.Path]::GetFileName($path)))
    $hash='sha256:'+(Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if($asset.Count -ne 1 -or $asset[0].size -ne (Get-Item -LiteralPath $path).Length -or $asset[0].digest -ne $hash){throw 'Uploaded asset size or digest mismatch; draft left unpublished'}
}
& "$PSScriptRoot/prepare-update-feed.ps1" @feedArguments -ReleaseId $release.id -WindowsAssetId $windowsAsset[0].id -MacAssetId $macAsset[0].id -MacAppAssetId $appAsset[0].id -OutputDirectory 'artifacts/update-feed'
if((Invoke-Checked -Command git -Arguments @('rev-parse','HEAD')).Trim() -ne $commit -or (Invoke-Checked -Command git -Arguments @('status','--porcelain'))){throw 'Source changed during publication; draft left unpublished'}
Invoke-Checked -Command gh -Arguments @('release','edit',$tag,'--repo',$repository,'--draft=false') | Out-Null
# Advertise only a complete, published release. The feed commit follows its binary source commit.
New-Item -ItemType Directory -Force -Path 'updates' | Out-Null
Copy-Item -LiteralPath 'artifacts/update-feed/feed.json' -Destination 'updates/feed.json'
Invoke-Checked -Command git -Arguments @('add','--','updates/feed.json') | Out-Null
Invoke-Checked -Command git -Arguments @('commit','-m',"Publish signed update feed for $version",'--','updates/feed.json') | Out-Null
Invoke-Checked -Command git -Arguments @('push','origin','HEAD:main') | Out-Null
Write-Output "Published https://github.com/$repository/releases/tag/$tag and its signed update feed."
