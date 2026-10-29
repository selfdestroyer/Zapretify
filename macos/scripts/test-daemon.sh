#!/bin/bash
# Проверка одной стратегии демона zapretifyd с автоматической остановкой.
# Запуск (демон уже установлен): sudo bash test-daemon.sh [стратегия]
# Скрипт сам включает движок, собирает данные и ВСЕГДА выключает его в конце, даже если сеть
# перестала работать. Отчёт: REPORT_DIR (по умолчанию рабочий стол).

set -u
[[ $EUID -eq 0 ]] || { echo "Запустите через sudo"; exit 1; }
STRATEGY=${1:-general}
Z="/Library/Application Support/Zapretify/zapretifyd"
USER_NAME=${SUDO_USER:?запустите через sudo от своего пользователя}
USER_HOME=$(dscl . -read "/Users/$USER_NAME" NFSHomeDirectory | awk '{print $2}')
REPORT_DIR=${REPORT_DIR:-$USER_HOME/Desktop}
REPORT="$REPORT_DIR/test $(echo "$STRATEGY" | tr -c 'A-Za-z0-9 ()\n' '_') $(date +%H%M%S).txt"
IFACE=$(route -n get default | awk '/interface:/{print $2}')
SITES="https://www.youtube.com https://i.ytimg.com https://discord.com https://gateway.discord.gg
       https://cdn.discordapp.com https://www.instagram.com https://x.com https://example.com"
TRIES=3
TD1= TD2=

exec 3>&1 # терминал; основной вывод идёт в отчёт
say() { echo "$*" >&3; }
section() { printf '\n===== %s [%s] =====\n' "$1" "$(date +%T)"; say "  $(date +%T) $1"; }

finish() {
  {
    [[ -n $TD1 ]] && kill "$TD1" 2>/dev/null
    [[ -n $TD2 ]] && kill "$TD2" 2>/dev/null
    section "Остановка"
    sudo -u "$USER_NAME" "$Z" stop | grep -E '"(ok|running|error)"'
    ifconfig utun60 >/dev/null 2>&1 && echo "utun60 ВСЁ ЕЩЁ ЕСТЬ" || echo "utun60 убран"
    echo "Правила pf после остановки (должно быть пусто):"; pfctl -a com.apple/zapretify -s rules 2>/dev/null
    section "Сайты после остановки"
    sites
  } >>"$REPORT" 2>&1
  chown "$USER_NAME" "$REPORT"
  say "  остановлено, отчёт: $REPORT"
}

# Каждый сайт TRIES раз от имени пользователя (как браузер): «код время» через «|».
# При ошибке добавляется код curl: 6 — DNS, 7 — соединение отклонено, 28 — таймаут,
# 35 — ошибка TLS, 52/56 — соединение сброшено.
# Сайты проверяются параллельно (иначе заблокированные сайты дают 8 × 3 × 8 с ожидания на блок).
site() {
  printf '%-30s' "$1"
  for i in $(seq $TRIES); do
    r=$(sudo -u "$USER_NAME" curl -s -o /dev/null -m 8 -w '%{http_code} %{time_total}s' "$1"); rc=$?
    [[ $rc -ne 0 ]] && r="$r e$rc"
    printf ' %s' "$r"
    [[ $i -lt $TRIES ]] && printf ' |'
  done
  echo
}

sites() {
  local tmp n=0 pids=""
  tmp=$(mktemp -d)
  for u in $SITES; do
    n=$((n + 1))
    site "$u" >"$tmp/$n" &
    pids="$pids $!"
  done
  wait $pids
  for i in $(seq $n); do cat "$tmp/$i"; done
  rm -rf "$tmp"
}

# Адреса от системного DNS и от DoH Cloudflare: разные адреса или заглушки = подмена DNS провайдером.
dns_check() {
  for h in www.youtube.com www.instagram.com discord.com x.com; do
    sys=$(dscacheutil -q host -a name "$h" | awk '/ip_address/{print $2}' | grep -v : | sort | tr '\n' ' ')
    doh=$(curl -s -m 6 -H 'accept: application/dns-json' "https://1.1.1.1/dns-query?name=$h&type=A" |
      grep -o '"data":"[0-9.]*"' | cut -d'"' -f4 | sort | tr '\n' ' ')
    printf '%-20s система: %-36s DoH: %s\n' "$h" "${sys:-нет}" "${doh:-недоступен}"
  done
}

say "Стратегия «${STRATEGY}»..."
{
  echo "Стратегия: $STRATEGY, интерфейс: $IFACE, $(date)"
  sw_vers | tr '\n' ' '; uname -m
  section "Маршрут (VPN)"
  for d in default 1.1.1.1 142.250.0.1 162.159.128.233; do
    printf '%-16s через %s\n' "$d" "$(route -n get "$d" 2>/dev/null | awk '/interface:/{print $2}')"
  done
  section "DNS"
  dns_check
  section "Сайты до запуска ($TRIES попытки)"
  sites

  trap finish EXIT
  section "Запуск"
  sudo -u "$USER_NAME" "$Z" start "$STRATEGY" | grep -E '"(ok|running|strategy|error|note)"'
  ifconfig utun60
  echo "Правила pf:"; pfctl -a com.apple/zapretify -s rules 2>/dev/null

  section "Сайты при работающем движке ($TRIES попытки)"
  sites

  # Пакеты одного соединения с example.com (адрес от системного DNS, его же использует curl):
  # что уходит в utun60 и что реально уходит в сеть и приходит обратно.
  IP=$(dscacheutil -q host -a name example.com | awk '/ip_address/{print $2}' | grep -v : | head -1)
  section "Пакеты example.com ($IP)"
  if [[ -n $IP ]]; then
    CAP1=$(mktemp); CAP2=$(mktemp)
    tcpdump -i utun60 -nn -l "host $IP" >"$CAP1" 2>/dev/null & TD1=$!
    tcpdump -i "$IFACE" -nn -l "host $IP and port 443" >"$CAP2" 2>/dev/null & TD2=$!
    sleep 1
    sudo -u "$USER_NAME" curl -s -o /dev/null -m 8 --resolve "example.com:443:$IP" -w 'curl: %{http_code} %{time_total}s\n' https://example.com
    sleep 1
    kill "$TD1" "$TD2" 2>/dev/null; TD1= TD2=
    echo "в utun60: $(wc -l <"$CAP1" | tr -d ' '), через $IFACE: $(wc -l <"$CAP2" | tr -d ' ')"
    echo "--- utun60"; head -12 "$CAP1"
    echo "--- $IFACE"; head -20 "$CAP2"
    rm -f "$CAP1" "$CAP2"
  fi

  section "Статус демона"
  sudo -u "$USER_NAME" "$Z" status | grep -E '"(running|strategy|pid|iface|last_exit|note)"'
} >"$REPORT" 2>&1
