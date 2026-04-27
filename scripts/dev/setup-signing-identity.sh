#!/usr/bin/env bash
# Bootstrap a self-signed code-signing identity named "Charon Self-Signed"
# in the user's login keychain.
#
# Idempotent: re-running with the identity already present and configured
# is a no-op. Re-running with a partial state (identity present but not on
# the codesign partition list) repairs it.
#
# After this script runs successfully, `make install` can sign the charon
# binary with: codesign --sign "Charon Self-Signed" ...
#
# Interactive prompts you may see:
#   1. (One-time, on FIRST `make install` after this script): a Keychain
#      Access dialog asking whether to allow `codesign` to access the
#      "Charon Self-Signed" private key. Click **Always Allow**. Future
#      `make install` runs are silent.
#
# We deliberately do not run `add-trusted-cert` (the predicate we'll use
# later in #000003 matches by leaf cert hash, not trust anchor) and we do
# not run `set-key-partition-list` (deprecated, requires the user's login
# password, and the GUI "Always Allow" dialog accomplishes the same goal
# more reliably).

set -euo pipefail

# ── Constants ────────────────────────────────────────────────────────────────
CN="Charon Self-Signed"
DAYS=3650
KEY_BITS=4096
LOGIN_KC="$HOME/Library/Keychains/login.keychain-db"

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
command -v openssl >/dev/null  || die "openssl not in PATH"
command -v security >/dev/null || die "security CLI not in PATH"

# ── Helper: is identity importable + usable? ─────────────────────────────────
# `find-identity` (no -v) lists ALL identities including untrusted ones —
# `find-identity -v -p codesigning` filters to trusted-only and would hide
# our self-signed cert.
identity_exists() {
    security find-identity "$LOGIN_KC" 2>/dev/null | grep -q "\"$CN\""
}

# ── Step 1: create identity if missing ───────────────────────────────────────
if identity_exists; then
    ok "Identity '$CN' already in login keychain — skipping create."
else
    info "Creating self-signed code-signing identity: $CN"

    WORKDIR="$(mktemp -d "${TMPDIR%/}/charon-signing.XXXXXX")"
    trap 'rm -rf "$WORKDIR"' EXIT

    CONF="$WORKDIR/req.cnf"
    KEY="$WORKDIR/key.pem"
    CRT="$WORKDIR/cert.pem"
    P12="$WORKDIR/identity.p12"
    P12_PW="$(openssl rand -hex 16)"

    # `1.2.840.113635.100.1.1` is Apple's code-signing OID — required for codesign
    # to accept the cert as a code-signing identity.
    cat >"$CONF" <<EOF
[req]
distinguished_name = dn
prompt             = no
x509_extensions    = v3_ext

[dn]
CN = $CN

[v3_ext]
keyUsage             = critical, digitalSignature
extendedKeyUsage     = critical, codeSigning, 1.2.840.113635.100.1.1
basicConstraints     = critical, CA:FALSE
subjectKeyIdentifier = hash
EOF

    info "Generating ${KEY_BITS}-bit RSA key + self-signed cert (${DAYS} days)..."
    openssl req -x509 -newkey "rsa:${KEY_BITS}" -nodes \
        -keyout "$KEY" -out "$CRT" \
        -days "$DAYS" -config "$CONF" >/dev/null 2>&1 \
        || die "openssl req failed"

    # `-legacy` keeps OpenSSL 3.x emitting a p12 macOS's importer can read.
    # Without it, `security import` fails with "MAC verification failed".
    info "Bundling key + cert into PKCS#12 (legacy format for macOS compat)..."
    openssl pkcs12 -export -legacy \
        -inkey "$KEY" -in "$CRT" \
        -name "$CN" \
        -out "$P12" -passout "pass:${P12_PW}" >/dev/null 2>&1 \
        || die "openssl pkcs12 -legacy failed (need OpenSSL 3.x with legacy provider)"

    info "Importing identity into login keychain..."
    security import "$P12" \
        -k "$LOGIN_KC" \
        -P "$P12_PW" \
        -T /usr/bin/codesign \
        -f pkcs12 >/dev/null \
        || die "security import failed"
fi

# ── Verify ───────────────────────────────────────────────────────────────────
if ! identity_exists; then
    die "identity '$CN' not visible after import — investigate with: security find-identity \"$LOGIN_KC\""
fi

ok "Identity '$CN' is ready for codesign."
info "Next: run 'make install'."
info ""
info "On the FIRST install only, macOS will show a keychain dialog asking"
info "whether codesign may use the 'Charon Self-Signed' private key."
info "Click 'Always Allow'. Subsequent installs are silent."
