#!/bin/bash
# Launch /Applications/Comprador.app, poll for the NFS mount to appear,
# report success/failure. If the bridge can't claim USB (ptpcamerad race),
# this script will time out and tell the operator to unplug+replug.
#
# Usage: ./scripts/test/launch.sh
#
# Run AFTER install.sh and AFTER tail.sh is capturing in another pane.

set +e

echo "=== launch.sh ==="

# Verify install
if [ ! -d /Applications/Comprador.app ]; then
    echo "ERROR: /Applications/Comprador.app missing. Run install.sh first."
    exit 1
fi

# Launch
echo "  opening /Applications/Comprador.app..."
/usr/bin/open /Applications/Comprador.app

# Poll for mount up to 30 sec
echo "  waiting for NFS mount (up to 30 sec)..."
for i in $(seq 1 30); do
    MNT=$(/sbin/mount | /usr/bin/grep '\.local:/' | /usr/bin/awk '{print $3}' | /usr/bin/head -1)
    if [ -n "$MNT" ]; then
        echo ""
        echo "  Mount appeared after ${i}s: $MNT"
        echo ""
        echo "  Bridge process(es):"
        /usr/bin/pgrep -lf "Resources/bridge" 2>/dev/null | /usr/bin/sed 's/^/    /'
        echo ""
        echo "=== Mount live. PERFORM DRAG-DROP TEST NOW. ==="
        echo ""
        echo "When done (success OR failure), in this pane run:"
        echo "  ./scripts/test/analyze.sh <variant> <log-path-from-tail.sh>"
        exit 0
    fi
    sleep 1
done

echo ""
echo "FAILURE: mount did not appear within 30 sec."
echo ""
echo "Most likely: bridge couldn't claim USB (ptpcamerad won the race)."
echo "Check the tail.sh pane for 'Adding notification request CE85' — that's"
echo "the 'Check your phone' file-transfer notification fire."
echo ""
echo "Recovery:"
echo "  1. Unplug the phone"
echo "  2. Wait 3 sec"
echo "  3. Plug back in, select File Transfer on phone"
echo "  4. Comprador's IOKit watcher should fire and attempt mount again"
echo ""
echo "If you're stuck, run scripts/test/recover.sh and start over."
exit 1
