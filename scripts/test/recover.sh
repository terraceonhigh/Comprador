#!/bin/bash
# Force-recover system state. Always safe to run. Idempotent.
# Kills running Comprador + bridge + dns-sd orphans, force-unmounts any
# Comprador NFS mounts, restarts Finder. ~5 sec total.
#
# Run before each new test run, AND immediately after any cascade event.
# Requires sudo for the destructive bits (killall -9, umount -f).

set +e
echo "=== recover.sh — forcing clean Comprador state ==="

# 1. Kill the app
echo "  killing Comprador (if running)..."
sudo /usr/bin/killall -9 Comprador 2>/dev/null

# 2. Kill the bridge subprocess
echo "  killing bridge (if running)..."
sudo /usr/bin/killall -9 bridge 2>/dev/null

# 3. Kill mDNS announcer orphans. dns-sd processes may have been reparented
# to launchd when the bridge crashed; they keep advertising
# Comprador-<device>.local on the network even after Comprador is gone, and
# the next test run gets confused by the duplicate registration. SIGKILL +
# verify-loop because SIGTERM was leaving stuck dns-sd's after the
# 2026-05-18 evening tests.
echo "  killing dns-sd orphans (SIGKILL + verify)..."
for round in 1 2 3; do
    pids=$(/usr/bin/pgrep -f "dns-sd.*Comprador" 2>/dev/null)
    if [ -z "$pids" ]; then
        echo "    round $round: no dns-sd orphans"
        break
    fi
    echo "    round $round: killing $pids"
    echo "$pids" | while read pid; do
        kill -9 "$pid" 2>/dev/null
    done
    sleep 1
done

# 3b. Final verification — if anything dns-sd-shaped is still up, surface it
# rather than continuing silently.
stragglers=$(/usr/bin/pgrep -lf "dns-sd.*Comprador" 2>/dev/null)
if [ -n "$stragglers" ]; then
    echo "    WARNING: dns-sd processes survived SIGKILL after 3 rounds:"
    echo "$stragglers" | /usr/bin/sed 's/^/      /'
fi

# 4. Force-unmount any per-device NFS mount Comprador left behind.
# The mount-output mount-point may contain spaces ("Library/Application
# Support/..."), so awk $3 mangles the path — instead, regex-extract
# everything between " on " and the trailing " (options)". Without this,
# the previous awk-$3 implementation silently umount-failed on the
# real-world path and clean.sh's subdir-existence gate had to catch
# the still-live mount (which it did, on 2026-05-18 evening, exposing
# this bug).
echo "  force-unmounting Comprador NFS mounts..."
/sbin/mount | /usr/bin/grep '\.local:/' \
            | /usr/bin/sed -E 's/^[^ ]+ on (.+) \([^()]*\)$/\1/' \
            | while IFS= read -r mp; do
    if [ -z "$mp" ]; then continue; fi
    echo "    umount -f $mp"
    if ! sudo /sbin/umount -f "$mp"; then
        echo "      WARNING: umount -f failed for $mp"
    fi
done

# 5. Restart Finder (kills any process still wedged on the dead mount)
echo "  restarting Finder..."
/usr/bin/killall Finder 2>/dev/null

# 6. Settle window
sleep 2

# Verify
echo ""
echo "  Verification:"
mounts=$(/sbin/mount | /usr/bin/grep '\.local:/' | /usr/bin/wc -l | /usr/bin/tr -d ' ')
procs=$(/usr/bin/pgrep -l Comprador 2>/dev/null | /usr/bin/wc -l | /usr/bin/tr -d ' ')
bridge_procs=$(/usr/bin/pgrep -lf "Resources/bridge" 2>/dev/null | /usr/bin/wc -l | /usr/bin/tr -d ' ')
echo "    Comprador mounts remaining: $mounts"
echo "    Comprador processes remaining: $procs"
echo "    Bridge processes remaining: $bridge_procs"

echo ""
echo "=== recover.sh done ==="
