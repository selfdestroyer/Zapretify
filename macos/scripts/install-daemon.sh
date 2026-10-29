#!/bin/bash
# Устанавливает демон zapretifyd и движок utunws (этап 2 порта, проверка из Терминала).
# Использование: sudo bash install-daemon.sh <zapretifyd> <utunws>
# Владельцем (кто может управлять демоном) становится пользователь, запустивший sudo.
set -euo pipefail

ROOT="/Library/Application Support/Zapretify"
LABEL=io.github.selfdestroyer.zapretifyd
PLIST="/Library/LaunchDaemons/$LABEL.plist"
LOGDIR=/Library/Logs/Zapretify

[[ $EUID -eq 0 ]] || { echo "Запустите через sudo"; exit 1; }
DAEMON=${1:?укажите путь к zapretifyd}
ENGINE=${2:?укажите путь к utunws}
OWNER_UID=${SUDO_UID:?запустите через sudo от своего пользователя}
[[ $OWNER_UID -gt 0 ]] || { echo "Владелец не может быть root"; exit 1; }

launchctl bootout "system/$LABEL" 2>/dev/null || true

install -d -o root -g wheel -m 0755 "$ROOT" "$ROOT/engine" "$LOGDIR"
install -o root -g wheel -m 0755 "$DAEMON" "$ROOT/zapretifyd"
install -o root -g wheel -m 0755 "$ENGINE" "$ROOT/engine/utunws"
printf '{"owner_uid": %d}\n' "$OWNER_UID" > "$ROOT/daemon.json"
chown root:wheel "$ROOT/daemon.json"
chmod 0644 "$ROOT/daemon.json"

cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array><string>$ROOT/zapretifyd</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardErrorPath</key><string>$LOGDIR/zapretifyd.log</string>
</dict>
</plist>
EOF
chown root:wheel "$PLIST"
chmod 0644 "$PLIST"

launchctl bootstrap system "$PLIST"
sleep 1
"$ROOT/zapretifyd" status >/dev/null && echo "Демон установлен и отвечает. Журнал: $LOGDIR/zapretifyd.log"
