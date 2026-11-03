#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm run build:editor
.venv/bin/python scripts/build_backend.py
cd desktop-wails
CGO_LDFLAGS='-framework UniformTypeIdentifiers' go build -tags production -o 'build/bin/EasyRead Wails.app/Contents/MacOS/EasyRead Wails' .
app='build/bin/EasyRead Wails.app'
mkdir -p "$app/Contents/Resources/backend"
cp ../build/backend/easyread-backend "$app/Contents/Resources/backend/"
cp build/darwin/Info.plist "$app/Contents/Info.plist"
codesign --force --deep --sign - "$app"
