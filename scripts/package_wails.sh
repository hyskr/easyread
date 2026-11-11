#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/build_wails.sh
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
ditto 'desktop-wails/build/bin/EasyRead Wails.app' "$stage/EasyRead Wails.app"
ln -s /Applications "$stage/Applications"
mkdir -p dist/wails
hdiutil create -volname 'EasyRead Wails' -srcfolder "$stage" -ov -format UDZO dist/wails/EasyRead-Wails-arm64.dmg
ditto -c -k --sequesterRsrc --keepParent "$stage/EasyRead Wails.app" dist/wails/EasyRead-Wails-arm64.zip
