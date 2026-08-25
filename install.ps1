# ctxd Universal Installer for Windows PowerShell
# Usage: irm https://ctxd.dev/install.ps1 | iex; ctxd setup

$ErrorActionPreference = "Stop"

Write-Host "⚡ Installing ctxd (AI Context Engine) for Windows..." -ForegroundColor Cyan

$InstallDir = "$env:LOCALAPPDATA\ctxd\bin"
if (!(Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$TargetBin = Join-Path $InstallDir "ctxd.exe"

# If go is installed, build from source
if (Get-Command go -ErrorAction SilentlyContinue) {
    Write-Host "🔨 Compiling latest release with Go..." -ForegroundColor Green
    go install github.com/recscse/ctxd@latest
    $Gopath = go env GOPATH
    $GoBin = Join-Path $Gopath "bin\ctxd.exe"
    if (Test-Path $GoBin) {
        Copy-Item $GoBin $TargetBin -Force
    }
}

# Update User PATH environment variable if needed
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "✓ Added $InstallDir to User PATH" -ForegroundColor Green
}

Write-Host "✨ ctxd installed successfully to $TargetBin!" -ForegroundColor Green
Write-Host ""
Write-Host "🚀 Next step: Run 'ctxd setup' in your repository to configure all AI agents automatically." -ForegroundColor Cyan
