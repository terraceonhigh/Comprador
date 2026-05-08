# Testing Comprador

## Automated Tests

### Helper unit tests

```bash
make helper-test
```

Exercises the `/etc/hosts` block management against a temp file
(no root required). Covers ADD/REMOVE/CLEAR, idempotency, name
validation, and the wire-protocol dispatcher.

### Bridge integration test suite (`test.sh`)

Tests the Go WebDAV bridge against a real device. Requires a phone
connected in File Transfer mode.

```bash
./test.sh
```

Tests:
1. Root directory listing
2. Subdirectory listing
3. File download (round-trip: upload → download → verify)
4. File upload
5. Verify uploaded file exists
6. Create folder
7. Delete file (+ verify gone)
8. Delete folder (+ verify gone)

### Swift app diagnostic (`test-swift.sh`)

Tests IOKit USB device detection. Launches the app binary directly and
monitors for attach/detach events.

```bash
./test-swift.sh
```

Prompts you to unplug and replug the phone while monitoring output for
30 seconds.

## Manual Testing

### Entry 19a — reattach-during-unmount race

Verifies that a USB detach+reattach storm arriving while an unmount is in
flight does not leave Comprador in a dead state (phone plugged in but nothing
mounted, no further recovery possible without a physical replug).

**Setup:** phone connected and mounted in Finder.

**Trigger:** on the phone, open the USB notification (pull down the shade,
tap the "Charging this device via USB" / "File Transfer" notification) and
switch the USB mode to **Charging Only**. Immediately switch it back to
**File Transfer**. The two taps should be within a second of each other.

This fires a real OS-level detach event followed immediately by a reattach
— the same sequence as a phone screen sleep or MTP interface flutter,
reproduced on demand.

**Expected log sequence (pass):**

```
USB detached — <DeviceName>
USB attached — <DeviceName>           ← arrives while teardown is in flight
Reattach while unmount in flight — queuing (entry 19a)
Unmounting /Volumes/<DeviceName>
Stopping bridge (PID XXXXX)
[teardown completes]
Device attached — <DeviceName>        ← synthesised from pending queue
[normal mount sequence: bridge → PORT= → mount → Mounted at /Volumes/<DeviceName>]
```

Phone remounts in Finder without any user action beyond the USB mode switch.

**Regression (fail):**

```
USB detached — <DeviceName>
USB attached — <DeviceName>
Ignoring attach — already mounted     ← old behaviour; device lost forever
Unmounting /Volumes/<DeviceName>
Stopping bridge (PID XXXXX)
[silence]
```

**Monitoring the log during the test:**

```bash
# In one terminal — build and run the app (log tees to build/comprador.log)
make run-swiftc

# In another terminal — watch the log
tail -f build/comprador.log | grep -E "attach|detach|Unmount|Mounted|queuing|Ignoring"
```

**Note on the trigger:** the USB mode switch is the most reliable way to
reproduce the race on demand. Locking the phone screen while connected
sometimes works too, but is phone-model-dependent. A slow unplug+replug
does *not* reproduce this race — it fires a full detach with nothing
in-flight, which was always handled correctly.

### Bridge standalone

```bash
make dev
# Note the PORT=XXXXX line

# In another terminal:
mkdir -p /tmp/mtp-test
mount_webdav -s -S http://127.0.0.1:PORT/ /tmp/mtp-test

# Test operations
ls "/tmp/mtp-test/Internal shared storage/"
cp /tmp/some-file.txt "/tmp/mtp-test/Internal shared storage/"
cp "/tmp/mtp-test/Internal shared storage/some-file.txt" /tmp/roundtrip.txt
diff /tmp/some-file.txt /tmp/roundtrip.txt

# Cleanup
umount /tmp/mtp-test
```

### Full app

```bash
make run
# App appears in menu bar
# Plug phone, select File Transfer
# Wait ~15-30s for mount
# Browse phone in Finder
# Click "Eject" in menu bar menu or unplug phone
```

### WebDAV debugging with curl

```bash
# Check server is responding
curl -v -X OPTIONS http://127.0.0.1:PORT/

# List root
curl -X PROPFIND -H "Depth: 1" http://127.0.0.1:PORT/

# Stat a file
curl -X PROPFIND -H "Depth: 0" http://127.0.0.1:PORT/Internal%20shared%20storage/

# Download a file
curl -o /tmp/test http://127.0.0.1:PORT/Internal%20shared%20storage/some-file.txt
```

## Reset Procedure

When things go wrong (bridge hangs, stale mount, USB session locked):

```bash
# Kill everything
killall Comprador bridge dns-sd comprador-helper 2>/dev/null

# Unmount any leftover webdav volumes
for v in $(mount | awk '/webdav/ {print $3}'); do
  diskutil unmount force "$v"
done

# If USB session is locked:
# 1. Unplug phone
# 2. Wait 3 seconds
# 3. Replug phone
# 4. Select File Transfer

# If you want to wipe the helper's hosts entries between runs
# (without uninstalling the daemon), the app does this automatically
# on teardown — but you can force it via:
echo "CLEAR" | nc -U /var/run/comprador-helper.sock
```

## Testing the helper in isolation

Run the helper without root by overriding the paths:

```bash
COMPRADOR_SKIP_ROOT_CHECK=1 \
COMPRADOR_SOCKET_PATH=/tmp/test.sock \
COMPRADOR_HOSTS_PATH=/tmp/test-hosts \
build/comprador-helper

# In another terminal:
echo "ADD Pixel-6"     | nc -U /tmp/test.sock
echo "ADD Galaxy-S24"  | nc -U /tmp/test.sock
sed -n '/Comprador/,/Comprador END/p' /tmp/test-hosts
echo "REMOVE Pixel-6"  | nc -U /tmp/test.sock
echo "CLEAR"           | nc -U /tmp/test.sock
```

## Testing mDNS hostname registration

The bridge registers `<DeviceName>.local` via `dns-sd -P` as a fallback
when the helper isn't installed. Exercise it without an MTP device:

```bash
make bridge   # also builds build/mdnstest if you've added it
build/mdnstest "Pixel 6"

# In another terminal:
dscacheutil -q host -a name Pixel-6.local   # should resolve to 127.0.0.1
ping -c 1 Pixel-6.local
```

## Common Failures

| Symptom | Cause | Fix |
|---------|-------|-----|
| `mtp-detect` sees nothing | Cable is charge-only, or File Transfer not selected | Try different cable; check `system_profiler SPUSBDataType` |
| Bridge prints "no MTP device found" | File Transfer not selected, or PTPCamera claimed interface | Select File Transfer; the app kills PTPCamera automatically |
| Bridge hangs at "Detecting MTP device" | Previous session still locked | Unplug/replug phone |
| Mount shows empty directory | PROPFIND response malformed | Check with `curl -X PROPFIND` |
| Files copy but are empty on phone | Write path returns read-only file handle | Ensure O_WRONLY/O_TRUNC triggers mtpNewFile |
| `libusb_claim_interface() = -3` | `ptpcamerad` / `AMPDeviceDiscoveryAgent` holds USB | Bridge kills them in a tight retry loop; if it still loses, unplug/replug for a fresh USB attach window |
| Volume named `Pixel-6.local` instead of `Pixel-6` | Helper isn't installed or approved | Click "Install Helper" in the menu; toggle on in System Settings → Login Items |
| Multiple `/Volumes/Pixel-6-1`, `-2` mounts | Stale mounts from prior crash | Cleared at next app launch; or run the umount loop in the Reset Procedure |
