#!/usr/bin/env python3
"""Exercise the probe_volumes() helper from clean.sh against absent,
empty, and occupied directory states. Mirrors the clean.sh logic line
by line; if this passes the safety check in clean.sh has the right
shape against the three named cases from letter 15."""

import os, sys, signal, time, tempfile, shutil

# --- begin verbatim-from-clean.sh ---
def probe_volumes_at(vol_dir):
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
# --- end verbatim ---

def case(label, expected_status, setup):
    with tempfile.TemporaryDirectory() as base:
        vol = os.path.join(base, 'Volumes')
        setup(vol)
        status, entries = probe_volumes_at(vol)
        ok = status == expected_status
        marker = 'OK  ' if ok else 'FAIL'
        print(f'  {marker} {label}: status={status} entries={entries}')
        return ok

def setup_absent(vol):
    pass  # don't create Volumes/ at all

def setup_empty(vol):
    os.makedirs(vol)

def setup_occupied(vol):
    os.makedirs(vol)
    os.makedirs(os.path.join(vol, 'XQ-BT52'))
    os.makedirs(os.path.join(vol, 'Pixel-6'))

results = [
    case('absent  ', 'absent',   setup_absent),
    case('empty   ', 'empty',    setup_empty),
    case('occupied', 'occupied', setup_occupied),
]

print()
print(f'{sum(results)}/{len(results)} cases passed')
sys.exit(0 if all(results) else 1)
