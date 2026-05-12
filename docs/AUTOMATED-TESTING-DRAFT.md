<!-- DRAFT: preliminary survey; not reviewed for accuracy against primary sources -->

# Automated end-to-end testing for Comprador — DRAFT

## Current state

Two automated pieces exist:

- **`test-md5.sh`** — byte-level oracle. Runs `md5sum` on the phone via ADB and compares against Mac-side hashes. The bridge is not in the verification path, so a bridge bug cannot hide behind self-consistency. Developer-only (gated by `COMPRADOR_TESTING_ADB=1`).
- **`test.sh`** — bridge integration suite. Issues WebDAV operations directly against the running bridge (list, upload, download, delete, mkdir). No Finder involvement; no Swift app involvement.

What's missing: anything that exercises the Swift app (IOKit attach → bridge spawn → NFS mount → Finder appearance) or validates the full stack unattended.

---

## Mac automation surfaces surveyed

| Surface | What it can do | Reliable? | Permissions needed | Notes |
|---|---|---|---|---|
| **`osascript` / AppleScript** | Control Finder: reveal items, open windows, eject volumes, check `(path to)` idioms. | Solid for Finder; brittle for UI coordinates. | System Settings → Privacy → Automation (per-app grant, first use). | `tell application "Finder" to eject disk "Pixel-6"` works reliably. Disk-level operations are the most durable use. |
| **JXA (JavaScript for Automation)** | Same scripting bridge as AppleScript, JS syntax. | Same reliability as AppleScript. | Same Automation grant. | No active development since ~2016; ships but unmaintained. Prefer AppleScript for new scripts. |
| **`open` shell command** | Open files/URLs/apps; `-R` reveals a file in Finder. | Very reliable. | None. | Cannot drive copy operations or detect mount events. |
| **NSWorkspace (Swift)** | Mount/unmount volumes, open files, observe volume mount/unmount notifications. | Reliable; first-party. | None beyond what the app already holds. | `NSWorkspace.shared.notificationCenter` can observe `didMountNotification` — useful inside the app itself, not from an external test harness. |
| **Accessibility API (AXUIElement)** | Control any UI element: buttons, menu items, drag-and-drop. | Brittle; layout changes break scripts. | System Settings → Privacy → Accessibility (per-app, cannot be pre-granted in sandboxed/hardened apps without MDM). | Last-resort only. Needed only for drag-and-drop verification; otherwise avoid. |
| **XCUITest** | Apple's official UI test framework; reads the AX tree within an Xcode scheme. | Good for windows; awkward for menu bar (`LSUIElement=YES`) apps. | No extra grants for apps built in the same scheme. | Requires Xcode and a test target; cannot drive Finder as a separate process easily. Marginal fit for this stack. |
| **Hammerspoon** | Lua on top of AX + AppleScript; can watch volumes, click menus, move files. | Reasonable; adds a runtime dep. | Same Accessibility grant as AX directly. | Lowers scripting complexity but is a heavyweight dep for a test rig. Skip unless the AX work becomes substantial. |
| **Filesystem-level shell** | `ls /Volumes/`, `cp`, `md5`, `diskutil unmount`. | Very reliable. | None. | Covers most test assertions. No Finder UI needed. |

---

## Recommendation

**Use shell (`cp`, `ls`, `md5`) for the bulk of assertions; use `osascript` only for eject.**

Four of the six listed test needs do not require Finder UI at all:

| Need | Mechanism |
|---|---|
| Detect mount appeared | `ls /Volumes/` or poll until mount point exists |
| List volume contents | `ls "/Volumes/<device>/"` |
| Copy Mac → mount | `cp <src> "/Volumes/<device>/<dst>"` |
| Copy mount → Mac | `cp "/Volumes/<device>/<src>" <dst>` |
| Eject | `osascript -e 'tell application "Finder" to eject disk "<name>"'` or `diskutil unmount "/Volumes/<name>"` |
| Drag-and-drop verification | AX (only if testing Finder's copy verb specifically) |

Drag-and-drop is the only test that genuinely needs UI automation and should be treated as optional/deferred. `diskutil unmount` is simpler than `osascript` for eject and avoids the Automation permission prompt.

---

## Concrete test architecture sketch

```
test-e2e.sh
  requires: adb connected, Comprador.app installed at /Applications/
  env: COMPRADOR_TESTING_ADB=1

1. PRECONDITION
   adb shell svc usb setFunction mtp   # put phone in MTP mode
   # Comprador auto-launches on attach; if not running, open -a Comprador

2. WAIT FOR MOUNT
   VOLUME=""
   for i in $(seq 1 60); do
     VOLUME=$(ls /Volumes/ | grep -v 'Macintosh HD' | head -1)
     [ -n "$VOLUME" ] && break
     sleep 2
   done
   [ -z "$VOLUME" ] && { echo "FAIL: no mount after 120s"; exit 1; }
   echo "Mounted at /Volumes/$VOLUME"

3. SMOKE READ
   ls "/Volumes/$VOLUME/" > /dev/null || { echo "FAIL: ls"; exit 1; }

4. WRITE + VERIFY (Mac → phone)
   PAYLOAD=$(mktemp /tmp/comprador-test-XXXXXX.bin)
   dd if=/dev/urandom of="$PAYLOAD" bs=1024 count=512 2>/dev/null
   cp "$PAYLOAD" "/Volumes/$VOLUME/comprador-test-$(date +%s).bin"
   # Bridge commit is synchronous; ADB check follows:
   PHONE_PATH="/storage/emulated/0/comprador-test-*.bin"
   adb shell "md5sum $PHONE_PATH" | awk '{print $1}' > /tmp/phone.md5
   md5 -r "$PAYLOAD" | awk '{print $1}' > /tmp/mac.md5
   diff /tmp/mac.md5 /tmp/phone.md5 || { echo "FAIL: write hash mismatch"; exit 1; }

5. READ + VERIFY (phone → Mac)
   ROUNDTRIP=$(mktemp /tmp/comprador-roundtrip-XXXXXX.bin)
   cp "/Volumes/$VOLUME/comprador-test-*.bin" "$ROUNDTRIP"
   diff "$PAYLOAD" "$ROUNDTRIP" || { echo "FAIL: read mismatch"; exit 1; }

6. CLEANUP + EJECT
   adb shell "rm $PHONE_PATH"
   diskutil unmount "/Volumes/$VOLUME"
   echo "PASS"
```

Steps 4–5 compose with `test-md5.sh` for bulk transfer verification.

---

## Blockers and workarounds

**Automation / Accessibility prompts.** `osascript` targeting Finder needs a one-time grant in System Settings → Privacy & Security → Automation; AXUIElement needs the Accessibility grant. Neither can be pre-granted without MDM or SIP off. Mitigation: the sketch above uses `diskutil unmount` (no grant needed) and avoids AX entirely unless drag-and-drop testing is added.

**`mount -t nfs` is unprivileged.** Confirmed on macOS: no sudo required in the pipeline.

**Headless CI.** AppleScript and AX both require a logged-in GUI session (`loginwindow` running). A headless SSH-only Mac cannot run them. The test rig requires auto-login + screen lock (not logout). Steps 1–5 in the sketch are pure shell and survive without a session; `diskutil unmount` at step 6 also works without GUI.

**TCC.db pre-seeding.** Seeding `/Library/Application Support/com.apple.TCC/TCC.db` requires SIP off — don't. Grant Automation/Accessibility manually once; grants persist across reboots.

**Comprador sandbox.** The test harness is an ordinary shell script; Comprador's sandbox does not constrain it.
