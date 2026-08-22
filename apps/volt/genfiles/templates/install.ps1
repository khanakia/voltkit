# [[.Binary]] installer for Windows (PowerShell).
#
# Usage:
#   irm [[.RawScriptURLPS]] | iex
#   $env:VERSION="v1.2.0"; irm ... | iex     # pin a version
$ErrorActionPreference = "Stop"

$Repo   = "[[.Repo]]"
$Binary = "[[.Binary]]"
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA $Binary }
$Version    = if ($env:VERSION) { $env:VERSION } else { "" }

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

# The redirect endpoint has no rate limit; the JSON API allows 60/hr/IP.
if ($Version) { $base = "[[.DownloadBasePS]]" }
else          { $base = "[[.LatestBase]]" }
if (-not $base) { Write-Error "this forge has no 'latest' redirect - pass a version"; exit 1 }

$tmp = Join-Path $env:TEMP ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")
  $line = Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match "_windows_$arch\.zip$" } | Select-Object -First 1
  if (-not $line) { throw "no windows/$arch asset in this release of $Repo" }
  $sum, $asset = $line -split "\s+", 2
  $zip = Join-Path $tmp $asset
  Write-Host "Downloading $asset..."
  Invoke-WebRequest -Uri "$base/$asset" -OutFile $zip

  # Verify BEFORE installing.
  $actual = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
  if ($actual -ne $sum.ToLower()) { throw "checksum verification FAILED for $asset - refusing to install" }

  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  Copy-Item (Join-Path $tmp "$Binary.exe") (Join-Path $InstallDir "$Binary.exe") -Force

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
    Write-Host "Added $InstallDir to user PATH (restart your shell)."
  }
  Write-Host "Installed $Binary to $InstallDir\$Binary.exe"
} finally {
  Remove-Item -Recurse -Force $tmp
}
