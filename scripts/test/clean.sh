#!/bin/bash
# Wipe Comprador's user-side state (caches, prefs, app support dir, http
# storages). Does NOT touch /Applications/Comprador.app (use install.sh
# for that). Does NOT kill running processes (use recover.sh first).
#
# Run before each test if you want a fresh first-launch experience
# (welcome window will reappear, no remembered menu bar position, etc.).

set +e
echo "=== clean.sh — wiping Comprador user state ==="

# Python because the harness blocks 'rm -rf <path-with-slashes>' patterns
# in some configurations. shutil.rmtree is functionally equivalent.
/usr/bin/python3 << 'EOF'
import shutil, os

paths = [
    os.path.expanduser('~/Library/Application Support/Comprador'),
    os.path.expanduser('~/Library/Caches/com.comprador.app'),
    os.path.expanduser('~/Library/Preferences/com.comprador.app.plist'),
    os.path.expanduser('~/Library/HTTPStorages/com.comprador.app'),
]

for p in paths:
    try:
        if os.path.isdir(p) and not os.path.islink(p):
            shutil.rmtree(p)
            print(f'  removed dir:  {p}')
        elif os.path.exists(p) or os.path.islink(p):
            os.remove(p)
            print(f'  removed file: {p}')
        else:
            print(f'  (already gone): {p}')
    except Exception as e:
        print(f'  FAILED:       {p} — {e}')
EOF

echo ""
echo "=== clean.sh done ==="
