param([string]$Executable=(Join-Path $PSScriptRoot '../artifacts/mac-lan.exe'))
$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
$smokePath=Join-Path $projectRoot ('.cache/standalone-smoke-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $smokePath | Out-Null
Copy-Item -LiteralPath $Executable -Destination (Join-Path $smokePath 'mac-lan.exe')
Set-Content -LiteralPath (Join-Path $smokePath 'keep.txt') -Value 'untouched'
foreach($mode in @('package-diagnostics','diagnostics')) {
    $outputPath=Join-Path $smokePath ($mode+'.json')
    $errorPath=Join-Path $smokePath ($mode+'-error.txt')
    $probe=Start-Process -FilePath (Join-Path $smokePath 'mac-lan.exe') -ArgumentList ('--'+$mode) -WorkingDirectory $smokePath -Wait -PassThru -WindowStyle Hidden -RedirectStandardOutput $outputPath -RedirectStandardError $errorPath
    if($probe.ExitCode -ne 0){throw "Standalone $mode failed: $(Get-Content -LiteralPath $errorPath -Raw)"}
    $data=Get-Content -LiteralPath $outputPath -Raw | ConvertFrom-Json
    if($data.uiStarted -or $data.hooksStarted -or $data.captureStarted){throw 'Diagnostic unexpectedly started UI/hooks/capture'}
    if($mode -eq 'package-diagnostics' -and (-not $data.helperPresent -or -not $data.viewerPresent -or -not $data.themePresent)){throw 'Embedded viewer/helper/theme is missing'}
    if($mode -eq 'diagnostics' -and $data.webview2 -like 'Unavailable:*'){throw 'WebView2 Runtime unavailable on this test machine'}
    $data | ConvertTo-Json -Depth 5
}
if((Get-Content -LiteralPath (Join-Path $smokePath 'keep.txt')).Trim() -ne 'untouched'){throw 'Neighboring file changed'}
Write-Output "Standalone checks passed; no UI/hooks/capture. Evidence: $smokePath"
