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

:: 2. Create Portable ZIP Package
echo [2/3] Создание Portable ZIP архива...
if not exist "dist" mkdir dist
powershell -NoProfile -Command "Compress-Archive -Path 'Zapretify.exe', 'bin', 'lists', 'assets', 'strategies.json', 'README.md', 'LICENSE', 'ARCHITECTURE.md' -DestinationPath 'dist\Zapretify-Portable-v1.1.0.zip' -Force"
echo [OK] dist\Zapretify-Portable-v1.1.0.zip создан!
echo.

:: 3. Build Windows Installer (Go + WebView2)
echo [3/3] Сборка инсталлятора Zapretify-Setup...
if exist "dist\Zapretify-Portable-v1.1.0.zip" (
    copy /y "dist\Zapretify-Portable-v1.1.0.zip" "cmd\installer\payload.zip" >nul
)
go build -ldflags="-s -w -H=windowsgui" -o dist\Zapretify-Setup-v1.1.0.exe .\cmd\installer
if %errorlevel% equ 0 (
    echo [OK] dist\Zapretify-Setup-v1.1.0.exe успешно создан!
) else (
    echo [NOTE] Ошибка компиляции Go-установщика. Проверка Inno Setup...
    where iscc >nul 2>&1
    if %errorlevel% equ 0 (
        iscc packaging\installer.iss
        echo [OK] dist\Zapretify-Setup-v1.1.0.exe создан через Inno Setup!
    )
)

echo.
echo ==============================================
echo   Сборка завершена успешно! Результаты в dist/
echo ==============================================
pause
