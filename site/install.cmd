@echo off
setlocal enabledelayedexpansion
chcp 65001 >nul

echo [devctx] Installing Developer Context Engine for Windows...

set "INSTALL_DIR=%LOCALAPPDATA%\devctx\bin"
if not exist "%INSTALL_DIR%" (
    mkdir "%INSTALL_DIR%"
)

set "TARGET_BIN=%INSTALL_DIR%\devctx.exe"
set "ZIP_PATH=%TEMP%\devctx_v1.0.0_windows_amd64.zip"
set "DOWNLOAD_URL=https://github.com/recscse/devctx/releases/latest/download/devctx_v1.0.0_windows_amd64.zip"

echo [devctx] Downloading pre-compiled binary...
curl -fsSL "%DOWNLOAD_URL%" -o "%ZIP_PATH%" >nul 2>&1

if exist "%ZIP_PATH%" (
    tar -xf "%ZIP_PATH%" -C "%INSTALL_DIR%" >nul 2>&1
    del /f /q "%ZIP_PATH%" >nul 2>&1
)

if not exist "%TARGET_BIN%" (
    echo [devctx] Compiling via Go...
    where go >nul 2>&1
    if !errorlevel! equ 0 (
        go install github.com/recscse/devctx@latest
        for /f "tokens=*" %%g in ('go env GOPATH') do (
            if exist "%%g\bin\devctx.exe" (
                copy "%%g\bin\devctx.exe" "%TARGET_BIN%" >nul 2>&1
            )
        )
    )
)

if not exist "%TARGET_BIN%" (
    echo [ERROR] Failed to install devctx. Please ensure curl/tar or Go is available.
    exit /b 1
)

:: Add to user PATH if not already present
echo %PATH% | findstr /i /c:"%INSTALL_DIR%" >nul
if %errorlevel% neq 0 (
    powershell -Command "[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';%INSTALL_DIR%', 'User')" >nul 2>&1
    set "PATH=%PATH%;%INSTALL_DIR%"
    echo [OK] Added %INSTALL_DIR% to User PATH
)

echo [OK] devctx installed successfully to %TARGET_BIN%!
echo.
echo [NEXT] Running devctx setup...
echo.

"%TARGET_BIN%" setup
