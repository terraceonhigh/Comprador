#!/usr/bin/env bash
# test-e2e.sh — Full-stack NFS end-to-end test.
# NFS counterpart to test.sh (which tests the legacy WebDAV path).
# Covers list, upload, download (md5 round-trip), mkdir, delete file,
# delete directory against a live MTP device via the NFS bridge.
#
# CAVEAT (2026-06-23): written 2026-05-12, BEFORE the Galatea NFSv4 pivot.
# Its mount options assume the old willscott/go-nfs NFSv3 server. The bridge
# now serves Galatea NFSv4, so the mount invocation here needs updating to
# match MountManager.mountNFS (vers=4.0, no nolocks) before this will pass.
# Salvaged from branch test/morning-scripts and kept as the basis for a real
# automated e2e test; the test logic (list/upload/download/mkdir/delete) is
# still the right shape.
#
# Requires: phone in File Transfer mode.
# Optional: COMPRADOR_TESTING_ADB=1  adds phone-side md5 check.
#
# Usage: ./test-e2e.sh

set -euo pipefail

MOUNT=/tmp/comprador-e2e
BRIDGE_PID=""
FAILURES=0
TESTS=0

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
pass()    { TESTS=$((TESTS+1)); echo -e "${GREEN}  PASS${NC}: $1"; }
fail()    { TESTS=$((TESTS+1)); FAILURES=$((FAILURES+1)); echo -e "${RED}  FAIL${NC}: $1"; }
info()    { echo -e "${YELLOW}=>>${NC} $1"; }
section() { echo ""; echo "--- $* ---"; }

cleanup() {
    umount "$MOUNT" 2>/dev/null || true
    [ -n "$BRIDGE_PID" ] && {
        kill "$BRIDGE_PID" 2>/dev/null || true
        wait "$BRIDGE_PID" 2>/dev/null || true
    }
    rm -f /tmp/e2e-payload.bin /tmp/e2e-roundtrip.bin \
          /tmp/e2e-bridge-out.log /tmp/e2e-bridge-err.log
}
trap cleanup EXIT

echo "========================================"
echo " Comprador NFS End-to-End Tests"
echo "========================================"
echo ""

info "Building bridge..."
make bridge 2>&1 | tail -1

info "Starting bridge (--nfs)..."
mkdir -p "$MOUNT"
./build/bridge --nfs >/tmp/e2e-bridge-out.log 2>/tmp/e2e-bridge-err.log &
BRIDGE_PID=$!

PORT=""; HOST=""
for i in $(seq 1 30); do
    if grep -q '^PORT=' /tmp/e2e-bridge-out.log 2>/dev/null; then
        PORT=$(grep -m1 '^PORT=' /tmp/e2e-bridge-out.log | cut -d= -f2)
        HOST=$(grep -m1 '^HOST=' /tmp/e2e-bridge-out.log | cut -d= -f2)
        break
    fi
    if ! kill -0 "$BRIDGE_PID" 2>/dev/null; then
        echo -e "${RED}ERROR${NC}: Bridge exited early."
        cat /tmp/e2e-bridge-err.log
        exit 1
    fi
    sleep 1
done
if [ -z "$PORT" ]; then
    echo -e "${RED}ERROR${NC}: Bridge did not print PORT= within 30s"
    cat /tmp/e2e-bridge-err.log
    exit 1
fi
info "Bridge on port $PORT, host $HOST (PID $BRIDGE_PID)"

if ! mount -o port="$PORT",mountport="$PORT",nfsvers=3,nolocks,tcp \
     -t nfs "${HOST}:/" "$MOUNT" 2>/dev/null; then
    echo -e "${RED}ERROR${NC}: NFS mount failed"
    cat /tmp/e2e-bridge-err.log
    exit 1
fi
info "Mounted at $MOUNT"

STORAGE=$(ls "$MOUNT" 2>/dev/null | head -1)
[ -z "$STORAGE" ] && { echo -e "${RED}ERROR${NC}: No storage at mount root"; exit 1; }
BASE="$MOUNT/$STORAGE"
info "Storage: $STORAGE"

# ---- Tests ----

section "1. Root listing"
if ls "$MOUNT" | grep -q .; then pass "Root lists storage(s)"; else fail "Root is empty"; fi

section "2. Storage listing"
if ls "$BASE" | grep -q .; then pass "Storage '$STORAGE' has content"; else fail "Storage is empty"; fi

section "3. Upload (512 KB)"
dd if=/dev/urandom bs=1024 count=512 of=/tmp/e2e-payload.bin 2>/dev/null
PAYLOAD_MD5=$(md5 -r /tmp/e2e-payload.bin | awk '{print $1}')
UPLOAD_NAME="comprador-e2e-$(date +%s).bin"
if cp /tmp/e2e-payload.bin "$BASE/$UPLOAD_NAME" 2>/dev/null; then
    pass "Upload 512 KB file"
else
    fail "Upload 512 KB file"
fi

section "4. Uploaded file visible"
sleep 1
if [ -f "$BASE/$UPLOAD_NAME" ]; then
    pass "Uploaded file visible in listing"
else
    fail "Uploaded file not visible after upload"
fi

section "5. Download (round-trip md5)"
if cp "$BASE/$UPLOAD_NAME" /tmp/e2e-roundtrip.bin 2>/dev/null; then
    ROUNDTRIP_MD5=$(md5 -r /tmp/e2e-roundtrip.bin | awk '{print $1}')
    if [ "$PAYLOAD_MD5" = "$ROUNDTRIP_MD5" ]; then
        pass "Download md5 matches upload ($PAYLOAD_MD5)"
    else
        fail "md5 mismatch: expected $PAYLOAD_MD5, got $ROUNDTRIP_MD5"
    fi
else
    fail "Download cp failed"
fi

section "6. Phone-side md5 via ADB"
if [ "${COMPRADOR_TESTING_ADB:-0}" = "1" ]; then
    PHONE_PATH="/storage/emulated/0/$UPLOAD_NAME"
    PHONE_MD5=$(adb shell "md5sum '$PHONE_PATH'" 2>/dev/null | awk '{print $1}')
    if [ "$PHONE_MD5" = "$PAYLOAD_MD5" ]; then
        pass "Phone-side md5 matches"
    else
        fail "Phone-side md5 mismatch (expected $PAYLOAD_MD5, got '$PHONE_MD5')"
    fi
else
    echo "    (skipped — set COMPRADOR_TESTING_ADB=1 to enable)"
fi

section "7. mkdir"
TESTDIR="comprador-e2e-dir-$(date +%s)"
if mkdir "$BASE/$TESTDIR" 2>/dev/null; then pass "mkdir"; else fail "mkdir"; fi

section "8. Delete file"
if rm "$BASE/$UPLOAD_NAME" 2>/dev/null; then
    sleep 1
    if [ ! -f "$BASE/$UPLOAD_NAME" ]; then
        pass "File gone after delete"
    else
        fail "File still present after delete"
    fi
else
    fail "Delete file"
fi

section "9. Delete directory"
if rmdir "$BASE/$TESTDIR" 2>/dev/null; then
    sleep 1
    if [ ! -d "$BASE/$TESTDIR" ]; then
        pass "Directory gone after rmdir"
    else
        fail "Directory still present after rmdir"
    fi
else
    fail "rmdir"
fi

# ---- Summary ----
echo ""
echo "========================================"
if [ "$FAILURES" -eq 0 ]; then
    echo -e "${GREEN}ALL TESTS PASSED${NC} ($TESTS tests)"
else
    echo -e "${RED}$FAILURES FAILURE(S)${NC} out of $TESTS tests"
    echo ""; echo "Bridge stderr:"; tail -20 /tmp/e2e-bridge-err.log
fi
echo "========================================"
exit "$FAILURES"
