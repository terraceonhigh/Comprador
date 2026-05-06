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

**Fix (not yet written):** Two complementary changes are probably needed.

1. The reattach handler should track a `pendingUnmount` flag (or
   equivalent) — if a reattach arrives while an unmount is in flight,
   queue the device-attach work for after the unmount completes rather
   than discarding it.
2. Even simpler: at the end of every successful unmount, check whether
   IOKit currently sees a matching device. If yes, synthesise an attach
   event ourselves to kick the connect path. That handles both this
   race and the broader "reattach while we're busy" case.

Workaround: physical unplug + replug fires a fresh attach event, which
breaks the deadlock. Tracked in TODO.md under "Handle detach during
file transfer gracefully (don't hang Finder)" — that entry was filed
before we'd reproduced this exact race, but the fix is the same shape.

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
