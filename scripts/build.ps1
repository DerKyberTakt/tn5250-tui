# Build tn5250-tui for the local OS/arch into dist\, version from `git describe`.
$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$ver = (& git describe --tags --always --dirty 2>$null)
if (-not $ver) { $ver = 'dev' }
$goos = (& go env GOOS).Trim()
$exe = 'tn5250-tui'
if ($goos -eq 'windows') { $exe = 'tn5250-tui.exe' }
New-Item -ItemType Directory -Force -Path dist | Out-Null
$env:CGO_ENABLED = '0'
& go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "dist/$exe" ./cmd/tn5250-tui
if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }
Write-Host "built dist/$exe ($ver)"
