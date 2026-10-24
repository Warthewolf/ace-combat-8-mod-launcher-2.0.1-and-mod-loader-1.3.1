#!/bin/sh
# Builds ACE8 Mod Launcher for Windows and Linux into dist/. Needs Go 1.24 or newer.
set -e
cd "$(dirname "$0")"
mkdir -p dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui" -o dist/ACE8ModLauncher.exe .
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags linuxapp -trimpath -ldflags "-s -w" -o dist/ACE8ModLauncher .
echo "Built dist/ACE8ModLauncher.exe (Windows) and dist/ACE8ModLauncher (Linux)"
