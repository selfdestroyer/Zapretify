#!/bin/bash
# Собирает Zapretify.app (universal: arm64 + x86_64, macOS 14+) в dist/macos/.
# Нужны Go (PATH или ~/sdk/go/bin), Xcode Command Line Tools и сеть (движок скачивается по engine.lock).
#
# Состав:
#   Contents/MacOS/Zapretify          оболочка на Swift (окно + значок в строке меню)
#   Contents/MacOS/zapretify-core     Go-ядро (интерфейс, API, Telegram-прокси, обновление баз)
#   Contents/Resources/               базы для первого запуска: strategies.json, lists/, bin/*.bin
#   Contents/Resources/daemon/        zapretifyd, utunws и скрипты установки службы
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT="$ROOT/dist/macos"
BUILD="$OUT/build"
APP="$OUT/Zapretify.app"
export PATH="$HOME/sdk/go/bin:$PATH"

VERSION=$(sed -n 's/^const Version = "\(.*\)"/\1/p' "$ROOT/core/config/config.go")
[[ -n $VERSION ]] || { echo "не найдена версия в core/config/config.go" >&2; exit 1; }
eval "$(grep -E '^ENGINE_[A-Z0-9_]+=[A-Za-z0-9._/-]*$' "$ROOT/macos/engine.lock")"

rm -rf "$APP" "$BUILD"
mkdir -p "$BUILD" "$APP/Contents/MacOS" "$APP/Contents/Resources/daemon" "$APP/Contents/Resources/lists" "$APP/Contents/Resources/bin"

echo "== Go: ядро и демон ($VERSION)"
for arch in arm64 amd64; do
  (cd "$ROOT" && GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BUILD/core-$arch" .)
  (cd "$ROOT" && GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BUILD/zapretifyd-$arch" ./cmd/zapretifyd)
done
lipo -create -output "$APP/Contents/MacOS/zapretify-core" "$BUILD/core-arm64" "$BUILD/core-amd64"
lipo -create -output "$APP/Contents/Resources/daemon/zapretifyd" "$BUILD/zapretifyd-arm64" "$BUILD/zapretifyd-amd64"

echo "== Swift: оболочка"
SDK=$(xcrun --sdk macosx --show-sdk-path)
for t in arm64-apple-macos14.0 x86_64-apple-macos14.0; do
  xcrun swiftc -O -sdk "$SDK" -target "$t" -framework AppKit -framework WebKit \
    "$ROOT/macos/app/Shell.swift" -o "$BUILD/shell-${t%%-*}"
done
lipo -create -output "$APP/Contents/MacOS/Zapretify" "$BUILD/shell-arm64" "$BUILD/shell-x86_64"

echo "== Движок utunws $ENGINE_VERSION"
ENGINE="$BUILD/utunws"
curl -fsSL -o "$ENGINE" "https://github.com/$ENGINE_REPO/releases/download/$ENGINE_VERSION/utunws"
echo "$ENGINE_SHA256_UNIVERSAL  $ENGINE" | shasum -a 256 -c - >/dev/null || { echo "SHA256 движка не совпадает с engine.lock" >&2; exit 1; }
install -m 0755 "$ENGINE" "$APP/Contents/Resources/daemon/utunws"
install -m 0644 "$ROOT/macos/scripts/install-daemon.sh" "$ROOT/macos/scripts/uninstall-daemon.sh" "$APP/Contents/Resources/daemon/"

echo "== Ресурсы"
sed "s/@VERSION@/$VERSION/g" "$ROOT/macos/app/Info.plist" > "$APP/Contents/Info.plist"
cp "$ROOT/strategies.json" "$APP/Contents/Resources/"
cp "$ROOT"/lists/*.txt "$ROOT"/lists/*.txt.backup "$APP/Contents/Resources/lists/" 2>/dev/null || cp "$ROOT"/lists/*.txt "$APP/Contents/Resources/lists/"
cp "$ROOT"/bin/*.bin "$APP/Contents/Resources/bin/"
ICONSET="$BUILD/Zapretify.iconset"; mkdir -p "$ICONSET"
for s in 16 32 128 256; do
  sips -z $s $s "$ROOT/assets/logo.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) "$ROOT/assets/logo.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/Zapretify.icns"

echo "== Подпись (ad-hoc)"
codesign -s - -f "$APP/Contents/Resources/daemon/zapretifyd" "$APP/Contents/Resources/daemon/utunws" "$APP/Contents/MacOS/zapretify-core"
codesign -s - -f "$APP"
codesign --verify --strict "$APP"

rm -rf "$BUILD"
echo "Готово: $APP"
for f in MacOS/Zapretify MacOS/zapretify-core Resources/daemon/zapretifyd Resources/daemon/utunws; do echo "$f: $(lipo -archs "$APP/Contents/$f")"; done
