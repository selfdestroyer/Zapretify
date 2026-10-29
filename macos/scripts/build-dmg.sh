#!/bin/bash
# Собирает dist/Zapretify-macOS-v<версия>.dmg: Zapretify.app, ярлык «Программы» и оформленное окно
# (фон в цветах приложения, стрелка, подсказка о первом запуске).
# Сначала собирает приложение (build-app.sh). Нужен python3; dmgbuild ставится в dist/macos/.venv.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT="$ROOT/dist/macos"
APP="$OUT/Zapretify.app"
VERSION=$(sed -n 's/^const Version = "\(.*\)"/\1/p' "$ROOT/core/config/config.go")
DMG="$ROOT/dist/Zapretify-macOS-v$VERSION.dmg"
DMGBUILD_VERSION=1.6.7

bash "$ROOT/macos/scripts/build-app.sh"

echo "== Фон окна"
BG="$OUT/dmg-bg"; mkdir -p "$BG"
xcrun swiftc -O "$ROOT/macos/app/dmg-background.swift" -o "$BG/render"
"$BG/render" "$BG/bg.png" 1 "$APP/Contents/Resources/Zapretify.icns"
"$BG/render" "$BG/bg@2x.png" 2 "$APP/Contents/Resources/Zapretify.icns"

echo "== dmgbuild $DMGBUILD_VERSION"
VENV="$OUT/.venv"
[[ -x "$VENV/bin/dmgbuild" ]] || { python3 -m venv "$VENV" && "$VENV/bin/pip" install -q "dmgbuild==$DMGBUILD_VERSION"; }

rm -f "$DMG"
"$VENV/bin/dmgbuild" -s "$ROOT/macos/scripts/dmg-settings.py" -D app="$APP" -D bg="$BG/bg.png" "Zapretify" "$DMG"
codesign -s - -f "$DMG"
rm -rf "$BG"
hdiutil verify "$DMG" >/dev/null
echo "Готово: $DMG ($(du -h "$DMG" | cut -f1))"
shasum -a 256 "$DMG"
