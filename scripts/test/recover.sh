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

# 3. Kill mDNS announcer orphans
echo "  killing dns-sd orphans..."
/usr/bin/pgrep -f "dns-sd.*Comprador" 2>/dev/null | while read pid; do
    kill "$pid" 2>/dev/null
done

# 4. Force-unmount any per-device NFS mount Comprador left behind
echo "  force-unmounting Comprador NFS mounts..."
/sbin/mount | /usr/bin/grep '\.local:/' | /usr/bin/awk '{print $3}' | while read mp; do
    echo "    umount -f $mp"
    sudo /sbin/umount -f "$mp" 2>/dev/null
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
