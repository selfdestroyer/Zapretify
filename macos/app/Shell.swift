// Zapretify.app: a thin macOS shell around the Go core (zapretify-core).
// It starts the core, shows its web UI in a native window (WKWebView) and keeps an icon in the
// menu bar. Closing the window keeps Zapretify running in the menu bar, like the tray on Windows.
// The core lives as long as its stdin is open: quitting (or crashing) the shell stops it.

import AppKit
import WebKit

private let showNotification = Notification.Name("io.github.selfdestroyer.zapretify.show")

// Height of the UI's top bar (.top in index.html): the window buttons are centred in it.
private let headerHeight: CGFloat = 54

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler {
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

    private func buildStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        let image = NSImage(systemSymbolName: "shield.lefthalf.filled", accessibilityDescription: "Zapretify")
        image?.isTemplate = true
        statusItem.button?.image = image
        let menu = NSMenu()
        menu.addItem(withTitle: "Открыть Zapretify", action: #selector(showWindow), keyEquivalent: "").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Выйти из Zapretify", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        statusItem.menu = menu
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
