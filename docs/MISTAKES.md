# Mistakes & Pitfalls

Everything we got wrong building Comprador, so you don't have to.

## MTP / libmtp

### 1. `LIBMTP_Get_First_Device` blocks forever

**What happened:** Our first attempt used `LIBMTP_Get_First_Device()` which
is a convenience function. It blocked indefinitely when the USB interface
was claimed by another process.

**Fix:** Switched to the raw detection API:
`LIBMTP_Detect_Raw_Devices()` → `LIBMTP_Open_Raw_Device_Uncached()`.
This gives granular error reporting and doesn't block.

### 2. Wrong root parent ID for enumeration

**What happened:** `LIBMTP_Get_Files_And_Folders(dev, storageID, 0)`
returned zero results. We passed `0` as the parent ID thinking it meant
"root".

**Fix:** The root parent constant is `0xFFFFFFFF`
(`LIBMTP_FILES_AND_FOLDERS_ROOT`), defined in `libmtp.h` line 923.
Parent ID `0` means something else entirely in MTP.

### 3. Wrong root parent ID for object creation

**What happened:** After fixing enumeration to use `0xFFFFFFFF`, we tried
`0` as parent ID for `LIBMTP_Create_Folder` and
`LIBMTP_Send_File_From_Handler`. Got `PTP Invalid Object Handle (2009)`.

**Fix:** Object creation at storage root also needs `0xFFFFFFFF`, not `0`.
We added `resolveParentID()` which converts our internal storage ID
(which equals the storage root's object ID in our map) to `0xFFFFFFFF`.

### 4. Full recursive enumeration is impractical

**What happened:** First attempt walked the entire phone filesystem at
startup. A Pixel 6 with YouTube Music cache and photo thumbnails had
thousands of entries. Startup took over 5 minutes and hadn't finished.

**Fix:** Lazy enumeration. Only fetch directory contents when Finder
actually browses into them via PROPFIND. Startup drops to under 1 second.

### 5. `GetFolderList` doesn't work with uncached devices

**What happened:** `LIBMTP_Get_Folder_List_For_Storage` returned NULL
for devices opened with `LIBMTP_Open_Raw_Device_Uncached`.

**Fix:** Dropped the folder tree API entirely. Use
`LIBMTP_Get_Files_And_Folders` with `LIBMTP_FILES_AND_FOLDERS_ROOT`
recursively instead — it works with both cached and uncached devices.

### 6. `LIBMTP_destroy_file_t` frees the filename

**What happened:** We allocated a C string for the filename with
`C.CString()`, assigned it to `fi.filename`, then called both
`C.free(cname)` and `LIBMTP_destroy_file_t(fi)` via defers. Double-free
caused a SIGTRAP crash.

**Fix:** Let `LIBMTP_destroy_file_t` own the string. Don't free it
separately:
```go
// BAD
cname := C.CString(name)
defer C.free(unsafe.Pointer(cname))  // double-free!
fi.filename = cname
defer C.LIBMTP_destroy_file_t(fi)

// GOOD
fi.filename = C.CString(name)
defer C.LIBMTP_destroy_file_t(fi)  // frees fi.filename
```

## cgo Callbacks

### 7. Wrong callback signature — 3 params instead of 5

**What happened:** We wrote `goGetFileCallback(buf, size, data)` with 3
parameters. The actual `MTPDataPutFunc` signature has 5:
`(void* params, void* priv, uint32_t sendlen, unsigned char *data, uint32_t *putlen)`.
The bridge would hang on any file download because the stack was corrupted.

**Fix:** Read `libmtp.h` carefully. The typedefs are at lines 498 and 513.
Match the exact signature including the `params` pointer (unused but
present) and the output length pointer.

### 8. `io.EOF` treated as error in upload callback

**What happened:** When uploading a file, the `goDataGetFunc` callback
returned `LIBMTP_HANDLER_RETURN_ERROR` when `io.Reader.Read()` returned
`(0, io.EOF)`. This caused `LIBMTP_Send_File_From_Handler` to abort
with `PTP I/O Error`.

**Fix:** `io.EOF` is normal end-of-data, not an error:
```go
// BAD
if err != nil && n == 0 {
    return C.LIBMTP_HANDLER_RETURN_ERROR
}

// GOOD
if err != nil && err != io.EOF && n == 0 {
    return C.LIBMTP_HANDLER_RETURN_ERROR
}
```

### 8a. cgo MTP callback allocates a fresh slice per call → multi-GiB Go heap retention

**What happened:** After the streaming-write refactor (commit
`0c5a18e`) eliminated bridge-side `bytes.Buffer` accumulation,
mid-Send memory profiling on the same Mac showed the bridge process
hitting **10 GB physical footprint** for a 9 GiB Attenborough send.
`vmmap -summary` attributed almost all of it to **`VM_ALLOCATE`:
11.3 GB across 409 regions, 9.9 GB swapped out, only 128 MiB
resident** — the kernel was actively paging it to swap, but the
allocations themselves were retained.

The pattern: `bridge/mtp/binding_callbacks.go`'s `goDataGetFunc`
(invoked by libmtp via cgo to pull the next chunk of upload data)
calls `make([]byte, int(wantlen))` on every invocation. For a 9 GiB
file with libmtp's typical chunk size of ~22 MiB, that's ~400
allocations totalling 9 GiB of garbage. Go's GC eventually frees
them, but macOS's default `MADV_FREE` policy means the pages stay
in the process's address space (and count against physical
footprint) until the kernel reclaims under pressure. The 409 regions
in `vmmap` roughly matched the 400 chunk count — strong evidence
that each callback's slice became its own arena segment that never
returned to the OS.

The same pattern affects `goDataPutFunc` (the GET path). When the
user accidentally clicks a multi-GiB file on the mounted volume,
Finder QuickLook fires `LIBMTP_Get_File_To_Handler`, the callback
allocates per-chunk slices for the entire file, and the bridge's
footprint balloons by another file-size's worth of `VM_ALLOCATE`
regions on top of any from prior sends. The 409-region observation
came mid-GET and was still climbing (10.0 → 10.1 GB) when we killed
the process.

**Fix (sketched, not yet implemented):** reuse a single buffer per
session instead of allocating per call. The buffer lives in the
callback registry alongside the io.Reader/io.Writer; on the first
call it's `make([]byte, max(wantlen, defaultChunk))`, subsequent
calls reuse it (with a one-time grow if `wantlen` exceeds the
current buffer). Caps Go-side memory at one chunk (~22 MiB) instead
of file-size. Same fix for both Get and Send paths.

**What this leaves uncovered:** there may be additional C-side
allocations inside libmtp itself (PTP transaction buffers, etc.)
that we don't control. After the Go-side fix, profile again. If a
real C-side leak remains, options narrow to (a) patching libmtp,
(b) calling some libmtp release/reset API between transfers, or
(c) restarting the bridge process between large transfers as a
caretaker workaround.

## WebDAV / Finder

> **Section status — slated for historical archive once v0.4.0 ships.**
> NFS has been the default mount path since v0.3.0 (2026-05-09). The
> WebDAV apparatus is retained in tree for legacy reasons but is no
> longer exercised by normal use; the v0.4.0 retirement work
> ([TODO.md "Tidying" Tier 3](../TODO.md)) will remove
> `bridge/webdav/`, `MountManager.mount`, `ResumeCompanion`, and the
> writeseq-cap heuristics. At that point, every entry in this section
> (9, 9a, 10, 11, 11a–11e) becomes a postmortem on code that no longer
> exists. Keep them — the underlying lessons about Apple's webdavfs
> quirks generalise — but read them with that frame.

### 9. `Seek` always returned `(0, nil)`

**What happened:** Our `mtpFile.Seek()` fetched the file into a
`bytes.Buffer` but always returned `(0, nil)`. The WebDAV handler calls
`Seek(0, SeekEnd)` to determine file size before serving — getting `0`
back meant every file appeared empty.

**Fix:** Replaced `bytes.Buffer` with `bytes.Reader`, which implements
`io.ReadSeeker` properly.

### 9a. Apple WebDAVFS chunked-upload cap is variable, not fixed at 32–64 MiB

**Background:** PR #2 ("WebDAV: fix Finder large-file uploads truncating
at 32/64 MiB") landed a truncation guard based on the assumption that
Apple WebDAVFS's writeseq cap was 32 or 64 MiB depending on memory
pressure. That figure came from one machine's behavior under one
specific memory state. Real-world cap is much more variable.

**Observation 2026-05-05:** On a fresh Mac with ample free memory, a
9.09 GB `David.Attenborough.A.Life.on.Our.Planet.mkv` was truncated by
webdavfs at **4,295,639,040 bytes (≈ 4.00 GiB)** — three orders of
magnitude above the documented 32 MiB. The truncation guard fired
correctly, refused the partial commit, and the user saw a clean Finder
error -36 with the existing file (none, in this case) preserved. A
1 GiB Shrek dragged minutes earlier on the same mount went through
unscathed.

**Updated mental model:** The writeseq cap appears to scale with
*available* free memory rather than total RAM. Under memory pressure
the cap can drop to tens of MiB; with most of RAM free, the cap can be
multiple GiB. A user who closes 12 Chrome tabs between drags will see
*different* size limits. This is a property of `webdav_strategy()` in
the apple-oss-distributions kernel module, not something the WebDAV
server can control directly.

**Implication:** The truncation guard prevents corrupt files but
doesn't make large drag-uploads succeed. A user with a 6 GB file is
near a coin-flip on whether Finder drag works. `cp` from Terminal
bypasses the writeseq path and works at any size — see the 8 GiB
test in `bridge/`'s commit history. Note 11d below for the broader
plan to make this disappear.

### 10. Existing files can't be overwritten

**What happened:** When Finder drags a file to the phone, the WebDAV
handler first creates a 0-byte file (PUT with O_CREATE), then tries to
write content. On the second PUT, the file already exists in our cache,
so `OpenFile` returns a read-only `mtpFile` instead of a writable
`mtpNewFile`. Content goes nowhere.

**Fix:** When `O_WRONLY`, `O_RDWR`, `O_CREATE`, or `O_TRUNC` flags are
set, always return `mtpNewFile`. If the file already exists, delete it
first then create a fresh writable file.

### 11. Cache not invalidated after mutations

**What happened:** After uploading a file, the parent directory's
"populated" flag wasn't reset. A subsequent PROPFIND returned the stale
cached listing without the new file.

**Fix:** Call `ObjectMap.InvalidateDir(parent)` after every mutation:
Mkdir, RemoveAll, Rename, and mtpNewFile.Close.

### 11a. Failed enumeration cached as "empty directory"

**What happened:** `populateDir` called `device.GetFilesAndFolders` and
unconditionally called `MarkPopulated(dirPath)` afterward. But
`GetFilesAndFolders` swallowed libmtp errors — logged them but returned an
empty `[]FileMeta` with no error. So a transient `PTP I/O Error 02ff`
(phone screen asleep, USB renumeration mid-flight, kernel-driver
collision) cached as "successfully enumerated zero entries." Finder then
showed the directory empty *forever*, even after the device recovered,
until the bridge process restarted. Reproduced when a USB
detach/reattach storm hit during the storage's first `PROPFIND`.

**Fix:** `GetFilesAndFolders` now returns `(entries, error)`. If the
error stack is non-empty after the libmtp call, surface that to the
caller. `populateDir` skips `MarkPopulated` on error so the next access
retries. Two-line behaviour fix; the bug was the silent-swallow API
shape.

### 11b. Finder error code 100060 = ETIMEDOUT

**Reference:** macOS Finder's "(error code 100060)" dialog corresponds to
`NSCocoaErrorDomain Code=256` wrapping `NSPOSIXErrorDomain Code=60` —
i.e. `ETIMEDOUT`. The dialog is generic: any I/O timeout against a
WebDAV mount surfaces as 100060 to the user, regardless of the actual
cause (server unreachable, server slow, client stuck waiting on
unresponsive resource).

To find the real reason, run:
```bash
log show --last 5m --predicate 'process == "Finder"' --style compact \
  | grep -iE 'TranslateCFError|CopyEngine|NSUnderlyingError'
```
The `NSUnderlyingError` field gives the actual POSIX errno.

### 11c. WebDAV quota properties not advertised → preflight refusal

**What happened:** Drag-and-drop of a 1 GB file failed with error 100060
*before* webdavfs sent a single byte to the bridge. The bridge log
showed only the existence-check PROPFIND for the destination filename
(404), then nothing. Finder bailed at preflight.

**Cause:** Finder uses `statfs(2)` against the mount before starting a
copy to verify free space. webdavfs translates `statfs` into a PROPFIND
for `<D:quota-available-bytes/>` and `<D:quota-used-bytes/>`. The
`golang.org/x/net/webdav` package doesn't synthesise these
automatically — it returns 404 for both — and webdavfs reports the
mount as having zero free bytes (`df -h` returns `0Bi` and times out).
Finder's preflight refuses any non-trivial copy "for lack of space."

**Fix:** Implement `webdav.DeadPropsHolder` on the root `mtpDir`,
returning real bytes from libmtp's storage info. After the fix,
`df -h /Volumes/<phone>` reports the device's actual capacity, and
Finder lets the copy through. See `bridge/webdav/handler.go:DeadProps`.

**Side note:** the `Storage` info is snapshotted at session open and
never refreshed mid-session, so the displayed free-space drifts as the
user copies files. Cosmetic; not a correctness issue. Refreshing on
every quota PROPFIND would be a future improvement.

### 11d. Single session goroutine deadlocks under concurrent read pressure

**What happened:** During a Shrek drag-and-drop, the bridge appeared
healthy (`/Internal shared storage` listed instantly via PROPFIND) but
LOCK and PUT requests hung indefinitely. Finder timed out with 100060
after 60s. `sample` of the bridge process showed 798/798 stack samples
in `LIBMTP_Get_File_To_Handler → ptp_read_func` — pegged on a single
GET.

**Cause:** libmtp is not thread-safe. The bridge serialises *all* MTP
operations through one goroutine (`bridge/mtp/session.go`'s `run` loop),
which is correct. But the GET handler, once it's pulled by a client
(Spotlight, QuickLook, AudiovisualThumbnailExtension, mdworker, or the
user opening a file), holds the session goroutine for the *entire
read duration* — minutes for a multi-GB file. Concurrent LOCK/PUT/
DELETE/PROPFIND-needing-populate queue behind it. Finder's 60s I/O
timeout fires long before the GET completes. webdavfs gives up,
surfaces ETIMEDOUT (100060) to Finder.

In our session, the trigger was a `comprador-test-8g.bin` left in
`/Download` from prior testing. Some macOS background indexer opened it
to inspect, webdavfs pulled it through the bridge, and the next 14
minutes of drag-and-drop attempts all timed out.

**Workaround for testing:** Don't leave large files on the device that
Spotlight will want to index.

**Real fix (not yet written):** Make GETs cancellable. When the HTTP
request context is canceled (client disconnect or webdavfs timeout),
call `LIBMTP_Cancel_Operation` so the session goroutine isn't held
hostage by a client that already gave up. This structurally settles
three TODO items at once: bridge-crash recovery, detach-mid-transfer
graceful handling, and large-directory enumeration blocking.

### 11d-bis. webdavfs writeseq EADDRNOTAVAIL (different bug, same -36)

**Background:** distinct from the writeseq *cap* documented in 9a (which
truncates a multi-GiB PUT after delivering some bytes). This one prevents
webdavfs from even opening its writeseq TCP connection — zero body bytes
delivered, immediate -36, while the bridge's regular HTTP path (PROPFIND,
0-byte placeholder PUT) keeps working fine.

**Symptom in `/tmp/comprador-run.log`:** Two or three `MTP SendFile(...,
size=0)` placeholders within a few hundred milliseconds, then PROPFINDs
returning "file does not exist" as Finder gives up. Bridge log shows no
real PUT body, no `STRANDED`, no truncation receipt.

**Symptom in `log show --predicate 'process CONTAINS webdavfs'`:**
```
webdavfs_agent connectx(27, [srcif=5, ...]) failed: [49: Can't assign requested address]
writeseqReadResponseCallback: EventErrorOccurred CFStreamError: domain 1, error 49
stream_error: Posix error 49
```

errno 49 = `EADDRNOTAVAIL`. webdavfs's writeseq path uses CFStream
directly (not the same transport as its regular HTTP path), and CFStream
fails to bind a local source port for the loopback connection. The
regular path keeps working at the same time, which is why placeholders
land but the real body doesn't.

**Reproducibility 2026-05-06:** First seen at 23:51 as a one-off,
suspected transient. Confirmed reproducible at 00:47 — second drag of
the same Attenborough.mkv exhibited identical pattern. So it's a real
state issue, not a fluke.

**Possible causes** (not narrowed down):
- macOS network-stack state accumulating across many bridge restarts
  in one session (we did a lot tonight)
- mDNS resolution cache returning a stale interface hint to CFStream
- CFStream's bind(2) trying to use a source address that conflicts
  with another in-flight connection

**Workaround:** Unknown. Likely needs `pkill -9 webdavfs_agent`
followed by a remount to clear webdavfs's internal CFStream state, or
a system-wide network-stack reset (`sudo dscacheutil -flushcache;
sudo killall -HUP mDNSResponder`). Untested.

**Implication for Comprador:** This bug bypasses option C's resumable
upload mechanism entirely — the bridge never sees any body bytes, so
there's nothing to persist for the Swift companion to complete. If it
turns out to be common, option C alone won't be sufficient and we
need a separate path that detects "writeseq never started" and recovers
via different means (full source read from the Mac, kicked off by the
Swift companion based on an absent-PUT-body timeout).

### 11d-tris. Finder QuickLook on a multi-GiB phone file pulls the whole thing

**What happened:** User accidentally clicked the Attenborough.mkv on
the mounted phone volume. Finder QuickLook's preview generator fires
a stat → opens the file → reads enough to render a thumbnail. For
video, "enough" means several MiB at minimum, often the whole file
if the AVKit decoder needs to scan moov atoms scattered through the
container. macOS sometimes also speculatively pre-fetches the
*entire* file when QuickLook is invoked.

In our case, that triggered `LIBMTP_Get_File_To_Handler` on a 9.09
GiB file. The bridge's session goroutine pegged in
`ptp_read_func` → blocked every other MTP operation → bridge memory
ballooned by another file-size's worth of `VM_ALLOCATE` regions
(see 8a) → system load spiked from ~3 to ~41 → Finder beach-balled
on every interaction → mouseover-rainbow-throbber across the whole
desktop. ETA to GET completion at MTP read throughput: ~15-20 min.

**Workaround:** force-quit Comprador to abort the GET, releases
both the libmtp session and the leaked allocations. Lose the mount;
have to replug.

**Defenses worth considering:**
1. Bridge-side: rate-limit GETs to mounted-volume files larger than
   some threshold (e.g., refuse with 416 if size > 100 MiB and the
   request looks like a thumbnailer probe — `User-Agent: */
   QuickLook*` or similar).
2. Mount-side: add `nobrowse` mount flag so the volume doesn't
   appear in Finder sidebar's left pane (less drag-temptation, but
   user can still navigate manually).
3. mdutil: explicitly disable Spotlight indexing on this mount path
   so the indexer doesn't make the same mistake.
4. Best long-term answer: make GETs cancellable (the longstanding
   TODO from #11d) so a click that triggers a 9 GiB read can be
   killed when the user closes the QuickLook preview.

### 11e. Eject-mid-buffer leaves an orphan webdavfs cache

**What happened:** User dragged a 1 GB file into the mount; webdavfs
buffered ~1 GB locally to its cache (`/private/tmp/.webdavcache.<pid>`)
before flushing. Mid-buffer, user clicked "Eject" in the Comprador
menu. Comprador unmounted via `DAUnmountWithOptions` and killed its
bridge cleanly. But webdavfs's userspace agent (`webdavfs_agent`)
stayed alive holding the cache file open with no server to flush to.
A USB re-enumeration triggered a fresh bridge spawn at a new port,
and Comprador remounted at `/Volumes/<name>-1` because the kernel
hadn't yet released the original mount path.

Result: two volumes in Finder, one stale (no backing server), one
live. Drags into the "wrong" mount silently disappear into webdavfs
cache held by the orphan agent.

**Workaround:** `pkill -9 webdavfs_agent` and `diskutil unmount force
/Volumes/<name>` (the latter often reports failure but succeeds).

**Long-term fix:** Eject path needs to either (a) wait for webdavfs
cache to flush before killing the bridge — requires IPC we don't have —
or (b) explicitly send `webdavfs_agent` SIGTERM before
`DAUnmount` so the kernel releases the mount path before the next
attach event. Not yet attempted.

## macOS / IOKit

### 12. `IOUSBDevice` vs `IOUSBHostDevice`

**What happened:** Used `kIOUSBDeviceClassName` (`"IOUSBDevice"`) for IOKit
matching. No devices were ever matched. No error — just silence.

**Fix:** macOS 13+ uses `IOUSBHostDevice`. Check with:
```bash
ioreg -p IOUSB -l | grep "class"
```
Use `IOServiceMatching("IOUSBHostDevice")` instead.

### 13. `kUSBVendorID` vs `"idVendor"`

**What happened:** Used the legacy `kUSBVendorID` constant for IOKit
property matching and lookup. Didn't match anything.

**Fix:** Modern IOKit uses `"idVendor"` and `"idProduct"` as property
keys. Same for `IORegistryEntryCreateCFProperty`.

### 14. NSApplication.delegate is weak

**What happened:** Created `AppDelegate()` as a local variable in the
`@main` struct's `main()` function. Assigned it to
`NSApplication.shared.delegate` (which is weak). The delegate was
immediately deallocated by ARC. No lifecycle methods ever fired.

**Fix:** Use a separate `main.swift` file:
```swift
let delegate = AppDelegate()  // strong reference
NSApplication.shared.delegate = delegate
_ = NSApplicationMain(CommandLine.argc, CommandLine.unsafeArgv)
```

### 15. NSLog not visible in `log stream`

**What happened:** Ran the app via `open Foo.app` and watched
`log stream --process Comprador`. Our NSLog messages didn't appear.
System framework messages appeared, but not ours.

**Fix:** Launch the binary directly
(`Foo.app/Contents/MacOS/Foo > log.txt 2>&1`) to capture NSLog output.
The `log stream` tool has filtering quirks with NSLog from unsigned apps.

### 16. Per-vendor IOKit matching dict + ARC = silent failure

**What happened:** Registered separate IOKit matching notifications for
each of 15 vendor IDs. Each required a matching dictionary. The
`mutableCopy()` + Swift ARC + IOKit's CFDictionary consumption semantics
interacted badly — notifications silently failed to register.

**Fix:** Register ONE notification for ALL `IOUSBHostDevice` connections,
then filter by vendor ID in the callback. Simpler and avoids the ARC
memory management issue.

## USB / Process Management

### 17. PTPCamera claims the USB interface

**What happened:** macOS's `PTPCamera` process auto-launches and claims
the MTP/PTP USB interface before our bridge can.
`libusb_claim_interface()` returns `-3` (`LIBUSB_ERROR_ACCESS`).

**Fix:** Kill `PTPCamera` and `AMPDevicesAgent` before starting the bridge:
```swift
Process("/usr/bin/killall", ["-9", "PTPCamera"])
```

### 18. SIP strips DYLD_LIBRARY_PATH

**What happened:** The bridge binary links `libmtp.9.dylib` from
`/opt/homebrew/opt/libmtp/lib/`. Set `DYLD_LIBRARY_PATH` on the spawned
process. Binary still couldn't find the library — macOS SIP strips all
`DYLD_*` environment variables from child processes.

**Fix:** Bundle `libmtp.9.dylib` (and `libusb-1.0.0.dylib`) in the app's
`Frameworks/` directory. Use `install_name_tool -change` to rewrite the
load path to `@executable_path/../Frameworks/`.

### 19. USB re-enumeration storm on MTP mode switch

**What happened:** When an Android phone switches to File Transfer (MTP)
mode, the USB interface re-enumerates — causing 3-4 rapid detach/attach
IOKit events within seconds. Our detach handler killed the bridge
mid-startup every time.

**Fix:** Added `isConnecting` flag that locks out all attach/detach
handling during connection. Initial 5-second delay before first bridge
attempt. Retry logic with increasing delays. The phone needs time to
settle into MTP mode.

### 19a. Detach+reattach storm AFTER mount loses the device entirely

**What happened:** Mount succeeds cleanly. ~5 seconds later — typically
when the phone screen goes to sleep, or some other phone-side event
flutters the MTP interface — IOKit fires a detach immediately followed
by an attach (3-5 ms apart). Comprador's detach handler initiates an
unmount + bridge stop, which is asynchronous; meanwhile the *re*attach
handler fires and sees `isMounted == true` (the unmount hasn't
completed yet) and logs `Ignoring attach — already mounted`. The unmount
then finishes, the bridge is gone, and there is **no further attach
event to trigger a fresh bridge spawn** — the phone stays plugged in
but Finder shows nothing, and the menu bar app sits in a "detached, no
bridge, no mount" terminal state until the user physically replugs.

Reproduced 2026-05-06 from the bridge log:

```
13:51:51  Mounted at /Volumes/XQ-BT52.local
13:51:56.108  USB detached — XQ-BT52
13:51:56.113  USB attached — XQ-BT52
13:51:56.113  Device detached — XQ-BT52
13:51:56.113  Device attached — XQ-BT52
13:51:56.113  Ignoring attach — already mounted   ← bug surfaces here
13:51:56.113  Unmounting /Volumes/XQ-BT52.local
13:51:56.115  Stopping bridge (PID 1743)
[silence forever; user must unplug+replug]
```

**Why it's not just the existing `isConnecting` lockout from #19:** that
flag only fires *during the initial mount sequence*. Once the mount
succeeds it's cleared, leaving the post-mount state machine without a
reattach-during-pending-unmount guard.

**Fix (2026-05-07):** `AppDelegate` now tracks `pendingAttach: USBDevice?`.
When `handleDeviceAttached` fires while `isMounted == true`, it queues the
device there instead of discarding the event. At the end of the detach
handler's teardown Task — after `connectedDevice = nil` and icon reset —
it drains `pendingAttach` by calling `handleDeviceAttached(queued)`.
`ejectDevice()` clears `pendingAttach` before teardown so a user-initiated
disconnect doesn't immediately reconnect.

With the fix, the log reads:

```
13:51:51  Mounted at /Volumes/XQ-BT52.local
13:51:56.108  USB detached — XQ-BT52
13:51:56.113  USB attached — XQ-BT52
13:51:56.113  Device detached — XQ-BT52
13:51:56.113  Device attached — XQ-BT52
13:51:56.113  Reattach while unmount in flight — queuing (entry 19a)
13:51:56.113  Unmounting /Volumes/XQ-BT52.local
13:51:56.115  Stopping bridge (PID 1743)
[teardown completes]
13:51:57.400  Device attached — XQ-BT52   ← synthesised from pending queue
13:51:57.400  [normal mount sequence resumes]
```

Workaround no longer needed. Tracked in TODO.md under "Handle detach during
file transfer gracefully (don't hang Finder)" — that entry was filed
before we'd reproduced this exact race, but the fix is the same shape.

### 19b. Multi-device ptpcamerad-kill race on app startup

**Symptom (2026-05-17, post-multi-device-step-6):** Architect
launches Comprador.app with two phones (Xperia + Pixel 6) already
plugged in. Pixel mounts cleanly; Xperia fails to mount with no
user-visible explanation. Unplug + replug the Xperia recovers it.
The failure was 100% reproducible on plug-then-launch; absent on
plug-after-launch.

**Empirical receipt** (from `build/comprador.log`):

```
13:24:52.725  USB attached — Pixel 6   ← initial-scan drain
13:24:52.725  USB attached — XQ-BT52   ← initial-scan drain
13:24:58.067  Pixel:  [status] Starting bridge…
13:24:58.085  Xperia: [status] Starting bridge…
13:24:58.091  Xperia: IOKit preflight skipped (USBDeviceOpenSeize → 0xE00002C5)
13:24:58.111  Killed ptpcamerad / AMPDeviceDiscoveryAgent
13:24:58.146  Pixel:  IOKit preflight OK (seized + re-enumerated)
13:24:58.225  Xperia bridge: libusb_claim_interface() = -3 (LIBUSB_ERROR_ACCESS)
13:24:58.315  Xperia bridge: failed to open MTP device — kernel-bound
              to the USB interface, requires physical replug or IOKit
              interface seize
```

`0xE00002C5` is `kIOReturnExclusiveAccess`.

**Root cause.** With both phones already attached when the app
launches, `ptpcamerad` has been claiming both USB interfaces in
exclusive-access mode for however long the user had them plugged
in. Both DeviceSessions then race to spawn their bridges
concurrently. The Xperia preflight runs first, finds `ptpcamerad`
holding the USB interface, fails. The Pixel preflight runs second
(a few tens of ms later, after a parallel bridge subprocess has
called `killall ptpcamerad`), finds the interface free, succeeds.
Xperia's bridge then tries `libusb_claim_interface` directly —
but the kernel's USB Imaging Class driver has by now bound
itself to the device's class-6 PTP interface, and that binding
**survives ptpcamerad's death** — only a physical replug (or
another IOKit-level re-enumeration via `USBDeviceReEnumerate`)
breaks it.

The Pixel works in the same window because its preflight runs
*after* the first `killall` lands, and the IOKit re-enumeration
that the preflight performs is the thing that breaks the
kernel binding. Xperia gets neither: its preflight fired too
early, and there's no retry path to re-attempt the seize once
the kill has cleared the way.

**Fix shape (revised after first attempt).**

A single global `killall ptpcamerad` at app startup turned out
to be insufficient: macOS's launchd respawns `ptpcamerad` within
seconds of the kill, and the 5-second gap between
`applicationDidFinishLaunching` and the actual `USBDeviceOpenSeize`
calls is plenty of time for the respawn. Verified empirically
2026-05-17 13:34: the startup kill fired at T+0, the seize tried
at T+5 s, and the seize still failed with
`kIOReturnExclusiveAccess` — meaning a fresh `ptpcamerad` had
already taken the device by then.

**The actual fix** is in `BridgeProcess.start()`: swap the order
of `killCompetingProcesses()` and `USBSeizer.seizeAndReset()`.
The existing implementation called *seize* first and *kill*
second, which guaranteed every seize ran against a live
`ptpcamerad`. Killing first means each seize runs against an
already-dead `ptpcamerad`, and the seize's USBDeviceReEnumerate
fires its USB-level replug before launchd has time to respawn
the holder. Concurrent DeviceSessions both fire their own
`killCompetingProcesses` first, so even with two near-
simultaneous bridge spawns the kills converge to "ptpcamerad
dead" before either seize runs. ~3 lines (the swap itself; the
respawn-window analysis is in the block comment).

The global startup pre-kill is **kept as belt-and-suspenders**
— harmless if launchd respawns ptpcamerad before the seizes, and
arguably useful for the rare edge case where the bridge subprocess
itself fails to fire `killCompetingProcesses` for some reason.

`USBSeizer.shared` serializing actor (PLAN-MULTI-DEVICE.md §7)
is **not yet implemented**. The order swap alone closes the
empirical failure; the actor is cheap insurance for a
hypothetical race we haven't observed. Add if validation
surfaces another seize-collision pattern.

**Third iteration (2026-05-17 mid-afternoon).** The order swap
was necessary but still insufficient when both phones were
attached. Built a serializing `DispatchQueue` around the
kill+seize pair (b2b76859), then moved the post-seize settle
sleep inside the queue (8396a7f6) so cross-session protection
would actually work. Both attempts failed empirically: with
the queue + 1.2s in-queue settle, Session A's seize for the
Pixel still succeeded but Session B's seize for the Xperia
got `kIOReturnExclusiveAccess` 67 ms after Session A's seize
returned. Worse: a wider pattern surfaced — the Pixel's USB
descriptor migrated from `0x18d1:0x4ee1` (MTP File Transfer)
to `0x18d1:0x4ee8` (a non-MTP variant libmtp doesn't enumerate)
sometime during the earlier failed retry cycles. Once the
Pixel was in `0x4ee8`, no amount of in-app seizing recovered
it; only a physical replug reset the phone to whatever mode
its on-screen USB notification had selected.

**Root cause of the race (final understanding).** Both phones
sit downstream of the same root-hub port (Pixel locID
`0x00110000`, Xperia `0x00121100` — both bus 0, both behind a
shared hub). `USBDeviceOpenSeize` isn't a per-device flag;
it's a transaction the macOS USB family mediates through the
host controller's state machine. `USBDeviceReEnumerate`
triggers a physical-layer USB reset (hub drops the port,
device renegotiates descriptors, hub re-detects, descriptor
query starts over). For the ~0.5–1.5 s that reenum takes,
the host controller's bus state is "busy with reset," and the
USB family correctly rejects concurrent exclusive-access
requests on the same controller. Our two `DeviceSession`s
running ~10 ms apart hit this window every time.

We can't probe "bus is settled now" from userspace; we can
only sleep an empirical guess. The Pixel `0x4ee1 → 0x4ee8`
degradation is the *worse* failure mode lurking behind
"retry the race in software": every additional
seize+reenumerate cycle stresses the phone's USB
re-negotiation, and after a few rounds the phone's USB stack
falls back to a non-MTP configuration. So even when the
software race appears to resolve, we may have silently moved
the phone into an unrecoverable (without replug) state.

**Final fix shape (shipped 2026-05-17 14:00s).**

1. Revert b2b76859 + 8396a7f6 (the serializing queue) —
   commit `e64983f1`. The queue mechanism was attacking the
   wrong problem; cost without benefit.
2. Fail-fast on first claim failure — commit `327ae66d`. Swift
   `retryDelays: [0,3,5]` → `[0]`; Go `maxAttempts: 2 → 1`.
   One clean attempt, then surface the unplug-and-replug
   notification. No retry loop to degrade the phone further.
3. Welcome-window onboarding includes the
   unplug-and-replug recovery hint plus the `ptpcamerad`
   side-effect disclosure — commit `1441171b`. Primes the user
   for the failure before it happens; the
   `postFileTransferNotification` alert lands as
   confirmation, not surprise.

Kept: 54c929d6 (kill-before-seize order). That's still
correct on its own merits.

**Empirical result.** Two phones pre-attached: first phone
mounts on first try (bus is idle when its seize fires), second
phone fails fast and the user gets the notification + welcome-
window-primed expectation. User replugs the second phone, the
DeviceWatcher fires a fresh attach event, second phone mounts
on its own first try (bus is idle again, since the first phone
is fully stable). Net latency: one cable wiggle, no scary
"Server connections interrupted" alert from the kernel NFS
client.

**Pattern lesson.** When two software clients fight over a
shared hardware resource and the kernel arbitrates correctly
by rejecting concurrent claims, the right software response
is usually not "fight harder" or "wait longer" but "accept
the kernel's correctness and let the user arbitrate." The
user's physical action (replug) is a guaranteed coordination
point that no amount of `Thread.sleep` tuning can match,
because we can't reliably detect when the resource is free.

**Architectural exit (deferred).** The race exists because we
use IOKit `USBDeviceOpenSeize` to break the kernel's USB
Imaging Class binding. A FUSE-T-based mount substrate would
not need to break that binding at all — the kernel mediates
the relevant access through a different IOKit family. Most
of MISTAKES 19b would vanish under FUSE-T, along with the
ptpcamerad kill (entries 11, 17, 19a) and the
NFS-RPC-timeout class (entry 4). Tracked in
[PLAN-NFS-READ.md](PLAN-NFS-READ.md) and TODO.md.

**Pattern lesson.** A "seize race" between sibling DeviceSessions
wasn't on my multi-device radar — the assumption was that each
session's seize was independent because each targets a different
USB device. It is — but the *prerequisite* of the seize
(`ptpcamerad` not holding exclusive access to any device on the
bus) is process-wide and global. PLAN-MULTI-DEVICE.md §7 named
this in the abstract; the architect's bug report was the first
empirical confirmation.

**Status:** fix in flight, to be validated by the same
plug-both-then-launch scenario.

### 20. `mount_webdav` silently fails with custom mount point

**What happened:** Tried calling `/sbin/mount_webdav` directly to control
the volume name (mount at `/Volumes/Pixel 6` instead of
`/Volumes/127.0.0.1`). Exit code 2, no error message, regardless of
whether the directory existed or not.

**Fix:** Reverted to `NetFSMountURLSync` which works reliably. The volume
name issue (`127.0.0.1` in Finder sidebar) remains an open TODO.
`kNetFSMountAtMountDirKey` also returns error 2. The volume name appears
to be derived from the server hostname and cannot be easily overridden
through the mount API.

## Build System

### 21. Go's `vendor/` directory conflict

**What happened:** Put the libmtp C header in `bridge/vendor/libmtp.h`.
Go's module system interpreted `vendor/` as a Go vendor directory and
complained about inconsistent vendoring.

**Fix:** Renamed to `bridge/cvendor/`.

### 22. Charge-only USB cables

**What happened:** `mtp-detect` returned no devices, phone showed File
Transfer mode selected. `system_profiler SPUSBDataType` showed no phone
at all.

**Fix:** The USB-C cable had no data lines (charge-only). A different cable
fixed it instantly. Always check `system_profiler SPUSBDataType` first —
if the device doesn't appear there, it's a cable/port issue, not software.

### 23a. Xcode pbxproj drift from `app-swiftc` adding files

**What happened:** `make app-swiftc` (the swiftc-only build, no Xcode)
globs `MenuBarApp/Sources/*.swift` and compiles everything it finds.
`make app` and `make app-debug` (the Xcode builds) require explicit
project membership — the file must appear in `PBXBuildFile`,
`PBXFileReference`, the Sources `PBXGroup`, and `PBXSourcesBuildPhase`.

When new Swift files were added to `Sources/` they "just worked" under
`app-swiftc` but quietly broke `app-debug` with errors like:
```
AppDelegate.swift:14: error: cannot find type 'WelcomeWindowController' in scope
```

In our case `WelcomeWindow.swift` and `USBSeizer.swift` were both
missing from the Xcode project. Hidden by the symptom of `app-swiftc`
working: the Debug build had been broken for an unknown number of
commits before anyone tried to use it.

**Fix:** Add four lines per file to `Comprador.xcodeproj/project.pbxproj`:
```
A1xxxxxx /* Foo.swift in Sources */ = {isa = PBXBuildFile; fileRef = A2xxxxxx /* Foo.swift */; };
A2xxxxxx /* Foo.swift */ = {isa = PBXFileReference; lastKnownFileType = sourcecode.swift; path = Foo.swift; sourceTree = "<group>"; };
... A2xxxxxx /* Foo.swift */, ... in the Sources PBXGroup
... A1xxxxxx /* Foo.swift in Sources */, ... in PBXSourcesBuildPhase
```

**Prevention:** When adding any new `.swift` file, either commit through
Xcode (which updates the pbxproj), or add the four entries by hand and
run `make app-debug` to verify. Don't trust `make app-swiftc` as a
sanity check — it can't tell you what Xcode is missing.

### 23b. Duplicate `PBXFileReference` IDs in pbxproj

**What happened:** While debugging 23a, found that the IOKit framework
and `HelperClient.swift` were both defined as `A2000010` in the same
file. Two `PBXBuildFile` entries pointed at the same fileRef ID,
resolving in source order — the framework "won," so HelperClient.swift
was never compiled. Symptom was a flood of `cannot find 'HelperClient'
in scope` errors after wiring up the WelcomeWindow files.

**Fix:** Renumbered IOKit's fileRef to `A2000030`. Xcode tolerated the
duplicate silently for an unknown amount of time before this issue
surfaced.

**Prevention:** The pbxproj format is unforgiving and Xcode only
sometimes warns about ID collisions. When hand-editing, grep the file
for the new ID before assigning it. When Xcode generates IDs, they're
24-hex-char UUIDs and don't collide; our pbxproj uses 8-char readable
IDs which are easy to collide accidentally.

### 23c. `__preview.dylib` only in DerivedData, no main binary

**What happened:** `xcodebuild` reported `BUILD SUCCEEDED` but launching
the app failed with `No such file or directory`. Inspection showed
`Comprador.app/Contents/MacOS/` contained only `__preview.dylib` — a
SwiftUI preview artifact — and not the main `Comprador` executable.

**Cause:** A SwiftUI preview session in Xcode left state in the
DerivedData that subsequent `xcodebuild` runs honored as the "current"
build product. The preview-only output was treated as complete.

**Fix:**
```bash
rm -rf ~/Library/Developer/Xcode/DerivedData/Comprador-*
make run
```

DerivedData clean fixes it permanently for that session. Doesn't seem
to recur unless the user reopens Xcode and starts a preview again.

### 23. Xcode not selected / not initialized

**What happened:** `xcodebuild` failed with three separate errors on a
fresh machine:
1. "active developer directory is a command line tools instance"
2. "You have not agreed to the Xcode license"
3. "A required plugin failed to load" (CoreSimulator missing)

**Fix:** Three commands, in order:
```bash
sudo xcode-select -s /Applications/Xcode.app
sudo xcodebuild -license accept
sudo xcodebuild -runFirstLaunch
```

## NFS pivot (go-nfs)

### 1. go-nfs does not support `exclusive` create mode

**What happened (anticipated):** `nfs_oncreate.go` in `willscott/go-nfs`
explicitly returns `NFSStatusNotSupp` for `createModeExclusive`
(see comment: "TODO: support 'exclusive' mode"). The macOS NFS client
uses exclusive mode for new-file creation from Finder (to prevent
duplicates). This means Finder writes will fail with ENOTSUP.

**Status (2026-05-08):** Phase 2a (read-only MTPFileSystem) verified — Finder
browses phone tree and file downloads work over NFS. Exclusive create is
unresolved for Phase 2b writes. Resolution chosen: map exclusive→guarded in
the go-nfs fork (acceptable on a single-client USB device), plus add a
Commit() hook to the Handler interface so write completion triggers MTP upload.

**Reference:** `~/Labs/go-nfs/nfs_oncreate.go:43`

### 1a. Per-storage FSStat: macOS sends FSSTAT against the root file handle regardless of statfs(2) path (confirmed; option 1 dead)

**Symptom (2026-05-11):** With the multi-storage FSStat patch landed
(commit `5bfd2462`, [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md)
steps 1–3), `df -h` against two distinct storages on the Xperia
returns identical numbers for both:

```
terrace@gala comprador % df -h ./Internal\ shared\ storage
Filesystem         Size    Used   Avail Capacity ...
XQ-BT52.local:/   134Gi    18Gi   116Gi    14%   ...
terrace@gala comprador % df -h ./SD\ card
Filesystem         Size    Used   Avail Capacity ...
XQ-BT52.local:/   134Gi    18Gi   116Gi    14%   ...
```

Both report what looks like the **aggregate** (or the Internal-only
total, hard to tell without phone-side reference numbers). The
patched go-nfs handler forwards `path` to `Handler.FSStat`; our
handler matches `path[0]` against `sanitizeName(st.Description)`
for each storage. If both df calls hit the aggregate fallback,
`path` was empty on both invocations — i.e., macOS's NFSv3 client
sent the **root** file handle for both FSSTAT RPCs, regardless of
which subpath statfs(2) was invoked against.

**Hypothesis.** macOS optimizes FSSTAT by always sending the root
FH (since FSSTAT is semantically a filesystem-wide query). The
path we resolve from the handle is therefore always `[]`. The
patch is mechanically correct but doesn't get the information it
needs from the kernel.

**Diagnostic added (same commit, follow-up):** the FSStat handler
now logs `path=...` on every call; the storage-init log prints
`Description → sanitized` so we can compare verbatim. Re-run with
`make dev-nfs 2>&1 | tee build/dev-nfs.log` and we'll see what
macOS actually sends.

**If the hypothesis holds**, plan option 1 (path-via-FSStat-arg)
is structurally insufficient and we have to fall back to plan
option 2 (encode storage in the NFS file handle so `FromHandle`
yields path-and-storage). Option 2 is more invasive but doesn't
depend on the client's FSSTAT-path behavior.

**Diagnostic result (2026-05-14):** Hypothesis confirmed. With the
Xperia mounted and `df`-equivalent statfs invoked against both
`Internal shared storage/` and `SD card/`, the bridge logged
**13 FSStat calls, all with `path=[]`**:

```
FSStat path=[] → aggregate (no storage match) free=124131749888/total=144027406336
[× 13, varying free count as transfers progressed]
```

The aggregate `124 GB free / 144 GB total` is `92.2 + 31.9 = 124.1`
and `112.1 + 31.9 = 144.0` — sum of the two storages. Plan option 1
(path-via-FSStat-arg) is structurally dead: the NFSv3 FSSTAT RPC
carries only a file handle, and macOS resolves that handle to the
mount root, not to whichever subdirectory `statfs(2)` was invoked
against. The path the patched go-nfs forwards is therefore always
empty regardless of how many subdirectory levels deep the user
called `statfs`.

**Required fix is plan option 2: encode storage ID in the NFS file
handle.** The bridge already mints unique handles per object; the
addition is making the storage identifier recoverable from any
handle (e.g. high bits of the handle, or a side table). FSStat then
reads it from `FromHandle(fh).Storage` and dispatches to the right
LIBMTP storage struct. No dependence on client-side path forwarding.

Diagnostic log preserved at `build/dev-nfs-2026-05-14.log`.

**Status:** closed-as-diagnosed. Implementation of option 2 tracked
in [TODO.md](../TODO.md) under multi-storage follow-ups.

### 1b. Directory copy: some files do not make the jump (resolved — wrong destination path)

**Symptom (2026-05-11):** Architect copied a 432-file ECON101
directory tree from Mac to phone and reported "some files did
not make the jump." Initial diff against
`/tmp/comprador/SD card/Download/ECON101` showed only 36 of 432
files present — catastrophic loss on the face of it.

**Actual cause:** the destination was `Internal shared storage`,
not `SD card`. The directory copy succeeded in full; the
verification diff was reading the wrong storage. Re-running
against `/tmp/comprador/Internal shared storage/Download/ECON101`
showed **430 files matching by sha256 byte-perfect**, 1 file
missing (`iclicker_quizzes/.DS_Store` — a Finder metadata file,
not user content), and 1 hash mismatch (the top-level
`.DS_Store` — Finder legitimately regenerates this for the
destination directory).

The 270 "extra" files on the phone were all `._*`-prefixed
AppleDouble companion files Finder writes to non-HFS+ targets
to preserve extended attributes. This noise is a known polish
item — see [V0.3.3.md item #3](V0.3.3.md) "Filter `._xattr` /
`.AppleDouble` companion files" — but not a transfer fault.

**Lesson.** Verify destination paths explicitly before drawing
conclusions about transfer fidelity. A 432→36 mismatch is
dramatic enough to look like a deep bug, but the actual cause
was reading a different storage entirely. Pair "what files
appeared at X" with "what path is X" — they're not always the
path the user thinks.

**Status:** closed.

### 2. In-tree `helpers/memfs` root acknowledgement

**What happened:** The go-nfs test suite has a comment:
`// File needs to exist in the root for memfs to acknowledge the root exists.`
Without a file in the root directory, `fs.Stat("/")` may return an error
and the NFS GETATTR on the root handle fails, causing the mount to appear
empty or fail.

**Fix:** Always create at least one file at the root level before serving.
Our stub does this (`hello.txt`). The MTP adapter will naturally satisfy
this because storage roots always contain at least one child.

### 3. fileSync-hold WRITE incompatible with macOS NFS client RPC timeout (2026-05-16; reverted)

**Hypothesis (2026-05-14, commit `0d1418ac`):** macOS Finder's
end-of-copy WRITE carries `stability=fileSync`. If we hold that
WRITE's RPC response until the MTP send completes, Finder's
progress dialog will reflect the *real* end-to-end duration —
the dialog cannot dismiss until libmtp confirms the bytes are
durable on the phone. "Single source of truth in Finder's
progress dialog."

The implementation: patch vendored `nfs_onwrite.go` to type-assert
the `billy.File` to a new `DurableSyncer` interface; have
Comprador's `stagingHandle` implement it via `commitOnce`
(sync.Once over the existing idle-flush MTP push). One MTP send
per file regardless of which trigger (idle-flush, COMMIT,
fileSync, retransmit) reaches it first.

**Empirical verification (2026-05-16, Xperia XQ-BT52):**

- *9 KB file (`the-town-draft.md`)*: the mechanism worked
  end-to-end. WRITE arrived at 01:45:21.438 with `how=2`; MTP
  SendFile started at 01:45:21.442; idle-flush committed at
  01:45:21.507. The WRITE RPC was held for **69 ms** while the
  bytes were durably written to the phone. Finder dialog
  dismissed honestly at the commit.

- *9.09 GB file (`David.Attenborough...mkv`)*: the mechanism
  worked at the protocol level — all bytes verified on the phone
  after — but the **UX collapsed**. WRITEs filled the staging
  temp at memory speed (offsets 0 → 9 094 266 880); the final
  `how=2` WRITE arrived at 01:49:32.621; MTP SendFile started
  at 01:49:32.622; idle-flush committed at 01:56:39.244. The
  WRITE RPC was held for **7 min 7 s** (~21 MB/s,
  plausible USB 2.0 MTP rate). macOS NFS client surfaced
  *"Server connections interrupted: comprador"* at T+~20 s
  into the held WRITE, with options *Ignore* / *Disconnect All*.
  Clicking *Ignore* allowed the transfer to complete in the
  background but Finder showed no progress dialog for the
  remaining ~6 min 47 s.

**Root cause.** macOS's NFSv3 client has a kernel-side patience
window — ~20–30 s of no response on any single WRITE RPC and
the client tears down the TCP connection and surfaces the
"interrupted" alert. The bridge cannot legitimately stretch
this. Any file whose MTP send exceeds the threshold (~600 MB
at 21 MB/s) trips the alert; the dialog never even appears.
The historic always-`unstable` reply returned a less-honest
answer (Finder dismissed early on its own NFS-side flush) but
never broke the dialog.

**The architectural escape is FUSE-T.** FUSE-T's `write()` and
`fsync()` callbacks have no equivalent kernel RPC timeout class;
progress is paced by callback completions rather than by a
single network RPC. The `ux_unavoidable_wait.md` memory note
from 2026-05-07 named FUSE-T as "the only architectural escape"
for this class of problem; the 2026-05-16 fileSync-hold attempt
bumped into the same wall from a different angle and confirmed
the diagnosis. The deliberation is queued in
[TODO.md](../TODO.md) §On-return pickups.

**Status:** reverted in commit `9239dcd7`. Bridge unit tests
(`make bridge-test`) green against the revert. Branch
`claude/multi-storage` returns to pre-`0d1418ac` WRITE
semantics: WRITEs ack at memory speed; the idle-flush timer
fires the MTP send asynchronously. Finder's progress dialog
returns to dismissing early but no alert.

**Methodological lesson.** The hypothesis was sound and the
mechanism worked; the falsification was in the kernel-client
behavior assumption (that macOS NFS would tolerate a
multi-minute single WRITE). Cheaper to have measured macOS's
RPC timeout *before* shipping the change than after; the
running-bridge logs from any v0.3.x release with a 1+ minute
phone-side stall would have surfaced this. Filed as an
instance of the *run-the-syscall-first* lesson
(memory: `feedback_test_syscall_before_designing_helper.md`).

### 4. First drag-drop after mount silently stalls for ~5 minutes (open; reproducible 2026-05-16)

**Symptom (2026-05-16, both sessions):** On the *first*
Mac→phone drag-drop attempt after a fresh `mount -t nfs`, macOS
NFS client surfaces "Server connections interrupted: comprador"
at T+~20-30 s. The Finder dialog disappears (or never appears).
The bridge log shows **zero traffic** during the stall — no
LOOKUP, no CREATE, no WRITE, no ACCESS. After ~5 minutes of
silence, the kernel-side recovery completes on its own; the
bridge suddenly receives a burst of exclusive-CREATE probes
followed by the actual CREATE+WRITE+commit sequence. The
dropped bytes flow through, the file lands on the phone.

Two empirical receipts, both with the Xperia XQ-BT52:

- **Session 1 (build `c84db8cc-dirty`, pre-revert).** Bridge
  started 01:37:03. Architect mounted, browsed, attempted a
  drag around 01:40. Bridge log silent 01:40:07 → 01:45:20
  (5 min 13 s). Recovery at 01:45:20 produced the burst:
  3x exclusive-CREATE errors, then real CREATE+WRITE for
  `the-town-draft.md` (9 KB, succeeded at 01:45:21.4).

- **Session 2 (build `fb4135a8-dirty`, post-revert).** Bridge
  started 02:13:13. Architect mounted, browsed 02:15:15 →
  02:15:38, attempted a drag of `Red_Castle.html` (137 KB)
  around 02:16. Bridge log silent 02:15:38 → 02:21:16
  (5 min 38 s). Recovery at 02:21:16 produced the burst:
  4x exclusive-CREATE errors, then MTP SendFile for two files
  (`Red_Castle.html` and `2026-05-10_23-00-14_Claude_Chat_Bone_China_Prime.md`,
  both committed by 02:21:21).

- **Session 3 (build `786eeb69-dirty`, post-revert, after a
  clean macOS reboot).** Bridge started 02:35:18. Architect
  mounted at 02:37:40, browsed minimally (Internal storage at
  02:37:48, Download at 02:37:50), attempted drag at
  02:38:00 with `PXL_20260502_232127771.jpg` (2.3 MB photo).
  Bridge log silent 02:37:50 → 02:43:12 (**5 min 12 s**,
  within 1 second of session 1's stall duration). Recovery
  at 02:43:12 produced the burst: 2x exclusive-CREATE
  errors, MTP SendFile at 02:43:14.913, idle-flush
  committed at 02:43:15.160. The MTP send itself took
  ~247 ms; the rest of the wall-clock was pure kernel-side
  stall. *The stall reproduces across a clean reboot,
  confirming this is not a session-state accumulation
  bug.* Log preserved at
  `build/dev-nfs-2026-05-16-post-reboot.log`.

- **Session 4 (build at commit `00235ca`, v0.3.1 release
  merge of 2026-05-09).** Architect tested the load-bearing
  diagnostic late on 2026-05-16 (kept up by rain): stalls
  identically. This **rules out the branch as the cause**
  (`00235ca` predates all the substantive `claude/multi-storage`
  code changes — `5bfd2462`, `1c402e86`, `54225165`,
  `a3dd67f7`). The bug ships in every v0.2.x and v0.3.x
  release Comprador has cut. *Architect's framing: "substrate
  issue" — i.e. in the macOS NFS client ↔ localhost NFSv3
  server ↔ mDNS resolution layer, not in our application code.*
  This finding moots the planned `git bisect` and pivots the
  investigation toward the substrate boundary.

**The diagnosis arc and its mis-attributions (preserved as
methodological receipt):**

1. *First framing:* "0d1418ac fileSync-hold caused this."
   Reverted in `9239dcd7`. Stall reproduced on the reverted
   code. Wrong.
2. *Second framing:* "Pre-branch, possibly substrate issue —
   macOS NFS client interaction with our localhost server."
   Reinforced when session 4 (v0.3.1 release merge `00235ca`)
   stalled identically. Pointed toward kernel-side tuning and
   FUSE-T as substrate replacement.
3. *Third framing (correct, 2026-05-16 afternoon):* **The
   bridge silently drops every NFSv3 READ RPC.** The "stall"
   is not silence on the wire — it's the bridge ACK-ing TCP
   delivery while never sending RPC replies. macOS times the
   READs out and surfaces "Server connections interrupted"
   to the user.

**Root cause (verified by pcap analysis 2026-05-16):**

Captured `build/stall.pcap` during the post-reboot stall
(session 5, build `236e7e71-dirty`, drag at +25.98 s).
Parsed RPC layer with `build/pcap_rpc.py`:

```
total RPC calls:   261
total RPC replies: 220
unanswered calls:  44     ← every one is NFSv3 READ
```

Other operations (ACCESS, GETATTR, FSSTAT, LOOKUP,
READDIRPLUS, CREATE, SETATTR, REMOVE, COMMIT, WRITE, NULL)
all answered correctly throughout. **READ is the only RPC
type silently dropped.**

The unanswered READs target 5 distinct file handles — exactly
one per file in the destination directory `Download/`
(DESIGN.md, Attenborough.mkv, How_a_Computer_Works.webm,
nora_and_daniel-v1.1.md, phone-marker.txt). Reads arrive in
32 KB-aligned sequential chunks (offsets 0, 32768, 65536, …,
491520+ on two of the files). This pattern is **macOS
Spotlight indexing** triggered when Finder enters the
directory — Spotlight extracts thumbnails/previews/indexable
text from each file by reading the first ~512 KB.

**Why the bridge silently drops every READ:**
[`bridge/nfs/cache.go:39 → 65`](../bridge/nfs/cache.go).
`MTPFileSystem.OpenFile` → `cache.open(name, id, session)`
→ `download(entry, id, session)` →
`session.Do(MTPRequest{Op: OpGetFile, ObjectID: id, Writer: tmp})`.
The `session.Do` call **blocks until the entire MTP file has
been downloaded** into the staging temp. MTP has no
random-access read — `LIBMTP_Get_File_To_Handler` pulls the
whole file every time. While the download runs, the NFS
goroutine handling that READ RPC is asleep. macOS's NFS
client RPC timeout (~20–30 s) fires long before the download
completes for any non-trivial file. By the time the bridge
unblocks and tries to write the response, the kernel has
already timed the RPC out.

The bridge does not even log the download attempt:
`Device.GetFileToWriter` in `bridge/mtp/operations.go:325`
only logs on error, not on entry — which is why every prior
session's "bridge log silent during stall" observation was
misread as "no work happening." Work was happening; we
weren't watching at the right layer.

**Why the recovery happens at ~5 minutes:**
Spotlight's retry budget. After ~5 minutes of unanswered
READs on a file, macOS abandons the preview attempt and
moves on. Once Spotlight is no longer holding RPCs in
flight, Finder is freed to complete the unrelated drag's
CREATE+WRITE+commit (the write path does not go through
`cache.open`, so it is unaffected by the read backlog).

**Why this was never caught:**

1. Developer-side verification used `adb shell md5sum` against
   the phone directly, **bypassing the bridge entirely** (see
   `test-md5.sh`, the architect's letter 12). This confirms
   write durability but never exercises the bridge's READ
   handler.
2. End-to-end testing focused on Mac→phone (writes); a
   phone→Mac drag through Finder was never explicitly run.
3. Finder browse (READDIR/LOOKUP/GETATTR) doesn't trigger
   READ — only opening a file or Spotlight indexing does.
4. The Spotlight indexing is invisible to the user; they have
   no awareness that their drag-into is being held up by an
   unrelated background read.

v0.2.x and v0.3.x both ship this bug. Every user who put any
file on their phone (via Comprador or otherwise) and then
opened that directory in Finder has hit this. The user-visible
symptom is the "Server connections interrupted" alert; the
hidden harm is that **phone→Mac reads have never actually
worked through Comprador**.

**Fix space:**

| Approach | Fixes? | Cost | Notes |
|---|---|---|---|
| Block Spotlight via `.metadata_never_index` at mount root | Kills the Spotlight-induced symptom | Tiny | Doesn't fix actual read-from-phone; Finder open-file would still hang on large files |
| Return `NFS3ERR_JUKEBOX` on READ for files > threshold | Both: Spotlight gives up gracefully, Finder shows "still preparing" | Moderate | NFS-spec-blessed semantics for "media not ready"; need to confirm Finder honors it |
| Pre-cache files at directory enter | Defers, doesn't fix | High | Impractical for large devices |
| FUSE-T migration | Sidesteps NFS-client-timeout class entirely | Week+ | Substrate replacement; deliberation still queued |

Approaches 1 and 2 are the v0.4.0-shippable candidates.
Likely shipping order: **1 first** (eliminates the
user-visible symptom on every fresh-mount-then-Finder-browse
scenario, which is what every user will do), **2 next**
(makes the rare case of opening a large file from the
phone fail-gracefully rather than hang).

**Bridge log + pcap artifacts:**
- `build/dev-nfs-2026-05-16.log` — sessions 1, 2.
- `build/dev-nfs-2026-05-16-post-reboot.log` — session 3.
- `build/dev-nfs-stall-probe.log` — session 5 (pcap capture).
- `build/stall.pcap` — full lo0 packet capture during the
  stall window of session 5.
- `build/pcap_analyze.py`, `build/pcap_dissect.py`,
  `build/pcap_rpc.py`, `build/pcap_read_args.py` — pure-stdlib
  Python analyzers used to extract the root cause. Reusable
  for future NFS-layer investigations.

**Status:** root cause identified. Fix selection in
progress (see TODO.md §NEXT SESSION).

**Empirical receipts for fix attempts (2026-05-16 evening):**

- **Approach 1 — `.metadata_never_index` sentinel** (commit
  `56c44372`). **Insufficient.** Sentinel correctly silences
  Spotlight content indexing — verified by clean 4-minute
  browse with no READ probes. But the actual culprit is
  QuickLook thumbnail extraction, which fires on Finder icon-view
  rendering and **does not respect `.metadata_never_index`**.
  Confirmed by instrumented bridge (commit `78eae7a3`): on the
  next drag-into-directory test, the bridge logged sequential
  reads of every file in `Download/` including hidden
  `.trashed-*` files, with the 1 GB Shrek file blocking the
  read pipeline for 36 s and the 9 GB Attenborough.mkv set to
  take ~7 min. Sentinel kept for orthogonal benefit (Spotlight
  *content* indexing still suppressed) but does not address
  QuickLook.

- **Approach 2 — `NFS3ERR_JUKEBOX` for files > 50 MB**
  (commit `1acdf7f7`). **Partially effective.** Verified
  2026-05-16 20:54: with bridge `1acdf7f7-dirty`, mounted via
  loopback + Finder icon-view of `Download/`, the bridge
  correctly returned JUKEBOX for Attenborough.mkv (9 GB) and
  How_a_Computer_Works.webm (133 MB) on every probe. Small
  files (98 KB jpg) went through the synchronous fast path in
  ~12 ms. macOS NFS client retried the JUKEBOX'd reads with
  exponential backoff (4 s → 8 s → 16 s → 30 s). **However,
  macOS Finder still surfaced "Server connections interrupted"
  alert after a few retries** — JUKEBOX is the spec-blessed
  "media not ready, retry later" status but macOS treats
  repeated JUKEBOX as a connection failure for user-display
  purposes. The mount stays functionally alive — drags into
  the directory still work normally during the retry storm.
  This is the "outcome 3" anticipated in PLAN-NFS-READ.md.

- **Outstanding: async prefetch on JUKEBOX** (drafted in
  [PLAN-NFS-READ.md](PLAN-NFS-READ.md) but not yet
  implemented). The mitigation for outcome 3: kick off an
  asynchronous background download when we return JUKEBOX, so
  the client's retry within the backoff window finds a
  populated cache and gets the bytes. Should silence the
  alert because Finder gets a real response on retry rather
  than another JUKEBOX. Deferred to a future session;
  expected ~1 day of careful work (state machine in
  `cache.go`, eviction interaction, concurrent-read coordination).

**Net status after 2026-05-16 evening:** the dominant user
scenarios (mount + browse, drag-drop into directory) work
without scary alerts. The scenario that still fails noisily
is *icon-view rendering of a directory containing files
> 50 MB* — Finder shows alerts after JUKEBOX retries
exhaust. Functional impact is bounded: the mount remains
usable, drags succeed, only the icon-view preview generation
for large files is degraded (which is acceptable: a 9 GB
video has no useful thumbnail anyway).

Double-clicking a large file to preview it directly is
**untested with the JUKEBOX patch** — last attempt (with
patch active but for a slightly earlier reason)
required a reboot. Speculatively safer with JUKEBOX active
since the synchronous download path is bypassed, but
empirically unverified.

**Update 2026-05-17 morning — double-click-Attenborough verified
with JUKEBOX:** the architect double-clicked the 9 GB
Attenborough.mkv via Finder, which launched VLC. VLC issued
NFSv3 READ; bridge returned JUKEBOX; macOS NFS client retried
with exponential backoff (4 s → 8 s → 16 s → 30 s → 30 s …).
The bridge stayed healthy throughout (0.27 CPU-seconds total
over 4 minutes, all idle). **VLC, however, hung indefinitely**
on the `read()` syscall — it has no JUKEBOX-aware retry budget
and the macOS NFS hard mount retries forever. **Force Quitting
VLC recovered cleanly without rebooting the system. The mount
survived; Finder still worked; drags into other directories
were still possible.**

This is a substantial improvement over pre-fix behaviour (which
required a full system reboot to recover from the synchronous
9 GB download) but confirms a **fundamental limitation of
JUKEBOX-only**: it works for clients with their own
timeout-and-give-up logic (Finder / QuickLook surface a
dismissable alert) but does not work for clients that do
straight `read()` syscalls (any media player, `cat`,
`md5sum`, …). Those apps hang at the syscall layer because
the kernel keeps retrying forever and the bridge keeps
returning JUKEBOX forever.

**Async prefetch on JUKEBOX is now confirmed required**, not
optional. The cleanest design (per
[PLAN-NFS-READ.md](PLAN-NFS-READ.md)): when we return JUKEBOX
for a large file, kick off the libmtp download asynchronously.
The first few retries continue returning JUKEBOX while the
download runs. Once the cache is populated, the next retry
succeeds and the app gets bytes. The user-visible UX becomes
"the app is loading" for the duration of the libmtp download
(~7 min for Attenborough at USB-MTP rate) instead of
"the app is permanently hung." Worse than instant, much
better than hang-forever.

**Verification 2026-05-17 — async prefetch shipped (commit
`a405ed48`):** end-to-end test with the Xperia + VLC +
Attenborough.mkv (9.09 GB):

- 11:58:42.744 — VLC issues NFS READ; bridge returns JUKEBOX
  and kicks off `cache.beginPrefetch START` in parallel
  goroutines for both Attenborough.mkv and the smaller
  How_a_Computer_Works.webm (134 MB).
- 11:58:47.211 — webm prefetch completes in 4.5 s.
- 12:04:24.817 — Attenborough prefetch completes in
  **5 min 42 s** (= 27 MB/s, faster than the 21 MB/s estimate;
  likely USB 3.x).
- 12:04:24.832 — next VLC retry hits the cache, logs
  `READ prefetched-cache-hit`, falls through to the normal
  read path. **VLC starts playing.**
- 12:04:24.83+ — hundreds of sequential `prefetched-cache-hit`
  reads as VLC streams the file content.

Outcome: **user waits ~6 minutes with VLC's loading UI**
(instead of permanent hang on the pre-prefetch builds), then
the file plays normally. Force Quit no longer required. The
mount stays alive for other reads/writes throughout, *except*
that other MTP operations queue behind the running prefetch
(libmtp's single-session-goroutine serialization). The
architect observed they "cannot browse other directories"
during the prefetch window — this is the existing within-device
concurrency limitation, not a regression introduced by the
prefetch. It would apply equally to a foreground 9 GB
phone→Mac copy.

QuickLook icon-view alert also reduced: yesterday's tests
showed multiple stacked alerts; with prefetch, only one alert
fired and the file became previewable on cache populate.

**2026-05-18 morning — v0.3.3 shipped, immediately retracted.**
The same `a405ed48` async prefetch code, with no source changes
between the 2026-05-17 verification above and this morning,
produced a **whole-system cascade-freeze** on first reproduction
after v0.3.3 was tagged + released:

- Architect installed CI-built v0.3.3 DMG from GitHub Releases
- Sent a small HTML file into `Internal Shared Storage/Download/`
  (which contained Attenborough.mkv 9 GB + a 139 MB documentary)
- Finder copy progress hung
- Finder stopped responding to clicks
- Dock failed to launch processes
- Keyboard input died in regular apps
- Recovery required SSH-from-phone-via-tmux + `sudo killall -9
  bridge` + `sudo umount -f`

Forensics in `/Library/Logs/DiagnosticReports/`:

- `bridge_2026-05-18-160548_gala.diag` — bridge writing 2.15 GB
  file-backed memory in 196 sec (libmtp downloading to prefetch
  cache temp file)
- `bridge_2026-05-18-161814_gala.diag` — same shape, different run
- `bridge_2026-05-18-165146_gala.diag` — same shape, the
  reproduction directly observed
- `Comprador_2026-05-18-165532_gala.cpu_resource.diag` — Swift
  app at 92% CPU in its stderr `readabilityHandler` closure
  (the `NSConcreteFileHandle._monitor` callback inside
  `BridgeProcess.start(useNFS:...)`), hot-looping to drain the
  bridge's per-read logging firehose

Forensically reconstructed chain: small WRITE landed fast →
parent dir mtime bumped → Finder icon-view fired READs against
directory members → Attenborough.mkv hit JUKEBOX threshold →
`cache.beginPrefetch` dispatched a goroutine calling
`session.Do(OpGetFile)` → libmtp session goroutine locked for
the full ~5 min download → all other NFS RPCs queued indefinitely
→ kernel marked mount "not responding" (`Status flags: 0x2` per
`nfsstat -m`) → `hard,nointr` mount option cascaded the
unresponsiveness to every system process that touched the mount
path.

**2026-05-18 evening — same code, different machine state, no
freeze.** Architect rebuilt commit `92d4e6d5` (code-equivalent to
the retracted v0.3.3 — the chain from `92d4e6d5` to the
merge `8f818bc1` is one docs-only commit) and tested the same
reproduction shape (drag small HTML into `Download/` containing
Attenborough + 139 MB doc). **Worked end-to-end.** No system
freeze. No "Server connections interrupted" alert. No bridge
death.

The diff between the two runs is environmental, not source:

| | Morning run (cascade) | Evening run (clean) |
|---|---|---|
| Source code | `a405ed48` (via `8f818bc1`) | `92d4e6d5` (same source) |
| Binary | CI-notarized DMG from GitHub Release | Locally-built `make app-swiftc` |
| Phone | Xperia XQ-BT52 | Xperia XQ-BT52 |
| Destination dir contents | Attenborough + 139 MB doc | Attenborough + 139 MB doc |
| Drag target | Small HTML | Small HTML |
| Outcome | System cascade-freeze | Clean end-to-end |

Between the runs:
- Multiple `killall Comprador`, `killall -9 bridge`, `killall
  Finder` cycles
- `make clean` runs that wiped build artifacts
- `/Applications/Comprador.app` deleted and reinstalled (different
  bundle each install)
- `~/Library/Application Support/Comprador`, `~/Library/Caches/
  com.comprador.app`, `~/Library/Preferences/
  com.comprador.app.plist` all deleted
- Orphan `dns-sd` for `Comprador-XQ-BT52` killed
- Xperia physically unplugged + replugged at some point
- Worktrees touched
- Probably mds_stores / fseventsd state shifted

**Verified via log grep 2026-05-18 evening:**

Architect re-ran `log stream --predicate 'process == "bridge" OR
process == "Comprador"' > /tmp/bridge-92d4e6d5.log` covering the
drag-and-drop window. Grep for `cache.beginPrefetch|JUKEBOX|onread`:
**zero hits.** The bridge's per-read logging (which produced
hundreds of stderr lines per second during the morning's cascade,
visible in the Comprador 16:55 .diag's heaviest stack) was silent
during the evening's drag. The Swift parent's `readabilityHandler`
NSLogs every chunk of bridge stderr into unified logging via
"Comprador bridge: %@" — so absence in the unified log means the
bridge didn't write those lines, which means the prefetch path
never engaged.

**Conclusion: the cascade is gated on Finder/Spotlight READ-probe
activity, not on our code path running unconditionally.** The morning
cascade required three things to align:

1. Bridge running with `cache.beginPrefetch` dispatchable (any
   code path that issues a >50 MB MTP READ against a known-large
   phone-resident file)
2. **Finder/Spotlight/QuickLook actively issuing parallel READs**
   against directory members in the prefetch-eligible size range —
   typically when the volume is new to Spotlight's per-volume
   index, icon-view is rendering thumbnails, or a fresh
   `mds_stores` is crawling
3. `hard,nointr` mount semantics turning the resulting libmtp lockup
   into a system-wide cascade

The morning's reproduction had all three. The evening's reproduction
had (1) and (3) but not (2). So the cascade didn't happen — not
because the code is non-deterministic, but because the **trigger
condition** (Finder probing during the drag window) didn't fire.

**What this tells us:**

1. **The cascade mechanism is real** — the .diag forensics are
   unambiguous. The 2.15 GB file-backed-write trail through
   `runtime.asmcgocall → write` is exactly what `cache.download`
   does. The mechanism reliably cascades the system *when* it
   fires.
2. **The cascade trigger is deterministic on Finder behavior**,
   which is itself a function of macOS's per-volume indexing
   state. Conditions that increase trigger probability:
   - First-time-seeing-this-volume Spotlight crawl (fresh
     `XQ-BT52.local` or fresh phone)
   - Fresh QuickLook thumbnail generation backlog (no cached
     thumbs for the directory contents)
   - QuickLook backlog from a previous session unable to
     complete (large-file READs queued from a prior crash)
   **View mode is not the gating variable** — architect confirmed
   2026-05-18 evening that both morning's cascade run and
   evening's clean run were in **icon view**. So the differentiator
   between them is purely macOS's per-volume indexing/thumbnail
   cache state, which was cold in the morning (fresh DMG install,
   fresh mount) and warm by the evening (many mount/unmount
   cycles + cache wipes that didn't touch `mds_stores`).
   The *worst-case user scenario* is exactly the launch demo:
   first-time user, plugs phone in, the mount is brand-new to
   Spotlight, Finder is in icon view (the default), user drops
   a file. **First-impression cascade.**
3. **A bug that fires under specific-but-common conditions
   is still unshippable.** A bug that "always fires on first
   user encounter, then mysteriously stops repro'ing on
   subsequent attempts" is *worse* than a deterministic one —
   it makes the architect look incompetent ("works on my
   machine") while sandbagging every new user.
4. **The fix direction is unchanged.** Chunked-yield prefetch
   (PLAN-PREFETCH-REDESIGN.md Option D) breaks the mechanism;
   soft-mount safety boundary (Step 5) catches any future
   unrelated fault.

**What we don't know:** which exact Finder-behavior delta gates
the trigger. Could be (a) Spotlight's per-volume first-encounter
backlog, (b) Finder view mode at drag time, (c) QuickLook
backlog, (d) something else. Resolving (a)/(b)/(c) is interesting
but not load-bearing for the fix — the redesign neutralizes the
mechanism regardless of which trigger condition obtains.

**2026-05-18 evening (later) — step2 variant tested, prefetch fired,
no cascade.** First test through the post-letter-15 harness. Build
`54c01f78` carries (i) the priority-queue refactor in
`bridge/mtp/session.go` (no PriorityLow callers — behaviourally a
no-op for current execution paths) and (ii) the strict
`scripts/test/clean.sh` Volumes/ check.

Pre-flight: `recover.sh` clean state, `Volumes/` absent, no
Comprador processes. Procedure: `setup.sh step2` →
`mdutil -E "<mount-path>"` → architect copied
`How_a_Computer_Works.webm` (135 MB) into `Download/` (which already
contained Attenborough.mkv 9 GB + the original webm). `finish.sh step2`.

Log: `/tmp/test-step2-194515.log` (311 lines).

| Signature | Count | What it means |
|---|---|---|
| `cache.beginPrefetch START` | 4 | Prefetch path engaged for Attenborough, webm (id=19), `.nfs.20051025.1b50` silly-rename, webm (id=36) |
| `cache.download START` | 3 | One per distinct MTP object descent |
| `cache.download END (OK)` | 4 | All completed (3 unique + the original webm under id=19 hidden by silly-rename) |
| `READ JUKEBOX` | 10 | NFS client retried 7× for `.nfs.20051025.1b50` in 3 s |
| `not responding` | **0** | No kernel mount-down signal |
| `Server connection` | **0** | No Finder "interrupted" alert |
| `cache.open` per-RPC storm | **0** | Stderr quiet during the wait |
| `Adding notification request CE85` | 0 | No USB-claim race |

Session-goroutine serialization confirmed: Attenborough.mkv took
4m16s on the libmtp wire; the webm (id=19) entered the channel
buffer at the same millisecond and completed 4 s later (so the
session goroutine pulled it the instant Attenborough returned and
the smaller libmtp transfer finished in ~5 s). This is what the
single-channel implementation would have done before Step 2 —
exactly as predicted, since no caller sets `PriorityLow` yet.

**Verdict bucket** (per the pre-committed criteria, *not* re-framed
after the run): row 3 — **`cache.beginPrefetch START` present, no
cascade fired.** Datapoint of interest.

**Honest accounting of what this run does and does not show:**

- **Does show:** the harness fix held (no `clean.sh` cascade,
  `finish.sh` ran to completion), Step 2's priority queue is wired
  correctly and inert (no deadlock, no goroutine leak, no behaviour
  delta vs single-channel for the same call site set), and the
  cascade trigger conditions met but did not fire — the session
  goroutine was tied up for 4+ minutes returning JUKEBOX on every
  parallel READ, and macOS did not mark the mount "not responding."
- **Does not show:** that Step 2 "fixed" or "changed" the cascade
  in any way. Step 2 has zero `PriorityLow` callers — claiming
  otherwise would be precisely the imprecise framing this section
  burned a day discovering it must not do. The cascade trigger is
  what failed to fire, not the mechanism.

**Candidate Finder-state deltas vs the morning's cascade run:**

- The 2026-05-18 morning was post-fresh-DMG-install, post-mount-with-
  cold-Spotlight on the volume.
- This evening was post-`recover.sh`-cleanup, post-many-mount-cycles
  across the day, with `mdutil -E` flush invoked *after* the mount
  came up (not "never been seen by Spotlight" cold, but
  "index flushed and pending re-crawl" cold — a different regime).

This narrows but does not pin down the gate. The pending control
is a same-procedure run against `prod` (HEAD `32ee45cd` ad-hoc,
no Step 2 commits) under the same post-letter-15 system state.
If `prod` *also* runs clean, the gate is environmental. If `prod`
cascades, then either (a) something between `32ee45cd` and
`54c01f78` other than the priority queue suppresses it, or (b)
the priority-queue refactor has an effect even with no
`PriorityLow` callers — both worth investigating before declaring
Step 2 a true no-op.

**2026-05-18 evening (later still) — prod control run also clean,
environmental-gate hypothesis confirmed.** Same procedure
(`setup.sh prod` → `mdutil -E` → copy a webm into `Download/`).
Log: `/tmp/test-prod-200338.log` (264 lines).

**Side-finding via build-identity stamp**: `prod` did *not* match
the README's claimed `32ee45cd`. The bundle's bridge log emits
`Comprador build: 6941a487-dirty` on startup. So the variant
labeled "HEAD ad-hoc, cprLog-converted" was actually built when
HEAD was `6941a487` (the chain-Pane-B harness commit) with the
cprLog conversion in the working tree but not yet committed. The
README was point-in-time correct when written and then drifted.

This is *the* use case for build-identity stamping — without
the in-bundle BuildID, the comparison would have been
silently miscalibrated. The README is corrected separately;
**trust the in-binary stamp, not the variants table.**

The tighter comparison this actually represents:

| Variant | Build identity | Includes priority queue? |
|---|---|---|
| `prod` | `6941a487-dirty` | No (one commit before f5db97cd) |
| `step2` | `54c01f78` | Yes (no `PriorityLow` callers) |

Side-by-side log signatures:

| Signature | step2 | prod |
|---|---|---|
| `cache.beginPrefetch START` | 4 | 3 |
| `cache.download START` | 3 | 3 |
| `cache.download END (OK)` | 4 | 3 |
| `JUKEBOX` returns | 10 | 3 |
| `not responding` | **0** | **0** |
| `Server connection` | **0** | **0** |
| `cache.open` storm | **0** | **0** |
| `Adding notification request CE85` | 0 | 0 |

Both serialized through the session goroutine. Different FIFO
ordering across runs — step2 dispatched Attenborough first (4m16s)
then webm (4s); prod dispatched webm first (6.2s) then Attenborough
(5m45.1s, channel-wait included). Ordering is driven by which NFS
READ arrived at the bridge first, not by any code-path
difference. **Both completed cleanly.** The user-visible drag
landed in both cases after the prefetch window.

**Conclusion: the cascade trigger is environmental, not gated on
code between `6941a487` and `54c01f78`.** Step 2 is now also
confirmed to have no detectable behaviour delta vs. the
harness-only twin — as predicted, since the priority lane has no
`PriorityLow` callers yet.

**Sharpened gate framing:** the cascade requires a high JUKEBOX-
return rate (many parallel READs hitting the size threshold during
the prefetch window). The morning's .diag forensics showed many
concurrent prefetches; this evening's runs saw only 1–2 active
at a time. The Spotlight/QuickLook probe rate is the proximate
driver — itself a function of Spotlight's per-volume-index cold/
warm state. We have not pinned the exact macOS-side variable
because we cannot trivially reproduce "Spotlight has never seen
this volume" without a reboot, a new mount-point path, or a
DMG-install cycle.

**What this tells us about Step 3:** the chunked-yield prefetch
still removes the mechanism even if the morning-regime trigger is
hard to reliably reproduce. A bug that fires on first user
encounter (cold Spotlight) and then stops repro'ing
(warm Spotlight) is precisely the user-impact shape worth fixing
unconditionally. The conclusion from the morning's MISTAKES update
("A bug that fires under specific-but-common conditions is still
unshippable") is reinforced, not weakened, by today's clean runs.

**Methodological notes for the next cascade-investigation round:**

- Build-identity stamping caught a silent README/binary drift
  that would otherwise have miscalibrated this control. Keep
  it on every variant; never trust a label without verifying the
  stamp.
- A genuine "cold Spotlight" reproduction probably needs either
  a reboot or a fresh mount-point path (Comprador currently puts
  mounts at `~/Library/Application Support/Comprador/Volumes/
  <DeviceName>`; using a per-test-run subdirectory would
  guarantee Spotlight has never indexed it). Worth considering
  if we want to make the morning's regime reproducible at will.
- The recover.sh path-with-spaces bug exposed today (umount
  silently failing on the actual mount path because awk-$3
  tokenized "Application Support") would have masked far worse
  failures had clean.sh's new gate not refused to walk into the
  stale mount. The harness-as-self-test property letter 15
  argued for has paid for itself within hours of landing.

**2026-05-18 night — Step 3 shipped, yield test passes, cascade
mechanism broken by construction.** [Commit
`74702901`](../bridge/nfs/cache.go), `claude/prefetch-redesign` HEAD.
`cache.download` now loops over the object in 16 MB chunks via
`OpGetPartial` at `PriorityLow` (Step 2's queue, commit `f5db97cd`).
Between chunks, the priority pump in `bridge/mtp/session.go` drains
high-priority requests before pulling the next low-priority chunk.

The discriminating test (not the cascade test — that one remains
environmentally gated tonight): **does a high-priority operation
arriving mid-prefetch land within the priority-pump's one-chunk
yield budget, instead of waiting the full multi-minute libmtp
transfer?** The yield test against build `74702901`, run
2026-05-18 ~20:41:

```
20:41:06.792  cache.beginPrefetch START  Attenborough.mkv (9 GB, id=14)
20:41:06.792  cache.download START       Attenborough  ← Step 3 chunked loop begins (PriorityLow)
20:41:12.586  Lazy enumerate /Download   ★ OpListDir (default PriorityHigh) landed during prefetch
20:41:13.642  OpenFile read-path         Red_Castle.html (137 KB)
20:41:13.642  cache.download START       Red_Castle.html  ← cache.download → PriorityLow (same lane)
20:41:13.825  cache.download END (OK)    dt=183ms  ← low-pri lane non-starving
```

Two complementary receipts, each evidencing a different invariant:

1. **The `Lazy enumerate` at 20:41:12.586** is the high-priority
   preemption signal. `OpListDir` is dispatched at the default
   `PriorityHigh` (the priority field's zero value), so its landing
   ~6 seconds into a multi-minute prefetch — between two of
   Attenborough's chunks — is direct evidence that the priority
   pump drains the high lane before pulling the next low-pri
   chunk. We don't have a duration measurement for the
   enumerate itself (no START log on `OpListDir`), but the fact
   that it landed at all during the prefetch window is the
   load-bearing observation.
2. **Red_Castle.html's 183 ms end-to-end completion** is the
   low-pri-non-starvation signal. Because `cache.download` always
   enqueues at `PriorityLow` — including on the synchronous
   `open()` path — Red_Castle.html's 137 KB read went into the
   same low lane as Attenborough's chunks. The 183 ms duration
   shows that low-pri requests queue behind one other low-pri
   chunk at most before being dispatched themselves. Well
   inside the 600 ms one-chunk ceiling the plan sized for.

Together, these are the falsifiable demonstration that the
priority pump works as designed: high-pri requests preempt
between low-pri chunks (#1), and low-pri requests serialize
without catastrophic starvation (#2). Log:
`/tmp/test-step3-203247.log`.

**Note on `open()` going through PriorityLow.** `cache.download`
runs unchanged for both background prefetch (via
`beginPrefetch`) and synchronous open (via `open`). Both paths
enqueue at `PriorityLow`. This is deliberate: a synchronous open
of a large file presents the same cascade-risk pathology as the
background prefetch did (multi-minute libmtp transfer locking
the session goroutine), so it gets the same chunked-yield
treatment. Small synchronous reads pay one chunk's worth of
yield latency in the worst case — acceptable.

**Cascade-fix analysis — is v0.3.3 fixed?**

The v0.3.3 cascade required four links:

1. Session goroutine locked for minutes on a single libmtp transfer.
2. Other NFS RPCs queueing indefinitely behind it.
3. macOS NFSv3 client marking the mount `not responding` (~30 s).
4. `hard,nointr` semantics cascading the unresponsiveness to every
   process touching the mount path.

Step 3 breaks link 1 by construction: chunks are bounded at 16 MB
and the priority pump drains high-pri requests between them, so
the session goroutine is never unavailable to high-pri work for
more than ~600 ms. The yield test is the direct measurement of
link 1 being broken — 183 ms is well under the 1 s `timeo=10`
first-timeout window, which is well under the ~30 s mount-down
threshold. Without link 1, links 2–4 cannot fire.

**Two honest caveats, both worth naming for posterity:**

1. We did not reproduce-then-suppress the cascade. The trigger
   conditions (cold Spotlight + Finder probe storm) did not fire
   tonight on prod, step2, or step3 — the environmental gate
   we narrowed earlier is still narrowed-but-not-pinned. The
   yield test is the empirical proxy: it measures the mechanism
   being broken, not the cascade being prevented in vivo. The
   two are equivalent given the chain analysis but they are not
   identical evidence.
2. The universal safety boundary (Step 5, soft/interruptible
   mount) is not yet shipped. Step 3 makes *this* class of bug
   impossible. A different bug — a different libmtp pathology,
   a deadlock we have not seen, a kernel-side substrate issue —
   could still produce the same chain if it locks the bridge
   for long enough. Step 5 catches any future fault regardless
   of cause; until it ships, "the v0.3.3 cascade cannot happen"
   is precise but "the system cannot cascade" is not.

**Status:** the v0.3.3 cascade as observed is fixed. The class of
cascade requires Step 5 to also be impossible. Both are in the
v0.3.4 release plan ([docs/PLAN-V0.3.4-RELEASE.md](PLAN-V0.3.4-RELEASE.md)).

## SMAppService / Helper

> **Section status — helper itself slated for v0.4.0 retirement.**
> The privileged helper is no longer invoked on the NFS mount path
> (per the entry below) and the only feature it still serves is the
> optional cosmetic `.local` hostname rewrite via `/etc/hosts`.
> [TODO.md "Tidying" Tier 3](../TODO.md) tracks the v0.4.0 decision
> to either drop that cosmetic entirely or migrate it to a one-shot
> root prompt at install time. When that lands, `helper/`,
> `HelperClient.swift`, the BUNDLE_HELPER Makefile recipe, the
> LaunchDaemon plist, and the SMAppService.daemon registration all
> go with it. The postmortem below remains the canonical receipt
> of what we learned designing-then-removing the helper.

### 1. The privileged helper was load-bearing for nothing

**What happened:** Phase 3 of the NFS pivot introduced a privileged
SMAppService daemon (`comprador-helper`) whose sole purpose was to
exec `mount_nfs` as root, on the assumption that `mount(2)` for NFS
volumes refuses unprivileged callers.

This produced a long list of downstream complications:

- An entire Go binary (`helper/`) running as root with a Unix-socket RPC
  protocol the GUI app talks to.
- LaunchDaemons plist embedded in the .app bundle.
- `SMAppService.daemon(plistName:).register()` flow on first run, with a
  System Settings → Login Items approval prompt.
- Notarization requirements for the helper signature to satisfy launchd's
  spawn checks.
- A `/var/db/com.apple.backgroundtaskmanagement/` (BTM) database state
  that, on the development machine, accumulated 16,975 failed-spawn
  records over a 24-hour debugging session and refused to recover even
  after `launchctl bootout`, fresh notarization, and a full reboot.

The session of 2026-05-08 was spent trying to surgically untangle BTM
without `sfltool resetbtm` (which would nuke every other Mac app's
login-item state). Six hours of archaeology.

The actual fact, verified by the empirical test that should have been
run on day one of the helper design:

```bash
mkdir -p /private/tmp/probe
mount -t nfs -o port=N,mountport=N,nfsvers=3,nolocks,tcp \
  localhost:/ /private/tmp/probe
echo $?  # 0
mount | grep probe
# localhost:/ on /private/tmp/probe (nfs, nodev, nosuid, mounted by terrace)
```

**`mount(2)` for NFS volumes on localhost accepts unprivileged callers,
applying `nodev,nosuid` flags as the safety floor.** The kernel was
willing the entire time; the helper layer was solving a problem that
didn't exist.

**Fix (commit `406e35e8`):** `MountManager.mountNFS` shells out to
`/sbin/mount` directly with the running user's credentials. Mountpoint
moves from `/Volumes/<phone>` (drwxr-xr-x root:wheel, mkdir-rejected for
unprivileged users) to `~/Library/Application Support/Comprador/Volumes/<phone>`
(user-owned). Finder still presents it as a Locations sidebar entry; the
volume label auto-derives from the mountpoint dirname, so a phone named
`XQ-BT52` shows up as `XQ-BT52` — without any of the helper's
mDNS/`/etc/hosts` hostname-rename machinery.

The helper code remains in tree for legacy WebDAV mounts and for
hostname cosmetics, but is no longer invoked on the NFS mount path.
SMAppService.daemon registration is no longer required for shipping,
which means BTM state corruption stops being a release blocker.

**Lesson:** before designing a privileged helper to launder root for a
single syscall, run that syscall as a non-privileged user and check
whether it actually fails. The empirical test would have taken thirty
seconds and saved a privileged-services architecture.
