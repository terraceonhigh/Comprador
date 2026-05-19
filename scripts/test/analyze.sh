#!/bin/bash
# Grep the captured log for the patterns that distinguish hypotheses about
# the cascade bug, output a verdict line. Run AFTER you've Ctrl-C'd tail.sh.
#
# Usage: ./scripts/test/analyze.sh <variant> <log-path>
#
# The log path is the file tail.sh wrote to (echoed at the top of that
# script's output, also at /tmp/test-<variant>-<HHMMSS>.log).

set +e

VARIANT="$1"
LOG="$2"

if [ -z "$VARIANT" ] || [ -z "$LOG" ]; then
    echo "Usage: $0 <variant> <log-path>"
    echo ""
    echo "Recent test logs in /tmp:"
    /bin/ls -t /tmp/test-*.log 2>/dev/null | /usr/bin/head -5 | /usr/bin/sed 's/^/  /'
    exit 1
fi

if [ ! -f "$LOG" ]; then
    echo "ERROR: log file $LOG not found"
    exit 1
fi

echo "=== analyze.sh — variant=$VARIANT log=$LOG ==="
echo ""

# Counts
count_total=$(/usr/bin/wc -l < "$LOG" | /usr/bin/tr -d ' ')
count_bridge_lines=$(/usr/bin/grep -c "Comprador bridge:" "$LOG" 2>/dev/null || echo 0)
count_prefetch=$(/usr/bin/grep -c "cache.beginPrefetch" "$LOG" 2>/dev/null || echo 0)
count_jukebox=$(/usr/bin/grep -ci "JUKEBOX" "$LOG" 2>/dev/null || echo 0)
count_filetransfer_notif=$(/usr/bin/grep -c "Adding notification request CE85" "$LOG" 2>/dev/null || echo 0)
count_bridge_ready=$(/usr/bin/grep -c "Bridge ready" "$LOG" 2>/dev/null || echo 0)
count_force_kill=$(/usr/bin/grep -c "Force killing bridge" "$LOG" 2>/dev/null || echo 0)
count_open_attempt=$(/usr/bin/grep -c "Open attempt" "$LOG" 2>/dev/null || echo 0)

echo "  Signal counts:"
printf "    %-50s %s\n" "total log lines:" "$count_total"
printf "    %-50s %s\n" "'Comprador bridge: ...' (NSLog'd stderr):" "$count_bridge_lines"
printf "    %-50s %s\n" "'cache.beginPrefetch' (prefetch dispatched):" "$count_prefetch"
printf "    %-50s %s\n" "'JUKEBOX' (>50 MB READ hit threshold):" "$count_jukebox"
printf "    %-50s %s\n" "'Open attempt' (bridge libmtp claim):" "$count_open_attempt"
printf "    %-50s %s\n" "'Bridge ready' (mount succeeded):" "$count_bridge_ready"
printf "    %-50s %s\n" "'Force killing bridge' (Swift parent gave up):" "$count_force_kill"
printf "    %-50s %s\n" "'Adding notification request CE85' ('Check phone'):" "$count_filetransfer_notif"
echo ""

# Verdict heuristic
echo "  Verdict:"
if [ "$count_filetransfer_notif" -gt 0 ] && [ "$count_bridge_ready" -eq 0 ]; then
    echo "    BRIDGE NEVER CLAIMED USB."
    echo "    The 'Check your phone' notification fired $count_filetransfer_notif time(s)."
    echo "    Likely ptpcamerad race. Test is inconclusive for the cascade hypothesis."
    echo "    Recovery: unplug + replug phone, re-launch."
elif [ "$count_bridge_ready" -gt 0 ] && [ "$count_prefetch" -gt 0 ]; then
    echo "    PREFETCH FIRED."
    echo "    Bridge claimed USB ($count_bridge_ready 'Bridge ready' hit) and dispatched"
    echo "    $count_prefetch prefetch operation(s). If the operator observed a cascade or"
    echo "    'Server connection Interrupted' on the Finder side, the bug repro'd on this run."
    echo "    Hypothesis A territory (mechanism fires when the prefetch path runs)."
elif [ "$count_bridge_ready" -gt 0 ] && [ "$count_prefetch" -eq 0 ]; then
    echo "    BRIDGE RAN, PREFETCH DID NOT FIRE."
    echo "    Mount succeeded and stayed up, but no >50 MB READ hit the bridge's JUKEBOX"
    echo "    path during the test window. If the operator observed a clean drag, the run"
    echo "    confirms hypothesis B (Finder probes are the gating trigger, not our code path"
    echo "    running unconditionally)."
else
    echo "    INCONCLUSIVE."
    echo "    Operator should compare the signal counts above with the observed Finder"
    echo "    behavior and decide which hypothesis the run supports."
fi

echo ""
echo "  Last 10 'Comprador bridge: ...' lines (most informative substream):"
/usr/bin/grep "Comprador bridge:" "$LOG" | /usr/bin/tail -10 | /usr/bin/sed 's/^/    /'

echo ""
echo "=== analyze.sh done ==="
