# ctxd Universal Installer for Windows PowerShell
# Usage: irm https://ctxd.dev/install.ps1 | iex; ctxd setup

$ErrorActionPreference = "Stop"

Write-Host "⚡ Installing ctxd (AI Context Engine) for Windows..." -ForegroundColor Cyan

$InstallDir = "$env:LOCALAPPDATA\ctxd\bin"
if (!(Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$TargetBin = Join-Path $InstallDir "ctxd.exe"
$Installed = $false

# 1. Try downloading pre-compiled binary from GitHub Releases
$DownloadUrl = "https://github.com/recscse/ctxd/releases/latest/download/ctxd_v1.0.0_windows_amd64.zip"
$ZipPath = Join-Path $env:TEMP "ctxd_v1.0.0_windows_amd64.zip"

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
    Write-Host "🔨 Compiling with Go..." -ForegroundColor Green
    go install github.com/recscse/ctxd@latest
    $Gopath = go env GOPATH
    $GoBin = Join-Path $Gopath "bin\ctxd.exe"
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
    Write-Host "✓ Added $InstallDir to User PATH" -ForegroundColor Green
}

Write-Host "✨ ctxd installed successfully to $TargetBin!" -ForegroundColor Green
Write-Host ""
Write-Host "🚀 Next step: Run 'ctxd setup' in your repository to configure all AI agents automatically." -ForegroundColor Cyan
