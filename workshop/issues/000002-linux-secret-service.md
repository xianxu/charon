---
id: 000002
status: open
deps: [000001]
github_issue:
created: 2026-04-24
updated: 2026-04-24
---

# Linux secret service backend

## Problem

Charon's vault currently only supports macOS Keychain (via `security` CLI). Linux users need a backend that uses GNOME Keyring / KDE Wallet via the D-Bus Secret Service API.

## Spec

- Implement `vault.Store` interface for Linux
- Use D-Bus Secret Service API (freedesktop.org standard)
- Pure Go (no CGo) — use a D-Bus library like `github.com/godbus/dbus`
- Same JSON blob format as keychain backend
- Cross-compile and test on Linux

## Plan

- [ ] D-Bus Secret Service backend (`internal/vault/secretservice/`)
- [ ] Build tag: `//go:build linux`
- [ ] Auto-select backend based on OS in `cmd/charon/main.go`
- [ ] Cross-compile test: `GOOS=linux go build ./...`
- [ ] Integration test on Linux (CI or container)
