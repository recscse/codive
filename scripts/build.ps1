# scripts/build.ps1 - Cross-platform build script for Windows/PowerShell
param (
    [string]$Version = "v1.1.0",
    [string]$Commit = "dev",
    [string]$Date = (Get-Date -Format "yyyy-MM-ddTHH:mm:ssZ")
)

$ldflags = "-s -w -X main.Version=$Version -X main.GitCommit=$Commit -X main.BuildDate=$Date"

$targets = @(
    @{ os = "windows"; arch = "amd64"; ext = ".exe" },
    @{ os = "windows"; arch = "arm64"; ext = ".exe" },
    @{ os = "darwin";  arch = "amd64"; ext = "" },
    @{ os = "darwin";  arch = "arm64"; ext = "" },
    @{ os = "linux";   arch = "amd64"; ext = "" },
    @{ os = "linux";   arch = "arm64"; ext = "" }
)

$distDir = Join-Path $PSScriptRoot "..\dist"
if (Test-Path $distDir) {
    Remove-Item -Recurse -Force $distDir
}
New-Item -ItemType Directory -Path $distDir | Out-Null

Write-Host "Building codive $Version binaries across 6 OS/Arch targets..." -ForegroundColor Cyan

foreach ($t in $targets) {
    $os = $t.os
    $arch = $t.arch
    $outName = "codive-$os-$arch$($t.ext)"
    $outPath = Join-Path $distDir $outName
    Write-Host "  -> Building $outName ($os/$arch)..." -ForegroundColor Green
    
    $env:GOOS = $os
    $env:GOARCH = $arch
    $env:CGO_ENABLED = "0"
    
    go build -ldflags $ldflags -o $outPath .
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to build target $os/$arch"
        exit 1
    }
}

Write-Host "`nAll release binaries successfully built in dist/:" -ForegroundColor Cyan
Get-ChildItem $distDir | Select-Object Name, Length
