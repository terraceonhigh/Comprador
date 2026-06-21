#!/bin/bash
# Chained finish for a cascade-investigation test run.
#
#   analyze  →  recover
#
# Use this as the single Pane B command after performing the drag-drop
# and Ctrl-C-ing the tail.sh pane. Prints the verdict from analyze.sh,
# then cleanly recovers the system for the next test run.
#
# Usage: sudo ./scripts/test/finish.sh <variant> [log-path]
#
# If <log-path> is omitted, finish.sh picks the most recent
# /tmp/test-<variant>-*.log automatically.

set +e

VARIANT="$1"
LOG="$2"

if [ -z "$VARIANT" ]; then
    echo "Usage: $0 <variant> [log-path]"
    echo ""
    echo "Recent test logs in /tmp:"
    /bin/ls -t /tmp/test-*.log 2>/dev/null | /usr/bin/head -5 | /usr/bin/sed 's/^/  /'
    exit 1
fi

# Auto-pick the most recent log for this variant if not specified
if [ -z "$LOG" ]; then
    LOG=$(/bin/ls -t /tmp/test-${VARIANT}-*.log 2>/dev/null | /usr/bin/head -1)
    if [ -z "$LOG" ]; then
        echo "ERROR: no /tmp/test-${VARIANT}-*.log found, and none specified."
        exit 1
    fi
    echo "  Auto-selected most recent log for variant=$VARIANT:"
    echo "    $LOG"
    echo ""
fi

HERE="$(/usr/bin/dirname "$0")"

echo "########################################################"
echo "# finish.sh — variant=$VARIANT"
echo "# Running: analyze → recover"
echo "########################################################"
echo ""

# 1. Analyze the captured log
"$HERE/analyze.sh" "$VARIANT" "$LOG"
echo ""

# 2. Recover so the system is ready for the next test
echo "########################################################"
echo "# Now recovering so the system is ready for the next test"
echo "########################################################"
echo ""
"$HERE/recover.sh"

echo ""
echo "########################################################"
echo "# finish.sh complete — system ready for next test variant"
echo "########################################################"
