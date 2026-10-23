param(
    [Parameter(Mandatory=$true)][string]$WindowsArtifact,
    [Parameter(Mandatory=$true)][string]$MacArtifact,
    [Parameter(Mandatory=$true)][string]$ReleaseTag,
    [Parameter(Mandatory=$true)][string]$Commit,
    [Parameter(Mandatory=$true)][long]$ReleaseId,
    [Parameter(Mandatory=$true)][long]$WindowsAssetId,
    [Parameter(Mandatory=$true)][long]$MacAssetId,
    [string]$PrivateKey='.release-private/updates.ed25519',
    [string]$PreviousFeed,
    [string]$PublicKey,
    [string]$MacAppArtifact,
    [long]$MacAppAssetId,
    [string]$OutputDirectory='artifacts/update-feed'
)
$ErrorActionPreference='Stop'
$repoRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
# Resolve caller-supplied paths before switching the Go working directory.
$windowsPath=(Resolve-Path -LiteralPath $WindowsArtifact).Path
$macPath=(Resolve-Path -LiteralPath $MacArtifact).Path
$appPath=if($MacAppArtifact){(Resolve-Path -LiteralPath $MacAppArtifact).Path}else{$null}
if([bool]$appPath -ne ($MacAppAssetId -gt 0)){throw 'Mac app artifact and positive asset ID must be supplied together'}
$keyPath=(Resolve-Path -LiteralPath $PrivateKey).Path
$outputRoot=[IO.Path]::GetFullPath($OutputDirectory)
$previousPath=if($PreviousFeed){(Resolve-Path -LiteralPath $PreviousFeed).Path}else{$null}
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$payloadPath=Join-Path $outputRoot 'payload.json'
$feedPath=Join-Path $outputRoot 'feed.json'
foreach($outputPath in @($payloadPath,$feedPath)){
    if([string]::Equals($keyPath,$outputPath,[StringComparison]::OrdinalIgnoreCase)){throw 'Update output must never replace the private signing key'}
}
$savedEnvironment=@{}
foreach($name in @('GOOS','GOARCH','CGO_ENABLED','GOCACHE','GOMODCACHE','GOMAXPROCS')){$savedEnvironment[$name]=[Environment]::GetEnvironmentVariable($name,'Process')}
try {
    $env:GOOS=(go env GOHOSTOS).Trim()
    if($LASTEXITCODE){throw 'Cannot determine native Go operating system'}
    $env:GOARCH=(go env GOHOSTARCH).Trim()
    if($LASTEXITCODE){throw 'Cannot determine native Go architecture'}
    $env:CGO_ENABLED='0'
    $env:GOCACHE=Join-Path $repoRoot '.cache/go-build'
    $env:GOMODCACHE=Join-Path $repoRoot '.cache/go-mod'
    $env:GOMAXPROCS='2'
    $toolArguments=@('run','-C',(Join-Path $repoRoot 'host'),'./cmd/macdow-release')
    $manifestArguments=@('manifest','--windows',$windowsPath,'--mac',$macPath,'--tag',$ReleaseTag,'--commit',$Commit,'--release-id',"$ReleaseId",'--windows-id',"$WindowsAssetId",'--mac-id',"$MacAssetId",'--out',$payloadPath)
    if($appPath){$manifestArguments+=@('--mac-app',$appPath,'--mac-app-id',"$MacAppAssetId")}
    if($previousPath){$manifestArguments+=@('--previous',$previousPath)}
    if($PublicKey){$manifestArguments+=@('--public-key',$PublicKey)}
    & go @toolArguments @manifestArguments
    if($LASTEXITCODE){throw 'Update manifest preparation failed'}
    $signArguments=@('sign','--key',$keyPath,'--payload',$payloadPath,'--out',$feedPath)
    if($PublicKey){$signArguments+=@('--public-key',$PublicKey)}
    & go @toolArguments @signArguments
    if($LASTEXITCODE){throw 'Update feed signing failed'}
    $verifyArguments=@('verify','--feed',$feedPath)
    if($PublicKey){$verifyArguments+=@('--public-key',$PublicKey)}
    & go @toolArguments @verifyArguments
    if($LASTEXITCODE){throw 'Prepared update feed verification failed'}
    Write-Output "Prepared signed update feed locally: $feedPath"
    Write-Output 'No repository or release was created, changed, or published.'
} finally {
    foreach($entry in $savedEnvironment.GetEnumerator()){[Environment]::SetEnvironmentVariable($entry.Key,$entry.Value,'Process')}
}
