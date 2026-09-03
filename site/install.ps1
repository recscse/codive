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
$DownloadUrl = "https://github.com/recscse/codive/releases/latest/download/codive_v1.0.0_windows_amd64.zip"
$ZipPath = Join-Path $env:TEMP "codive_v1.0.0_windows_amd64.zip"

try {
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
