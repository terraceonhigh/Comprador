#!/bin/bash
# Install a .app variant from dist-compare/ to /Applications/Comprador.app.
# Strips quarantine xattr (so the install behaves like a fresh local build,
# not a downloaded one — important for separating notarization-vs-quarantine
# variables).
#
# Usage: ./scripts/test/install.sh <variant>
# Where <variant> is one of dist-compare/Comprador-*.app's middle names.

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

SRC="dist-compare/Comprador-${VARIANT}.app"
DEST="/Applications/Comprador.app"

if [ ! -d "$SRC" ]; then
    echo "ERROR: $SRC not found"
    exit 1
fi

echo "=== install.sh — installing variant=$VARIANT ==="

# 1. Kill any running instance
sudo /usr/bin/killall -9 Comprador 2>/dev/null || true

# 2. Remove the old install
sudo /bin/rm -rf "$DEST"

# 3. Copy the variant
sudo /bin/cp -R "$SRC" "$DEST"

# 4. Strip quarantine so Gatekeeper doesn't do first-launch verification.
# This isolates the build-identity variable from the "downloaded from web"
# behavior; if quarantine matters, the operator can test that separately.
sudo /usr/bin/xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true

# 5. Verify
echo ""
echo "  Verification:"
SHA=$(/usr/bin/shasum -a 256 "$DEST/Contents/MacOS/Comprador" | /usr/bin/awk '{print $1}')
VER=$(/usr/bin/defaults read "$DEST/Contents/Info.plist" CFBundleVersion 2>/dev/null || echo "?")
SHORTVER=$(/usr/bin/defaults read "$DEST/Contents/Info.plist" CFBundleShortVersionString 2>/dev/null || echo "?")
echo "    Path:                       $DEST"
echo "    Binary sha256:              $SHA"
echo "    CFBundleShortVersionString: $SHORTVER"
echo "    CFBundleVersion:            $VER"

echo ""
echo "=== install.sh done — ready to launch ==="
