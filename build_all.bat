@echo off
chcp 65001 > nul
title Zapretify Build System

echo ==============================================
echo   Zapretify: Сборка Portable и Installer
echo ==============================================
echo.

cd /d "%~dp0"

:: 1. Compile Go Binary for Windows amd64
echo [1/3] Компиляция Zapretify.exe на Go...
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w -H=windowsgui" -o Zapretify.exe .
if %errorlevel% neq 0 (
    echo [ERROR] Ошибка компиляции Go!
    pause
    exit /b 1
)
echo [OK] Zapretify.exe успешно скомпилирован! (Размер: ~8 МБ)
echo.

set APP_VERSION=1.3.3

:: 2. Create Portable ZIP Package
echo [2/3] Создание Portable ZIP архива...
if not exist "dist" mkdir dist
powershell -NoProfile -Command "Compress-Archive -Path 'Zapretify.exe', 'config.json', 'strategies.json', 'bin', 'lists', 'assets' -DestinationPath 'dist\Zapretify-Portable-v%APP_VERSION%.zip' -Force"
echo [OK] dist\Zapretify-Portable-v%APP_VERSION%.zip создан!
echo.

:: 3. Build Windows Installer (Go + WebView2)
echo [3/3] Сборка инсталлятора Zapretify-Setup...
powershell -NoProfile -Command "Compress-Archive -Path 'Zapretify.exe', 'config.json', 'strategies.json', 'bin', 'lists', 'assets' -DestinationPath 'cmd\installer\payload.zip' -Force"
go build -ldflags="-s -w -H=windowsgui" -o dist\Zapretify-Setup-v%APP_VERSION%.exe .\cmd\installer
if %errorlevel% equ 0 (
    echo [OK] dist\Zapretify-Setup-v%APP_VERSION%.exe успешно создан!
) else (
    echo [NOTE] Ошибка компиляции Go-установщика. Проверка Inno Setup...
    where iscc >nul 2>&1
    if %errorlevel% equ 0 (
        iscc packaging\installer.iss
        echo [OK] dist\Zapretify-Setup-v%APP_VERSION%.exe создан через Inno Setup!
    )
)

echo.
echo ==============================================
echo   Сборка завершена успешно! Результаты в dist/
echo ==============================================
pause
