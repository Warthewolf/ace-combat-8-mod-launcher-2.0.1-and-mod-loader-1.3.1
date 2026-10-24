# Builds ACE8 Mod Launcher for Windows and Linux into dist\. Needs Go 1.24 or newer.
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = "0"; $env:GOARCH = "amd64"
$env:GOOS = "windows"; go build -trimpath -ldflags "-H windowsgui" -o dist\ACE8ModLauncher.exe .
$env:GOOS = "linux";   go build -tags linuxapp -trimpath -ldflags "-s -w" -o dist\ACE8ModLauncher .
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
Write-Host "Built dist\ACE8ModLauncher.exe (Windows) and dist\ACE8ModLauncher (Linux)"
