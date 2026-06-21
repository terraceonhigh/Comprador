#!/bin/bash
# Run this in a DEDICATED tmux pane (or terminal window). Captures the
# unified-log stream for Comprador + bridge processes, tees to a
# deterministic path, and stays foreground so the operator can:
#
# - Watch for live signals during the test (`Comprador bridge:` lines)
# - Ctrl-C cleanly when the test is done
# - Pass the log path to analyze.sh afterward
#
# Usage: ./scripts/test/tail.sh <variant>
#
# Writes to /tmp/test-<variant>-<HHMMSS>.log. The path is also echoed
# to stderr at startup so the operator can paste it into analyze.sh.

VARIANT="$1"
if [ -z "$VARIANT" ]; then
    echo "Usage: $0 <variant>"
    echo ""
    echo "  <variant> is the build label (matches dist-compare/Comprador-<variant>.app)"
    echo "  Output goes to /tmp/test-<variant>-<timestamp>.log"
    exit 1
fi

LOG="/tmp/test-${VARIANT}-$(/bin/date +%H%M%S).log"

echo "=== tail.sh — capturing to $LOG ==="
echo ""
echo "Foreground capture (no '&'). The log file persists after Ctrl-C."
echo ""
echo "Predicate is on subsystem == 'com.comprador.app' (the cprLog shim's"
echo "subsystem). This catches the public-marked content from AppDelegate,"
echo "BridgeProcess, DeviceSession, etc. — the NSLog→cprLog conversion"
echo "(commit landing 2026-05-18 evening) was the prerequisite."
echo ""
echo "After the test, in the other pane, run:"
echo "  ./scripts/test/analyze.sh $VARIANT $LOG"
echo ""
echo "Starting capture in 2 sec..."
sleep 2
echo "--------------------------------------------------------------"

# We OR on process == "bridge" so the bridge's own log output (if it ever
# starts using OSLog directly instead of stderr) also gets captured.
exec /usr/bin/log stream \
    --predicate 'subsystem == "com.comprador.app" OR process == "bridge"' \
    --style compact \
    | /usr/bin/tee "$LOG"
