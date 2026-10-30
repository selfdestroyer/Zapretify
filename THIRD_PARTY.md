# Сторонние компоненты

| Компонент | Лицензия | Где используется |
|---|---|---|
| zapret (bol-van) — `winws`, `nfqws`, `tpws` | MIT | Windows: бинарники в `bin/`; macOS: основа движка `utunws` |
| zapret-discord-youtube (Flowseal) — стратегии и списки | MIT | `strategies.json`, `lists/` |
| zapret-mac-discord-youtube (Flowseal) — macOS-движок `utunws` | MIT | macOS: собирается в отдельном репозитории `zapret-macos-engine`, подключается как бинарь по `macos/engine.lock` |
| tg-ws-proxy (Flowseal) | MIT | `core/tgproxy` |
| WinDivert | LGPL-3.0 / GPL-2.0 | Windows: `bin/WinDivert*.sys`, `bin/WinDivert.dll` |
| gorilla/websocket | BSD-2-Clause | `core/tgproxy` |

Полные тексты лицензий и копирайты — в `LICENSE` и в репозитории соответствующего компонента.
