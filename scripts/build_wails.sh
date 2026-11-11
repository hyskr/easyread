#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
export WAILS_VERSION="${WAILS_VERSION:-1.4.22}"
npm run build:editor
node scripts/build_backend.cjs
go run scripts/wails_icon.go
cd desktop-wails
CGO_LDFLAGS='-framework UniformTypeIdentifiers' go build -ldflags "-X main.version=${WAILS_VERSION:-1.4.22}" -tags production -o 'build/bin/EasyRead Wails.app/Contents/MacOS/EasyRead Wails' .
app='build/bin/EasyRead Wails.app'
mkdir -p "$app/Contents/Resources/backend"
cp -R ../build/backend/. "$app/Contents/Resources/backend/"
cp build/darwin/Info.plist "$app/Contents/Info.plist"
iconset=$(mktemp -d)/icon.iconset
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" build/appicon.png --out "$iconset/icon_${size}x${size}.png" >/dev/null
  double=$((size * 2))
  sips -z "$double" "$double" build/appicon.png --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/iconfile.icns"
rm -rf "$(dirname "$iconset")"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString ${WAILS_VERSION:-1.4.22}" "$app/Contents/Info.plist"
node -e 'require("../scripts/mac_sign.cjs").default({electronPlatformName:"darwin",appOutDir:"build/bin",packager:{appInfo:{productFilename:"EasyRead Wails"}}}).catch(e=>{console.error(e);process.exit(1)})'
