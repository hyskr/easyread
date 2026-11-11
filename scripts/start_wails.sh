#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
app='desktop-wails/build/bin/EasyRead Wails.app/Contents/MacOS/EasyRead Wails'
if [[ ! -x "$app" || ! -x build/backend/easyread-backend ]]; then ./scripts/build_wails.sh; fi
export EASYREAD_WAILS_DEV_ROOT="$PWD"
exec "$app"
