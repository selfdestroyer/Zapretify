// Draws the background of the Zapretify .dmg window: 660×420 pt, drawn at the given scale.
// Usage: swift dmg-background.swift <out.png> <scale> <app-icon.icns>
// The background continues the logo's own dark blue-grey gradient (#27333f → #181e2c), so the app
// icon looks like part of the window; the glows use the logo's cyan and violet.

import AppKit

let args = CommandLine.arguments
let out = args[1], scale = CGFloat(Double(args[2]) ?? 1)
let logo = NSImage(contentsOfFile: args[3])
let W: CGFloat = 660, H: CGFloat = 420
// Icon centres in Finder coordinates (top-left origin); build-dmg.sh uses the same numbers.
let appX: CGFloat = 180, appsX: CGFloat = 480, iconY: CGFloat = 205

let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(W * scale), pixelsHigh: Int(H * scale),
                           bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                           colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
rep.size = NSSize(width: W, height: H)
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
let y = { (top: CGFloat) in H - top } // Finder top-left → AppKit bottom-left

// Background: the logo's gradient (lighter top-left, darker bottom-right) with soft glows.
let hex = { (v: Int) in NSColor(srgbRed: CGFloat(v >> 16 & 255) / 255, green: CGFloat(v >> 8 & 255) / 255, blue: CGFloat(v & 255) / 255, alpha: 1) }
NSGradient(colors: [hex(0x27333f), hex(0x181e2c)])!.draw(in: NSRect(x: 0, y: 0, width: W, height: H), angle: -35)
for (cx, cy, r, color) in [(150.0, 250.0, 300.0, hex(0x21c9e7).withAlphaComponent(0.13)), (560.0, 120.0, 320.0, hex(0x7a3fd0).withAlphaComponent(0.16))] {
    let g = NSGradient(colors: [color, .clear])!
    let c = NSPoint(x: cx, y: cy)
    g.draw(fromCenter: c, radius: 0, toCenter: c, radius: r, options: [])
}

// Title.
func text(_ s: String, _ size: CGFloat, _ weight: NSFont.Weight, _ alpha: CGFloat, top: CGFloat) {
    let p = NSMutableParagraphStyle(); p.alignment = .center
    let a: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: size, weight: weight),
                                            .foregroundColor: NSColor.white.withAlphaComponent(alpha), .paragraphStyle: p]
    NSAttributedString(string: s, attributes: a).draw(in: NSRect(x: 0, y: y(top) - size * 1.4, width: W, height: size * 1.6))
}
if let logo {
    let box = NSRect(x: W / 2 - 92, y: y(46) - 26, width: 26, height: 26)
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(roundedRect: box, xRadius: 6, yRadius: 6).addClip()
    logo.draw(in: box)
    NSGraphicsContext.restoreGraphicsState()
}
let title = NSAttributedString(string: "Zapretify", attributes: [.font: NSFont.systemFont(ofSize: 22, weight: .bold), .foregroundColor: NSColor.white])
title.draw(at: NSPoint(x: W / 2 - 58, y: y(46) - 27))
text("Перетащите Zapretify в папку «Программы»", 15, .medium, 0.92, top: 92)

// Arrow from the app to Applications.
let arrow = NSBezierPath()
let ax0 = appX + 70, ax1 = appsX - 70, ay = y(iconY)
arrow.move(to: NSPoint(x: ax0, y: ay)); arrow.line(to: NSPoint(x: ax1 - 6, y: ay))
arrow.lineWidth = 4; arrow.lineCapStyle = .round
let dash: [CGFloat] = [0.1, 12]
arrow.setLineDash(dash, count: 2, phase: 0)
NSColor.white.withAlphaComponent(0.85).setStroke(); arrow.stroke()
let head = NSBezierPath()
head.move(to: NSPoint(x: ax1 + 6, y: ay)); head.line(to: NSPoint(x: ax1 - 12, y: ay + 11)); head.line(to: NSPoint(x: ax1 - 12, y: ay - 11)); head.close()
NSColor.white.withAlphaComponent(0.85).setFill(); head.fill()

// First launch hint.
text("Если macOS не открывает Zapretify: Системные настройки → Конфиденциальность", 12, .regular, 0.78, top: 330)
text("и безопасность → «Всё равно открыть». Затем приложение спросит пароль для службы", 12, .regular, 0.78, top: 350)

NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: out))
