#!/bin/bash
# Chained setup for a cascade-investigation test run.
#
#   recover  →  clean  →  install  →  launch
#
# Use this as the single Pane B command. After it completes successfully,
# perform the drag-drop manually in Finder, then run finish.sh.
#
# Usage: sudo ./scripts/test/setup.sh <variant>
#
# Variant matches dist-compare/Comprador-<variant>.app — e.g.
#   sudo ./scripts/test/setup.sh prod
#   sudo ./scripts/test/setup.sh notarized
#   sudo ./scripts/test/setup.sh 92d4e6d5
#
# Run under sudo so the individual scripts don't each prompt for
# credentials. Internally, recover.sh + install.sh need root for
# killall/umount/cp into /Applications. clean.sh runs as the
# invoking user — sudo's --preserve-env keeps HOME pointing at the
# operator's home directory so the right caches get wiped.

set -e

VARIANT="$1"
if [ -z "$VARIANT" ]; then
    echo "Usage: $0 <variant>"
    echo ""
    echo "Available variants in dist-compare/:"
    /bin/ls -d dist-compare/Comprador-*.app 2>/dev/null \
        | /usr/bin/sed 's|dist-compare/Comprador-||; s|\.app$||; s|^|  |'
    exit 1
fi

# Detect the directory this script lives in, so chained scripts resolve
# even if the operator invoked us via an absolute path.
HERE="$(/usr/bin/dirname "$0")"

# Verify the variant exists before doing any destructive work.
if [ ! -d "dist-compare/Comprador-${VARIANT}.app" ]; then
    echo "ERROR: dist-compare/Comprador-${VARIANT}.app not found"
    exit 1
fi

echo "########################################################"
echo "# setup.sh — variant=$VARIANT"
echo "# Running: recover → clean → install → launch"
echo "########################################################"
echo ""

# 1. Recovery (always safe to run, idempotent)
"$HERE/recover.sh"
echo ""

# 2. Clean user state (refuses if mount still active — recover.sh should
# have killed any mount, so this should always pass after step 1).
"$HERE/clean.sh"
echo ""

# 3. Install the requested variant
"$HERE/install.sh" "$VARIANT"
echo ""

# 4. Launch + poll for mount
"$HERE/launch.sh"

# launch.sh exits 1 if mount doesn't appear within 30 sec; that
# bubbles up through set -e. If we got here, the mount is live.
echo ""
echo "########################################################"
echo "# setup.sh complete — variant=$VARIANT mounted and ready"
echo "########################################################"
echo ""
echo "Now:"
echo "  1. Perform the drag-drop test manually in Finder."
echo "  2. Observe the behavior (clean / Server connection Interrupted / cascade-freeze)."
echo "  3. Ctrl-C the tail.sh pane to stop log capture."
echo "  4. Run: ./scripts/test/finish.sh $VARIANT /tmp/test-${VARIANT}-<HHMMSS>.log"
echo ""
