$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$env:DOTNET_CLI_HOME=Join-Path $PWD '.cache\dotnet'
$env:NUGET_PACKAGES=Join-Path $PWD '.cache\nuget'
$env:DOTNET_GENERATE_ASPNET_CERTIFICATE='false'
$env:DOTNET_CLI_TELEMETRY_OPTOUT='1'
$env:GOCACHE=Join-Path $PWD '.cache\go-build'
$env:GOMODCACHE=Join-Path $PWD '.cache\go-mod'
$env:GOMAXPROCS='2'
dotnet run --project tests/Core.Tests -c Release -p:BuildInParallel=false
if($LASTEXITCODE){throw 'Core tests failed'}
dotnet run --project tests/Session.Tests -c Release -p:BuildInParallel=false
if($LASTEXITCODE){throw 'Session protocol tests failed'}
dotnet run --project tests/Transfer.Tests -c Release -p:BuildInParallel=false
if($LASTEXITCODE){throw 'File transfer tests failed'}
dotnet run --project tests/Presentation.Tests -c Release -p:BuildInParallel=false
if($LASTEXITCODE){throw 'Presentation tests failed'}
node tests/viewer.test.mjs
if($LASTEXITCODE){throw 'Viewer tests failed'}
node tests/viewer-races.test.mjs
if($LASTEXITCODE){throw 'Viewer race tests failed'}
go test -C host -p 1 ./...
if($LASTEXITCODE){throw 'Host tests failed'}
go vet -C host ./...
if($LASTEXITCODE){throw 'Go vet failed'}
dotnet build src/Windows -c Release -m:1 -p:BuildInParallel=false -p:RestoreLockedMode=true
if($LASTEXITCODE){throw 'Windows compile failed'}
Write-Output 'Headless suite passed. No UI, capture, OS input hooks or injection started.'
