# tn5250-tui installer for Windows (PowerShell 5.1+).
#   iwr -useb https://raw.githubusercontent.com/DerKyberTakt/tn5250-tui/main/install.ps1 | iex
# Environment:
#   TN5250_TUI_VERSION  pin a release (for example v0.1.0); default = latest

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

function Install-Tn5250Tui {
    $repo = 'DerKyberTakt/tn5250-tui'
    $bin = 'tn5250-tui'

    # --- platform ---
    $archName = $env:PROCESSOR_ARCHITEW6432
    if (-not $archName) { $archName = $env:PROCESSOR_ARCHITECTURE }
    switch ($archName) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "Unsupported CPU: $archName" }
    }

    # --- version ---
    $tag = $env:TN5250_TUI_VERSION
    if (-not $tag) {
        Write-Host 'Looking up the latest release ...'
        $rel = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'tn5250-tui-installer' }
        $tag = $rel.tag_name
    }
    if (-not $tag) { throw 'Could not find the latest release tag (no release published yet?).' }
    if (-not $tag.StartsWith('v')) { $tag = "v$tag" }
    $ver = $tag.Substring(1)

    $asset = "${bin}_${ver}_windows_${arch}.zip"
    $base = "https://github.com/$repo/releases/download/$tag"
    $dest = Join-Path $env:LOCALAPPDATA "Programs\$bin"

    # --- download + verify ---
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("tn5250-tui-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading $asset ($tag) ..."
        $zip = Join-Path $tmp $asset
        $sums = Join-Path $tmp 'SHA256SUMS'
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $zip
        Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums

        $want = $null
        foreach ($line in Get-Content -Path $sums) {
            $parts = $line -split '\s+', 2
            if ($parts.Length -eq 2 -and $parts[1].TrimStart('*').Trim() -eq $asset) { $want = $parts[0].ToLowerInvariant(); break }
        }
        if (-not $want) { throw "$asset is not listed in SHA256SUMS" }
        $got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
        if ($want -ne $got) { throw "Checksum mismatch for $asset (wanted $want, got $got)" }
        Write-Host 'Checksum OK.'

        # --- install ---
        $stage = Join-Path $tmp 'x'
        Expand-Archive -Path $zip -DestinationPath $stage -Force
        $exe = Join-Path $stage "$bin.exe"
        if (-not (Test-Path $exe)) { throw "$bin.exe not found inside $asset" }
        New-Item -ItemType Directory -Path $dest -Force | Out-Null
        Copy-Item -Path (Join-Path $stage '*') -Destination $dest -Recurse -Force
    }
    finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
    Write-Host "Installed $dest\$bin.exe ($tag)."

    # --- PATH (user scope) ---
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) { $userPath = '' }
    $entries = $userPath -split ';' | Where-Object { $_ -ne '' }
    $present = $false
    foreach ($e in $entries) { if ($e.TrimEnd('\') -ieq $dest.TrimEnd('\')) { $present = $true } }
    if (-not $present) {
        if ($userPath -eq '') { $newPath = $dest } else { $newPath = $userPath.TrimEnd(';') + ';' + $dest }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Host "Added $dest to your user PATH (open a new terminal to pick it up)."
    }
    $env:Path = $env:Path + ';' + $dest

    Write-Host ''
    Write-Host "Run: $bin   (or: $bin <host>[:port])"
}

Install-Tn5250Tui
