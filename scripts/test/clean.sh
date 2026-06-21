#!/bin/bash
# Wipe Comprador's user-side state (caches, prefs, app support dir, http
# storages). Does NOT touch /Applications/Comprador.app (use install.sh
# for that). Does NOT kill running processes (use recover.sh first).
#
# Run before each test if you want a fresh first-launch experience
# (welcome window will reappear, no remembered menu bar position, etc.).

set +e
echo "=== clean.sh — wiping Comprador user state ==="

# SAFETY CHECK 1 (fast path): refuse to run if any Comprador NFS mount
# is active. Walking into a still-mounted directory under hard,nointr
# semantics wedges the rmtree process and cascades to every system
# service that touches the path.
#
# If this fires, run scripts/test/recover.sh first to force-unmount,
# THEN re-run this script.
if /sbin/mount | /usr/bin/grep -q '\.local:/'; then
    echo ""
    echo "REFUSING TO RUN — Comprador NFS mount is still active:"
    /sbin/mount | /usr/bin/grep '\.local:/' | /usr/bin/sed 's/^/  /'
    echo ""
    echo "Walking into a mounted hard,nointr share will wedge this shell"
    echo "and cascade to system services. Run recover.sh first:"
    echo ""
    echo "  sudo ./scripts/test/recover.sh"
    echo ""
    exit 1
fi

# SAFETY CHECK 2 (strict): refuse to run if ~/Library/Application Support/
# Comprador/Volumes/ contains any entries — even when nothing shows in
# mount(8), residual NFS dirent state can survive a partial recover and
# wedge stat() during rmtree.
#
# History: the architect hit this on 2026-05-18 evening — twice. The
# first time the mount was still live (Check 1 below now catches that).
# The second time the kernel had reaped the mount-table entry but the
# Volumes/XQ-BT52 dirent still pointed at a dead NFS file handle, so
# Check 1 passed and shutil.rmtree wedged when it stat()'d the
# subdirectory. Letter 15 (correspondence/15-the-day-the-harness-bit-twice)
# is the post-mortem.
#
# Implementation detail: listdir reads the parent's dirent stream
# without statting children, so it's the right primitive — but we still
# fork a child to time-bound it, because if even readdir wedges (a
# pathological case we have not seen but cannot rule out) we want to
# refuse rather than join the cascade we're investigating.
/usr/bin/python3 << 'EOF'
import os, sys, signal, time, shutil

vol_dir = os.path.expanduser('~/Library/Application Support/Comprador/Volumes')

def probe_volumes():
    """Return (status, entries) where status is:
      'absent'   — Volumes/ does not exist
      'empty'    — Volumes/ exists and is empty
      'occupied' — Volumes/ has entries (entries holds the names)
      'timeout'  — listdir did not complete within 5 seconds
      'error'    — listdir raised
    """
    if not os.path.isdir(vol_dir):
        return ('absent', [])

    r, w = os.pipe()
    pid = os.fork()
    if pid == 0:
        os.close(r)
        try:
            entries = os.listdir(vol_dir)
            os.write(w, b'\n'.join(e.encode() for e in entries))
            os._exit(0 if not entries else 2)
        except Exception:
            os._exit(3)

    os.close(w)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        wpid, status = os.waitpid(pid, os.WNOHANG)
        if wpid != 0:
            code = (status >> 8) & 0xFF
            data = b''
            try:
                while chunk := os.read(r, 4096):
                    data += chunk
            except OSError:
                pass
            os.close(r)
            entries = data.decode(errors='replace').split('\n') if data else []
            if code == 0:
                return ('empty', [])
            if code == 2:
                return ('occupied', entries)
            return ('error', [])
        time.sleep(0.1)

    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    os.close(r)
    return ('timeout', [])

status, entries = probe_volumes()

if status == 'timeout':
    print('REFUSING TO RUN — listdir on Volumes/ timed out after 5s.')
    print('This indicates stale NFS dirent state in the kernel that')
    print('survived recover.sh. Try:')
    print('  1. sudo ./scripts/test/recover.sh (again, with sudo)')
    print('  2. If still wedged, a reboot is the only clean exit.')
    sys.exit(1)

if status == 'occupied':
    print('REFUSING TO RUN — Volumes/ has entries:')
    for e in entries:
        print(f'  {e}')
    print()
    print('Even when nothing appears in mount(8), a stale NFS dirent here')
    print('will wedge stat() during rmtree. Run recover.sh first:')
    print()
    print('  sudo ./scripts/test/recover.sh')
    sys.exit(1)

if status == 'error':
    print('REFUSING TO RUN — listdir on Volumes/ errored unexpectedly.')
    print('Investigate manually before re-running.')
    sys.exit(1)

# status is 'absent' or 'empty' — Volumes/ has no children to descend
# into, so rmtree of the parent is safe.

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
