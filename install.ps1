# devctx Universal Installer for Windows PowerShell
# Usage: irm https://recscse.github.io/devctx/install.ps1 | iex; devctx setup

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Write-Host "[devctx] Installing Developer Context Engine for Windows..." -ForegroundColor Green

$InstallDir = "$env:LOCALAPPDATA\devctx\bin"
if (!(Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$TargetBin = Join-Path $InstallDir "devctx.exe"
$Installed = $false

# 1. Try downloading pre-compiled binary from GitHub Releases
$DownloadUrl = "https://github.com/recscse/devctx/releases/latest/download/devctx_v1.0.0_windows_amd64.zip"
$ZipPath = Join-Path $env:TEMP "devctx_v1.0.0_windows_amd64.zip"

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
    Write-Host "[devctx] Compiling with Go..." -ForegroundColor Green
    go install github.com/recscse/devctx@latest
    $Gopath = go env GOPATH
    $GoBin = Join-Path $Gopath "bin\devctx.exe"
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

Write-Host "[OK] devctx installed successfully to $TargetBin!" -ForegroundColor Green
Write-Host ""
Write-Host "[NEXT] Running devctx setup to configure AI clients..." -ForegroundColor Cyan

& "$TargetBin" setup
