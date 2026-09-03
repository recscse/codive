@echo off
setlocal enabledelayedexpansion
chcp 65001 >nul

echo [codive] Installing Developer Context Engine for Windows...

set "INSTALL_DIR=%LOCALAPPDATA%\codive\bin"
if not exist "%INSTALL_DIR%" (
    mkdir "%INSTALL_DIR%"
)

set "TARGET_BIN=%INSTALL_DIR%\codive.exe"
set "ZIP_PATH=%TEMP%\codive_v1.0.0_windows_amd64.zip"
set "DOWNLOAD_URL=https://github.com/recscse/codive/releases/latest/download/codive_v1.0.0_windows_amd64.zip"

echo [codive] Downloading pre-compiled binary...
curl -fsSL "%DOWNLOAD_URL%" -o "%ZIP_PATH%" >nul 2>&1

if exist "%ZIP_PATH%" (
    tar -xf "%ZIP_PATH%" -C "%INSTALL_DIR%" >nul 2>&1
    del /f /q "%ZIP_PATH%" >nul 2>&1
)

if not exist "%TARGET_BIN%" (
    echo [codive] Compiling via Go...
    where go >nul 2>&1
    if !errorlevel! equ 0 (
        go install github.com/recscse/codive@latest
        for /f "tokens=*" %%g in ('go env GOPATH') do (
            if exist "%%g\bin\codive.exe" (
                copy "%%g\bin\codive.exe" "%TARGET_BIN%" >nul 2>&1
            )
        )
    )
)

if not exist "%TARGET_BIN%" (
    echo [ERROR] Failed to install codive. Please ensure curl/tar or Go is available.
    exit /b 1
)

:: Add to user PATH if not already present
echo %PATH% | findstr /i /c:"%INSTALL_DIR%" >nul
if %errorlevel% neq 0 (
    powershell -Command "[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';%INSTALL_DIR%', 'User')" >nul 2>&1
    set "PATH=%PATH%;%INSTALL_DIR%"
    echo [OK] Added %INSTALL_DIR% to User PATH
)

echo [OK] codive installed successfully to %TARGET_BIN%!
echo.
echo [NEXT] Running codive setup...
echo.

"%TARGET_BIN%" setup
