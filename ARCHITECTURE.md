# Техническая архитектура Zapretify

Документ описывает внутреннее устройство, архитектурные слои, механизм совмещения двух ключевых технологий (**`zapret`** и **`tg-ws-proxy`**), межпроцессное взаимодействие (IPC) и спецификацию внутренних API приложения **Zapretify**.

---

## 1. Архитектурная концепция: объединение двух разных технологий

Главная инженерная задача Zapretify — устранить фрагментацию инструментов обхода цензуры и объединить **два принципиально различных механизма** в один стабильный процесс:

1. **L3/L4/L7 Пакетная десинхронизация (`zapret`)**:
   - Работает на уровне сетевого адаптера через драйвер ядра **WinDivert**.
   - Не направляет трафик на сторонние прокси/VPN-серверы, сохраняя 100% скорости домашнего канала.
   - Модифицирует заголовки TCP/UDP пакетов (TLS ClientHello, QUIC, STUN), ослепляя системы ТСПУ/DPI для YouTube, Discord и веб-сайтов.

2. **L7 Прикладной транспортный туннель (`tg-ws-proxy`)**:
   - Работает как локальный прокси-сервер на уровне приложения.
   - Перехватывает бинарный протокол Telegram MTProto и упаковывает его в защищенное соединение **WebSocket (WSS)** через Cloudflare CDN/Workers.
   - Обеспечивает бесперебойную работу Telegram даже в условиях полной блокировки официальных IP-адресов дата-центров (DC1–DC5) или сигнатур протокола MTProto.

---

## 2. Общая схема потоков данных и сетевого разделения

```mermaid
graph TD
    subgraph "Клиентские приложения"
        Browser["Веб-браузеры / Приложения"]
        Discord["Discord (Чат / Медиа / Голос)"]
        Telegram["Telegram Desktop (MTProto)"]
    end

    subgraph "Zapretify (Единый Go Host Process)"
        API["Встроенный HTTP REST API (127.0.0.1:порт)"]
        UI["WebView2 Frontend (Edge Chromium)"]
        Tray["Win32 Shell Notify Icon"]
        
        AppCtrl["Главный оркестратор (app.go)"]
        
        subgraph "Ядро 1: Zapret Manager"
            ZapretProc["Process Runner / Service Runner"]
            StratReg["Реестр 22 стратегий Flowseal"]
            Bench["AutoBenchmark движок"]
            Upstream["Flowseal Updater"]
        end
        
        subgraph "Ядро 2: tg-ws-proxy Server"
            TCPListener["Локальный TCP Listener (:1080)"]
            WSTunnel["WebSocket / H2 Dialer"]
            PayloadPipe["Pipelining & Buffering"]
        end
    end

    subgraph "Драйверы и системный уровень"
        WinDivertDriver["Драйвер ядра WinDivert64.sys"]
        WinwsBin["winws.exe (bol-van)"]
    end

    subgraph "Внешние сети"
        YouTubeCDN["YouTube / Google CDN"]
        DiscordVoice["Discord Voice Servers"]
        CFEdge["Cloudflare Edge CDN / Workers (WSS)"]
        TGDatacenters["Telegram DCs (DC1..DC5)"]
    end

    UI --- API
    Tray --> AppCtrl
    API --> AppCtrl

    AppCtrl --> ZapretProc
    AppCtrl --> TCPListener

    %% Zapret Path
    Browser -->|"TCP 80/443"| WinDivertDriver
    Discord -->|"TCP/UDP Голос и STUN"| WinDivertDriver
    WinDivertDriver ---|"Перехват и десинхронизация"| WinwsBin
    WinwsBin -->|"Фальшивые пакеты / Сплит TLS / Fake QUIC"| YouTubeCDN
    WinwsBin -->|"STUN десинхронизация"| DiscordVoice

    %% Telegram WS Proxy Path
    Telegram -->|"Прямой MTProto TCP"| TCPListener
    TCPListener --> WSTunnel
    WSTunnel -->|"WSS over TLS (Маскировка HTTPS)"| CFEdge
    CFEdge -->|"Форвардинг пакетов"| TGDatacenters
```

---

## 3. Ключевые подсистемы ядра

### 3.1. Подсистема Zapret (`core/zapret`)
- **Режимы работы**:
  1. *Пользовательский процесс (User Mode)*: запуск `bin/winws.exe` с аргументами выбранной стратегии через `exec.Command` с флагами сокрытия окон (`CREATE_NO_WINDOW`, `HideWindow: true`).
  2. *Системная служба Windows (Service Mode)*: интеграция с диспетчером служб Windows через `sc.exe create zapret ... start= auto`. Служба стартует до входа пользователя в Windows.
- **Обработка аргументов**:
  - Подстановка системных макросов: `%BIN%` (путь к бинарным файлам рукопожатий TLS/QUIC), `%LISTS%` (путь к хост-листам), `%GameFilterTCP%`/`%GameFilterUDP%` (порты игровых протоколов).
- **Изоляция сетевого трафика**:
  - Локальные порты прокси (`1080`, `1443`) и локальный интерфейс `127.0.0.1` исключены из фильтрации WinDivert во избежание рекурсивного перехвата.

### 3.2. Подсистема `tg-ws-proxy` (`core/tgproxy`)
- **Архитектура сервера**:
  - Реализован на чистом Go с использованием `github.com/gorilla/websocket`.
  - Запускает локальный TCP listener настраиваемого порта (по умолчанию `127.0.0.1:1080` или `1443`).
- **Туннелирование**:
  - При поступлении соединения от клиента Telegram, сервер устанавливает защищенное WebSocket-соединение (`wss://<worker-domain>/api`) с Cloudflare Workers.
  - Полнодуплексный конвейер данных (`clientConn <-> wsConn`) с буферизацией по 32 КБ.
  - Резервный режим: прямое TCP-подключение к Telegram DC2 (`149.154.167.220:443`) при отключенном режиме CF Worker.

### 3.3. Автоматический бенчмарк стратегий (`core/zapret/tester.go`)
- **Алгоритм тестирования**:
  1. Временная остановка фоновой службы для обеспечения чистоты замеров.
  2. Перебор 22 стратегий Flowseal:
     - Запуск `winws.exe` с тестируемыми флагами;
     - Прогрев драйвера WinDivert (400 мс);
     - Проверка контрольных доменов (YouTube GGC, Discord, Cloudflare) методами Ping, HTTPS handshake или DPI probe;
     - Замер процента успешных ответов и средней задержки (RTT);
     - Завершение тестового процесса.
  3. Ранжирование по стабильности (`Score = SuccessRate * 1000 - AvgLatency`).
  4. Запись отчета в файл `test_results.txt`.

### 3.4. Upstream-синхронизатор Flowseal (`core/updater`)
- **Списки хостов**:
  - Скачивание `list-general.txt`, `list-google.txt`, `list-exclude.txt`, `ipset-exclude.txt`, `ipset-all.txt` из `raw.githubusercontent.com`.
  - Атомарная запись через временные файлы (`.download` -> `os.Rename`).
- **Синхронизация стратегий**:
  - Динамический опрос GitHub API репозитория Flowseal на предмет обнаружения новых пакетных файлов `general*.bat`.
  - Параллельная загрузка содержимого файлов через пул воркеров.
  - Функция `ParseBatRawArgs`: очистка от управляющих символов `^`, сборка аргументов `winws.exe`, обновление `strategies.json` и вызов `zapret.GetRegistry().Load()` для горячей перезагрузки стратегий в памяти без перезапуска приложения.

### 3.5. Win32 и оконный уровень (`window_windows.go`)
- **WebView2 Integration**: встраивание легковесного веб-движка Microsoft Edge Chromium.
- **Windows DWM Dark Mode**: вызов `DwmSetWindowAttribute(hwnd, DWMWA_USE_IMMERSIVE_DARK_MODE, ...)` для отображения нативного тёмного заголовка окна.
- **Системный трей Windows**:
  - Регистрация иконки через `Shell_NotifyIconW`.
  - Обработка клика правой кнопкой мыши (`WM_RBUTTONUP`) с вызовом нативного меню (`TrackPopupMenuEx`).
  - Асинхронные команды включения/выключения Zapret и Telegram прокси.

---

## 4. Спецификация внутреннего REST API (`api.go`)

| Метод | Эндпоинт | Описание |
|---|---|---|
| `GET` | `/api/status` | Статус Zapret, TG-прокси и фоновой службы |
| `GET` | `/api/config` | Текущие настройки приложения |
| `POST` | `/api/config` | Сохранение настроек (`config.json`) |
| `GET` | `/api/strategies` | Список доступных стратегий с описанием и параметрами |
| `POST` | `/api/zapret/start` | Запуск `winws.exe` с выбранной стратегией |
| `POST` | `/api/zapret/stop` | Остановка процесса `winws.exe` |
| `POST` | `/api/tgproxy/start` | Запуск локального MTProto WebSocket прокси |
| `POST` | `/api/tgproxy/stop` | Остановка локального MTProto WebSocket прокси |
| `POST` | `/api/benchmark` | Старт тестирования стратегий |
| `POST` | `/api/benchmark/cancel` | Отмена идущего тестирования |
| `GET` | `/api/benchmark/status` | Получение результатов автотеста |
| `POST` | `/api/updater/assets` | Синхронизация списков и стратегий из Flowseal |
| `GET` | `/api/lists?file=...` | Чтение содержимого файла списка хостов |
| `POST` | `/api/lists?file=...` | Сохранение изменений в файле списка хостов |
| `GET` | `/api/logs` | Чтение последних строк лога `zapretify.log` |
| `POST` | `/api/logs/clear` | Очистка файла `zapretify.log` |
| `POST` | `/api/app/force-kill` | Принудительное завершение всех процессов и драйвера |
