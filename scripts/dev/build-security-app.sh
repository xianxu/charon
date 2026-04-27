#!/usr/bin/env bash
# Assemble "Charon Security.app" — a minimal app bundle wrapping the
# charon-security CLI binary, signed with the user's Charon Self-Signed
# identity, with its own bundle ID (com.charon.security) so TCC
# attributes permissions to it specifically.
#
# Why a .app and not a bare CLI:
#
#   TCC keys permissions on the *responsible code*. A bare Mach-O CLI
#   run from Terminal.app gets attributed to com.apple.Terminal in TCC.
#   That means when charon-security asks for Full Disk Access (to read
#   TCC.db and audit other apps' grants), the user would be prompted
#   to grant FDA to Terminal — and the auto-revoke at end-of-run would
#   nuke Terminal's FDA. A .app bundle with its own bundle ID solves
#   this: TCC sees `com.charon.security` as a distinct actor.
#
# Why hardened runtime: the security tool is exemplary. We tell users
# to prefer apps without weakening entitlements; we ship with hardened
# runtime on, no entitlements declared.
#
# Idempotent: re-running rebuilds Info.plist, re-copies the binary,
# re-signs. The bundle ID + leaf-cert pair is stable across rebuilds
# (same property charon's M4 ACL relies on), so existing TCC grants
# survive a re-install.

set -euo pipefail

# ── Inputs ───────────────────────────────────────────────────────────────────
BUNDLE_ID="${BUNDLE_ID:-com.charon.security}"
APP_NAME="${APP_NAME:-Charon Security}"
APP_DIR="${APP_DIR:-$HOME/Applications/$APP_NAME.app}"
SIGN_IDENTITY="${SIGN_IDENTITY:-Charon Self-Signed}"
BINARY="${BINARY:-bin/charon-security}"
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"

# ── Output helpers ───────────────────────────────────────────────────────────
if [ -t 1 ]; then
    GREEN=$'\033[1;32m'; RED=$'\033[1;31m'; CYAN=$'\033[1;36m'
    YELLOW=$'\033[1;33m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; CYAN=""; YELLOW=""; RESET=""
fi
info() { printf "%s==> %s%s\n" "$CYAN" "$*" "$RESET"; }
ok()   { printf "%s  [ok] %s%s\n" "$GREEN" "$*" "$RESET"; }
warn() { printf "%s  [!] %s%s\n" "$YELLOW" "$*" "$RESET" >&2; }
die()  { printf "%serror: %s%s\n" "$RED" "$*" "$RESET" >&2; exit 1; }

# ── Sanity ───────────────────────────────────────────────────────────────────
[[ "$(uname -s)" == "Darwin" ]] || die "macOS-only (uname is $(uname -s))"
[[ -f "$BINARY"            ]]   || die "binary not found: $BINARY  (run 'make security-build' first)"
command -v codesign >/dev/null  || die "codesign not in PATH"

# Use the broad `find-identity` (no -v -p codesigning). Charon's
# self-signed cert is intentionally untrusted by the system trust
# store — codesign signs with it fine, but the strict policy filter
# excludes it. Match the same check that Makefile.local's `sign:`
# target uses.
if ! security find-identity "$HOME/Library/Keychains/login.keychain-db" \
        2>/dev/null | grep -q "\"$SIGN_IDENTITY\""; then
    die "signing identity '$SIGN_IDENTITY' not found in login keychain. Run 'make signing-identity' first."
fi

# ── Assemble bundle ──────────────────────────────────────────────────────────
info "Building $APP_NAME.app at $APP_DIR"

mkdir -p "$APP_DIR/Contents/MacOS"

# Copy the binary into the bundle. CFBundleExecutable points at this name.
cp "$BINARY" "$APP_DIR/Contents/MacOS/charon-security"
ok "binary copied"

# Write Info.plist. LSUIElement=true makes this a background app — no
# Dock icon, no menu bar entry, no app switcher presence. The bundle
# exists for TCC attribution, not for end-user GUI interaction.
cat > "$APP_DIR/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleIdentifier</key>
    <string>$BUNDLE_ID</string>
    <key>CFBundleName</key>
    <string>$APP_NAME</string>
    <key>CFBundleExecutable</key>
    <string>charon-security</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>$VERSION</string>
    <key>CFBundleVersion</key>
    <string>$VERSION</string>
    <key>LSUIElement</key>
    <true/>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
</dict>
</plist>
EOF
ok "Info.plist written  ($BUNDLE_ID)"

# ── Sign ─────────────────────────────────────────────────────────────────────
# --options runtime turns on the hardened runtime. We declare no
# weakening entitlements — the security tool should be its own example.
#
# This will trigger a Keychain Access "Allow/Deny" prompt for the
# Charon Self-Signed private key. Click Allow (single-use), NOT Always
# Allow — the same rule the M3 setup script warns about.
info "Signing with '$SIGN_IDENTITY' (hardened runtime, no entitlements)"
warn "If a Keychain Access dialog pops up, click Allow — never 'Always Allow'."
codesign --force \
    --sign "$SIGN_IDENTITY" \
    --identifier "$BUNDLE_ID" \
    --options runtime \
    "$APP_DIR"
codesign --verify --verbose=1 "$APP_DIR" 2>&1 | sed 's/^/    /'
ok "signed and verified"

# ── Confirmation ─────────────────────────────────────────────────────────────
info "Bundle layout:"
find "$APP_DIR" -type f | sed 's/^/    /'

cat <<EOF

${GREEN}Installed${RESET} $APP_NAME.app at:
    $APP_DIR

Run the audit:
    make security
    # or:
    "$APP_DIR/Contents/MacOS/charon-security" check

To uninstall:
    make security-uninstall
EOF
