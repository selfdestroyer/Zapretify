#!/bin/bash
# Удаляет демон zapretifyd, движок и их файлы; правила pf снимаются.
# Использование: sudo bash uninstall-daemon.sh
set -uo pipefail

ROOT="/Library/Application Support/Zapretify"
LABEL=io.github.selfdestroyer.zapretifyd

[[ $EUID -eq 0 ]] || { echo "Запустите через sudo"; exit 1; }

launchctl bootout "system/$LABEL" 2>/dev/null   # демон сам останавливает движок и снимает pf
pfctl -a com.apple/zapretify -F all 2>/dev/null
pkill -TERM -f "^$ROOT/engine/utunws " 2>/dev/null
rm -f "/Library/LaunchDaemons/$LABEL.plist" /var/run/zapretifyd.sock /var/run/zapretify.conf
rm -rf "$ROOT" /Library/Logs/Zapretify
echo "Демон Zapretify удалён."
