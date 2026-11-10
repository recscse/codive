# codive Universal Installer for Windows PowerShell
# Usage: irm https://recscse.github.io/codive/install.ps1 | iex; codive setup

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Write-Host "[codive] Installing Developer Context Engine for Windows..." -ForegroundColor Green

$InstallDir = "$env:LOCALAPPDATA\codive\bin"
if (!(Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$TargetBin = Join-Path $InstallDir "codive.exe"
$Installed = $false

# 1. Try downloading pre-compiled binary from GitHub Releases
try {
    # Asset names include the version, so find the latest release's tag first.
    $Tag = (Invoke-RestMethod -Uri "https://api.github.com/repos/recscse/codive/releases/latest" -UseBasicParsing).tag_name
    $AssetName = "codive_${Tag}_windows_amd64.zip"
    $DownloadUrl = "https://github.com/recscse/codive/releases/download/$Tag/$AssetName"
    $ZipPath = Join-Path $env:TEMP $AssetName
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $ZipPath -UseBasicParsing
    Expand-Archive -Path $ZipPath -DestinationPath $InstallDir -Force
    Remove-Item $ZipPath -Force
    $Installed = $true
} catch {
    # Fallback to Go compilation
}

# 2. Fallback: If go is installed, build from source
if (-not $Installed -and (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "[codive] Compiling with Go..." -ForegroundColor Green
    go install github.com/recscse/codive@latest
    $Gopath = go env GOPATH
    $GoBin = Join-Path $Gopath "bin\codive.exe"
    if (Test-Path $GoBin) {
        Copy-Item $GoBin $TargetBin -Force
        $Installed = $true
    }
}

if (-not (Test-Path $TargetBin)) {
    Write-Host "[ERROR] Could not download codive, and Go is not installed to build it." -ForegroundColor Red
    Write-Host "Download it from https://github.com/recscse/codive/releases" -ForegroundColor Red
    exit 1
}

# 3. Update User PATH environment variable
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "[OK] Added $InstallDir to User PATH" -ForegroundColor Green
}

Write-Host "[OK] codive installed successfully to $TargetBin!" -ForegroundColor Green
Write-Host ""
Write-Host "[NEXT] Running codive setup to configure AI clients..." -ForegroundColor Cyan

& "$TargetBin" setup
