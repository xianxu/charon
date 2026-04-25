#!/bin/bash
# Integration test: verify Charon can proxy Gmail API requests.
# Prerequisites: charon serve running, Google account authenticated.
#
# Usage:
#   ./test/integration/gmail_test.sh [account-email]
#
# This test verifies the full chain:
#   charon run → HTTPS_PROXY → CONNECT → TLS interception → token injection → Gmail API

set -euo pipefail

ACCOUNT="${1:-xianxu@gmail.com}"
CHARON="./bin/charon"

echo "=== Charon Gmail Integration Test ==="
echo "Account: $ACCOUNT"

# Check charon binary exists.
if [ ! -f "$CHARON" ]; then
    echo "FAIL: $CHARON not found. Run 'make build' first."
    exit 1
fi

# Check proxy is running.
if ! "$CHARON" status 2>/dev/null | grep -q "ok"; then
    echo "FAIL: proxy not running. Start with '$CHARON serve'."
    exit 1
fi

# Test 1: Gmail profile
echo -n "Test 1: GET /users/me/profile ... "
PROFILE=$("$CHARON" run -- curl -s \
    -H "X-Charon-Account: $ACCOUNT" \
    "https://gmail.googleapis.com/gmail/v1/users/me/profile")

if echo "$PROFILE" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['emailAddress']" 2>/dev/null; then
    EMAIL=$(echo "$PROFILE" | python3 -c "import sys,json; print(json.load(sys.stdin)['emailAddress'])")
    echo "PASS ($EMAIL)"
else
    echo "FAIL"
    echo "$PROFILE"
    exit 1
fi

# Test 2: Gmail thread list (1 result)
echo -n "Test 2: GET /users/me/threads (limit 1) ... "
THREADS=$("$CHARON" run -- curl -s \
    -H "X-Charon-Account: $ACCOUNT" \
    "https://gmail.googleapis.com/gmail/v1/users/me/threads?maxResults=1")

if echo "$THREADS" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'threads' in d" 2>/dev/null; then
    COUNT=$(echo "$THREADS" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['threads']))")
    echo "PASS ($COUNT thread)"
else
    echo "FAIL"
    echo "$THREADS"
    exit 1
fi

echo "=== All tests passed ==="
