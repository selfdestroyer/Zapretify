// Zapretify.app: a thin macOS shell around the Go core (zapretify-core).
// It starts the core, shows its web UI in a native window (WKWebView) and keeps an icon in the
// menu bar. Closing the window keeps Zapretify running in the menu bar, like the tray on Windows.
// The core lives as long as its stdin is open: quitting (or crashing) the shell stops it.

import AppKit
import WebKit

private let showNotification = Notification.Name("io.github.selfdestroyer.zapretify.show")

// Height of the UI's top bar (.top in index.html): the window buttons are centred in it.
private let headerHeight: CGFloat = 54

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler, NSMenuDelegate {
    private var window: NSWindow!
    private var webView: WKWebView!
    private var statusItem: NSStatusItem!
    private let core = Process()
    private let coreIn = Pipe()
    private let coreOut = Pipe()
    private var uiURL: URL?
    private var startHidden = false
    private var quitting = false
    private var lastMouseDown: NSEvent?
    private var trayMenu: NSMenu!
    private var zapretOn = false
    private var tgOn = false

    func applicationDidFinishLaunching(_ note: Notification) {
        if let other = otherInstance() {
            // A second launch only brings the running Zapretify to the front.
            DistributedNotificationCenter.default().postNotificationName(showNotification, object: nil, deliverImmediately: true)
            other.activate()
            NSApp.terminate(nil)
            return
        }
        DistributedNotificationCenter.default().addObserver(self, selector: #selector(showWindow), name: showNotification, object: nil)
        buildMenu()
        buildStatusItem()
        buildWindow()
        startCore()
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        showWindow()
        return false
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        quitting = true
        stopCore()
        return .terminateNow
    }

    // MARK: core

    private func startCore() {
        guard let exe = Bundle.main.url(forAuxiliaryExecutable: "zapretify-core") else {
            fatal("Не найден zapretify-core внутри Zapretify.app")
            return
        }
        core.executableURL = exe
        // --minimized from the login item (LaunchAgent) goes on to the core; Finder's -psn_… does not.
        core.arguments = CommandLine.arguments.dropFirst().filter { !$0.hasPrefix("-psn") }
        var env = ProcessInfo.processInfo.environment
        env["ZAPRETIFY_SHELL"] = "1"
        core.environment = env
        core.standardInput = coreIn
        core.standardOutput = coreOut
        core.terminationHandler = { [weak self] _ in
            // «Закрыть всё» in the UI or a crash of the core: the shell has nothing left to show.
            DispatchQueue.main.async { if self?.quitting == false { NSApp.terminate(nil) } }
        }
        coreOut.fileHandleForReading.readabilityHandler = { [weak self] h in
            guard let text = String(data: h.availableData, encoding: .utf8), !text.isEmpty else { return }
            for line in text.split(separator: "\n") {
                if line.hasPrefix("ZAPRETIFY_URL "), let url = URL(string: String(line.dropFirst(14))) {
                    DispatchQueue.main.async { self?.coreReady(url) }
                } else if line == "ZAPRETIFY_MINIMIZED" {
                    DispatchQueue.main.async { self?.startHidden = true }
                }
            }
        }
        do { try core.run() } catch { fatal("Не удалось запустить ядро Zapretify: \(error.localizedDescription)") }
    }

    private func coreReady(_ url: URL) {
        guard uiURL == nil else { return }
        uiURL = url
        refreshStatus()
        Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in self?.refreshStatus() }
        webView.load(URLRequest(url: url))
        // ZAPRETIFY_MINIMIZED is printed right after the URL; let it arrive first.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { if !self.startHidden { self.showWindow() } }
    }

    private func stopCore() {
        guard core.isRunning else { return }
        try? coreIn.fileHandleForWriting.close() // the core stops Zapret and the proxy, then exits
        let deadline = Date().addingTimeInterval(8)
        while core.isRunning && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.1)) }
        if core.isRunning { core.terminate() }
    }

    // MARK: window

    private func buildWindow() {
        // The UI draws its own title bar: mark the page and let its header drag the window.
        let config = WKWebViewConfiguration()
        let js = """
        document.documentElement.classList.add('mac-shell');
        document.addEventListener('mousedown', e => {
          if (e.button !== 0 || !e.target.closest('header') || e.target.closest('button,a,input,select,textarea')) return;
          window.webkit.messageHandlers.drag.postMessage(e.detail);
        });
        """
        config.userContentController.addUserScript(WKUserScript(source: js, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        config.userContentController.add(self, name: "drag")
        NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] e in self?.lastMouseDown = e; return e }
        webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 720, height: 380), configuration: config)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.setValue(false, forKey: "drawsBackground") // no white flash before the dark UI loads
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 380),
                          styleMask: [.titled, .closable, .miniaturizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.title = "Zapretify"
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.contentView = webView
        window.backgroundColor = NSColor(red: 0.043, green: 0.051, blue: 0.075, alpha: 1)
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.center()
        window.setFrameAutosaveName("ZapretifyMain")
    }

    @objc func showWindow() {
        NSApp.setActivationPolicy(.regular)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate()
        placeWindowButtons()
    }

    // Centres the close/minimise/zoom buttons in the UI's header instead of the 28 pt system title bar.
    // AppKit lays them out again on focus changes, so this runs after those too.
    private func placeWindowButtons() {
        guard let close = window.standardWindowButton(.closeButton),
              let mini = window.standardWindowButton(.miniaturizeButton),
              let zoom = window.standardWindowButton(.zoomButton),
              let bar = close.superview?.superview else { return }
        var frame = bar.frame
        frame.size.height = headerHeight
        frame.origin.y = window.frame.height - headerHeight
        bar.frame = frame
        for (i, b) in [close, mini, zoom].enumerated() {
            b.setFrameOrigin(NSPoint(x: 20 + CGFloat(i) * 20, y: (headerHeight - b.frame.height) / 2))
        }
    }

    func windowDidBecomeKey(_ notification: Notification) { placeWindowButtons() }
    func windowDidResignKey(_ notification: Notification) { placeWindowButtons() }
    func windowDidResize(_ notification: Notification) { placeWindowButtons() }

    // Mouse down on an empty part of the UI header: move the window; double click follows the system setting.
    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.name == "drag", let event = lastMouseDown else { return }
        if (message.body as? Int ?? 1) >= 2 {
            let action = UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick") ?? "Maximize"
            if action == "Minimize" { window.miniaturize(nil) }
            return
        }
        window.performDrag(with: event)
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        // Closing the window keeps Zapretify working in the menu bar.
        window.orderOut(nil)
        NSApp.setActivationPolicy(.accessory)
        return false
    }

    // Links that leave the local UI (GitHub, Telegram) open in the default browser / app.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if let url = action.request.url, url.host != "127.0.0.1", url.scheme != "about" {
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = action.request.url { NSWorkspace.shared.open(url) }
        return nil
    }

    // MARK: menus

    // Menu bar icon: a click opens the window, a right click (or ⌃-click) shows the menu,
    // the same as the tray icon on Windows.
    private func buildStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.target = self
        statusItem.button?.action = #selector(statusClicked)
        statusItem.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])
        trayMenu = NSMenu()
        trayMenu.delegate = self
        updateStatusIcon()
    }

    @objc private func statusClicked() {
        let event = NSApp.currentEvent
        if event?.type == .rightMouseUp || event?.modifierFlags.contains(.control) == true {
            statusItem.menu = trayMenu
            statusItem.button?.performClick(nil)
            statusItem.menu = nil // otherwise a left click would open the menu too
        } else {
            showWindow()
        }
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        let state = NSMenuItem(title: statusText(), action: nil, keyEquivalent: "")
        state.isEnabled = false
        menu.addItem(state)
        menu.addItem(.separator())
        menu.addItem(withTitle: "Открыть Zapretify", action: #selector(showWindow), keyEquivalent: "").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: zapretOn ? "Выключить Zapret" : "Включить Zapret", action: #selector(toggleZapret), keyEquivalent: "").target = self
        menu.addItem(withTitle: tgOn ? "Выключить Telegram-прокси" : "Включить Telegram-прокси", action: #selector(toggleTG), keyEquivalent: "").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Выйти из Zapretify", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
    }

    private func statusText() -> String {
        switch (zapretOn, tgOn) {
        case (true, true): return "Защита активна: Zapret и Telegram"
        case (true, false): return "Zapret работает"
        case (false, true): return "Работает Telegram-прокси"
        default: return "Службы остановлены"
        }
    }

    // The app logo with a status dot: green — Zapret and the proxy run, orange — one of them,
    // red — nothing. A colour icon (not a template) is allowed in the menu bar; the dot gets a dark
    // ring so it stays visible on light and dark menu bars.
    private lazy var logo: NSImage? = NSImage(named: "Zapretify")

    private func updateStatusIcon() {
        let dot: NSColor = zapretOn && tgOn ? .systemGreen : zapretOn || tgOn ? .systemOrange : .systemRed
        let size = NSSize(width: 20, height: 18)
        let image = NSImage(size: size, flipped: false) { [weak self] _ in
            // The logo has a dark square background: round its corners like an app icon.
            let box = NSRect(x: 0, y: 1, width: 16, height: 16)
            NSGraphicsContext.saveGraphicsState()
            NSBezierPath(roundedRect: box, xRadius: 4, yRadius: 4).addClip()
            self?.logo?.draw(in: box)
            NSGraphicsContext.restoreGraphicsState()
            let r = NSRect(x: 11.5, y: 0.5, width: 8, height: 8)
            NSColor.black.withAlphaComponent(0.55).setFill()
            NSBezierPath(ovalIn: r.insetBy(dx: -1, dy: -1)).fill()
            dot.setFill()
            NSBezierPath(ovalIn: r).fill()
            return true
        }
        image.isTemplate = false
        image.accessibilityDescription = "Zapretify — " + statusText()
        statusItem.button?.image = image
        statusItem.button?.toolTip = "Zapretify — " + statusText()
    }

    @objc private func toggleZapret() { callAPI(zapretOn ? "/api/zapret/stop" : "/api/zapret/start") }
    @objc private func toggleTG() { callAPI(tgOn ? "/api/tg/stop" : "/api/tg/start") }

    // MARK: core API (the same session token as the UI)

    private func apiRequest(_ path: String, post: Bool) -> URLRequest? {
        guard let ui = uiURL, var c = URLComponents(url: ui, resolvingAgainstBaseURL: false),
              let token = c.queryItems?.first(where: { $0.name == "token" })?.value else { return nil }
        c.path = path
        c.queryItems = nil
        guard let url = c.url else { return nil }
        var r = URLRequest(url: url, timeoutInterval: post ? 90 : 5)
        r.httpMethod = post ? "POST" : "GET"
        r.setValue(token, forHTTPHeaderField: "X-Zapretify-Token")
        return r
    }

    private func refreshStatus() {
        guard let req = apiRequest("/api/status", post: false) else { return }
        URLSession.shared.dataTask(with: req) { [weak self] data, _, _ in
            guard let data, let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
            let z = (obj["zapret"] as? [String: Any])?["is_running"] as? Bool ?? false
            let t = obj["tg_proxy"] as? Bool ?? false
            DispatchQueue.main.async {
                self?.zapretOn = z
                self?.tgOn = t
                self?.updateStatusIcon()
            }
        }.resume()
    }

    private func callAPI(_ path: String) {
        guard let req = apiRequest(path, post: true) else { return }
        URLSession.shared.dataTask(with: req) { [weak self] data, resp, _ in
            DispatchQueue.main.async {
                if let http = resp as? HTTPURLResponse, http.statusCode >= 400 {
                    let msg = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }?["error"] as? String
                    self?.notify(msg ?? "Не удалось выполнить действие")
                }
                self?.refreshStatus()
            }
        }.resume()
    }

    private func notify(_ text: String) {
        let alert = NSAlert()
        alert.messageText = "Zapretify"
        alert.informativeText = text
        NSApp.activate()
        alert.runModal()
    }

    private func buildMenu() {
        let main = NSMenu()
        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "Скрыть окно", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Выйти из Zapretify", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        main.addItem(appItem)
        // Edit menu: without it ⌘C/⌘V/⌘A do not work in the web view's text fields.
        let editItem = NSMenuItem()
        let edit = NSMenu(title: "Правка")
        edit.addItem(withTitle: "Отменить", action: Selector(("undo:")), keyEquivalent: "z")
        edit.addItem(withTitle: "Вырезать", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "Копировать", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "Вставить", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "Выделить всё", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = edit
        main.addItem(editItem)
        NSApp.mainMenu = main
    }

    // MARK: helpers

    private func otherInstance() -> NSRunningApplication? {
        guard let id = Bundle.main.bundleIdentifier else { return nil }
        let me = ProcessInfo.processInfo.processIdentifier
        return NSRunningApplication.runningApplications(withBundleIdentifier: id).first { $0.processIdentifier != me }
    }

    private func fatal(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "Zapretify"
        alert.informativeText = message
        alert.alertStyle = .critical
        alert.runModal()
        NSApp.terminate(nil)
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
