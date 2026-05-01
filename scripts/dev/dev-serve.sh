#!/usr/bin/env bash
# Run the unsigned dev build of charon in foreground. Stops the prod
# launchd service before starting and prints a loud reminder when the
# dev process exits (any path: clean exit, Ctrl+C, kill).
#
# Invoked from the `make dev` target. Placing the trap inside this
# script (rather than inside the Makefile recipe) sidesteps Make's
# habit of aborting recipes on SIGINT before the recipe shell's
# EXIT trap can run.
set -uo pipefail

# banner_fired guards against double-printing: on SIGINT, both the
# INT trap and the (subsequent) EXIT trap want to fire. Idempotent
# trap body keeps the user from seeing the banner twice.
banner_fired=0
banner() {
    if [ "$banner_fired" -eq 1 ]; then
        return
    fi
    banner_fired=1
    printf '\n'
    printf '================================================================\n'
    printf '  Production charon proxy is NOT running.\n'
    printf '  Run:  make install\n'
    printf '  to restore the launchd service.\n'
    printf '================================================================\n'
}

# Trap every reasonable exit path. EXIT alone is unreliable in
# POSIX-mode sh on signal-induced termination; covering INT/TERM/HUP
# explicitly guarantees the banner runs once.
trap banner EXIT INT TERM HUP

echo "==> Stopping production charon service..."
~/.local/bin/charon service uninstall 2>/dev/null || true

echo ""
echo "==> Dev binary: ./bin/charon"
echo "    Vault: ~/.local/share/charon/ (separate from prod keychain)"
echo "    Listen: 127.0.0.1:8230"
echo ""

./bin/charon serve -v
