# Plan — multi-storage support

**Status — 2026-05-10:** scope and design notes for exposing
multiple device storages (Internal + SD card, primarily) through
the Finder mount. Borrows heavily from OpenMTP and SwiftMTP. The
plan is mostly *verification + closing a known gap*, not a
greenfield build — the bridge's data layer is already
storage-aware.

Decision the Architect has already made: **per-storage quota**,
not aggregate. Aggregate "X GB free of Y GB" reporting across
mixed storages is a cardinal sin — it can mislead the user into a
copy that fails partway through with no obvious cause.

## Where we already are

[bridge/mtp/session.go:296–323](../bridge/mtp/session.go) already
enumerates all storages at session open and registers each as a
top-level directory under `/` in the `ObjectMap`. Every
`ObjectMeta` carries its origin `StorageID`
([session.go:311–318](../bridge/mtp/session.go),
[fs.go:235, :252, :266, :350, :359](../bridge/nfs/fs.go)). The
lazy-enumeration path threads `StorageID` correctly through all
descendants
([session.go:340–377](../bridge/mtp/session.go)).

What's exposed today, mounting a phone with one storage:
- `/Internal storage/` → contents

What's exposed today, mounting a phone with two storages:
- `/Internal storage/` → contents
- `/SD card/` → contents

This already works. The bridge currently picks no preferred
storage — they're all visible side-by-side at the mount root.

## The gap that matters

[bridge/nfs/handler.go:38–43](../bridge/nfs/handler.go) implements
`FSStat` (the NFS RPC behind `statfs(2)` and Finder's "X GB
available" string) by summing across all storages:

```go
func (h *mtpNFSHandler) FSStat(_ context.Context, _ billy.Filesystem, s *gonfs.FSStat) error {
    s.TotalSize = h.session.TotalBytes()
    s.FreeSize = h.session.FreeBytes()
    s.AvailableSize = h.session.FreeBytes()
    return nil
}
```

`Session.FreeBytes()` and `TotalBytes()`
([session.go:155–180](../bridge/mtp/session.go)) sum
`MaxBytes` / `FreeBytes` across every storage on the device. The
result is aggregate. If a 128 GB Internal is at 100 GB free and a
64 GB SD card is at 5 GB free, Finder reports **105 GB free of
192 GB total** to the user, no matter which storage they're
standing in. A user dragging a 50 GB file into the SD card gets
green-lit by Finder, the copy starts, fails after 5 GB. That's
the cardinal sin.

## Goal

`statfs(2)` from inside `/Internal storage/` returns Internal's
free/total. Same `statfs` from inside `/SD card/` returns the SD
card's. The user always sees accurate numbers for the storage
they're currently in. Cross-storage browsing still works (both
storages visible at root), but each storage's quota is its own.

## The complication

`go-nfs`'s `Handler.FSStat` signature
([vendored at handler.go interface](../bridge/vendor/github.com/willscott/go-nfs/handler.go)):

```go
FSStat(context.Context, billy.Filesystem, *FSStat) error
```

It does **not** receive the requesting path. The path *is*
resolved from the file handle in
[nfs_onfsstat.go:16](../bridge/vendor/github.com/willscott/go-nfs/nfs_onfsstat.go)
(`fs, path, err := userHandle.FromHandle(roothandle)`) — but the
handler call on line 35 only passes `(ctx, fs, &defaults)`. So the
bridge has no way to know which path the kernel is asking about
without changing the interface or working around it.

Three options:

1. **Patch vendored go-nfs to pass path to FSStat.** Three-line
   change: extend the `Handler.FSStat` signature to accept the
   resolved path, update the one call site to pass it. The
   change is local to our vendored copy; we already vendor with a
   patch (`nfs_onwrite.go` unstable-write semantics, see
   [docs/GO-NFS-NOTES.md](GO-NFS-NOTES.md)). Worth proposing
   upstream after we prove it.
2. **Encode storage in the NFS file handle.** Handles are opaque
   bytes to the protocol. Our handle could embed the storage ID.
   Then `FromHandle` would yield path-and-storage and we could
   route without a path argument. More invasive than option 1.
3. **Best-effort guess from `billy.Filesystem`.** The handler
   receives `fs billy.Filesystem` which is our `MTPFileSystem`.
   It carries enough internal state to look at "most recently
   READDIR'd path" or similar — but that's racy and would not
   survive concurrent operations on two different storages.

**Recommendation: option 1.** It's small, it's the right shape,
it's worth upstreaming.

## What to borrow

- **OpenMTP's per-storage routing pattern**
  ([FileExplorerKalamDataSource.js:82–110](../../openmtp/app/data/file-explorer/data-sources/FileExplorerKalamDataSource.js)):
  every API call threads `storageId` as a parameter. Our bridge
  already does this through `StorageID` on every `ObjectMeta`.
  Confirmed-and-aligned with their pattern.
- **OpenMTP's storage description: phone-side verbatim**
  ([helpers.go:69–80](../../openmtp/ffi/kalam/native/helpers.go)).
  They don't relabel. We do `sanitizeName(st.Description)` for
  POSIX safety
  ([session.go:310](../bridge/mtp/session.go)) — keep that, since
  spaces in storage names break some Finder paths.
- **SwiftMTP's per-storage capacity display**
  ([SidebarView.swift:178–181](../../SwiftMTP/SwiftMTP/Views/SidebarView.swift)):
  inline progress bar + "X free of Y" text per storage. Our
  equivalent is whatever Finder shows; per-storage `FSStat` gives
  Finder the right numbers to render.
- **Both projects: no cross-storage copy**. They make the user
  switch storages, copy out, switch, copy in. Finder's drag-drop
  semantics over our mount already implement this naturally — a
  drag from `/Internal storage/` to `/SD card/` does a download
  + upload through the NFS mount, with the data round-tripping
  through the Mac. Acceptable; same as the references.

## Concrete changes

### 1. Patch vendored go-nfs to pass path to FSStat

```go
// bridge/vendor/github.com/willscott/go-nfs/handler.go
type Handler interface {
    // ... other methods ...
    FSStat(context.Context, billy.Filesystem, []string, *FSStat) error
}
```

```go
// bridge/vendor/github.com/willscott/go-nfs/nfs_onfsstat.go:35
err = userHandle.FSStat(ctx, fs, path, &defaults)
```

Add a `Why:` comment in nfs_onfsstat.go pointing at this doc and
the upstream-PR-pending status.

### 2. Per-storage FSStat in the bridge

```go
// bridge/nfs/handler.go
func (h *mtpNFSHandler) FSStat(_ context.Context, fs billy.Filesystem, path []string, s *gonfs.FSStat) error {
    // Resolve the path's storage. The path[0] segment is the
    // sanitized storage description (matches what we Put() at
    // session.go:310). An empty path or unknown storage falls
    // back to aggregate so we never error a statfs.
    storage := h.session.StorageForPath(strings.Join(path, "/"))
    if storage == nil {
        s.TotalSize = h.session.TotalBytes()
        s.FreeSize = h.session.FreeBytes()
        s.AvailableSize = h.session.FreeBytes()
        return nil
    }
    s.TotalSize = storage.MaxBytes
    s.FreeSize = storage.FreeBytes
    s.AvailableSize = storage.FreeBytes
    return nil
}
```

`Session.StorageForPath(path)` is a small helper that walks the
first path segment, matches against
`s.storages[*].Description` (after `sanitizeName`), returns the
matching `Storage` or `nil` for root / unknown.

### 3. Per-storage FreeBytes refresh

Currently snapshotted at session open
([session.go:170–180](../bridge/mtp/session.go), comment is explicit
about this). Carries the same staleness problem [V0.3.3.md
item #4](V0.3.3.md) calls out for aggregate. Two options:

- **Refresh on FSStat itself.** Every FSStat call re-queries
  libmtp for the relevant storage's `FreeBytes`. macOS issues
  FSStat infrequently (~once per Finder window focus + once per
  copy preflight); cost is a few hundred ms once or twice per
  drag. Acceptable.
- **TTL'd refresh.** Cache 10s; re-query if older. Cheaper if
  Finder gets chatty.

**Recommendation: refresh on FSStat call.** Simpler, low-cost in
practice, and "Finder shows fresh numbers" is the user-visible
behavior we want. If profiling later shows it's expensive, add
TTL.

`libmtp` doesn't expose a single-storage refresh as far as I
know; `LIBMTP_Get_Storage` re-fetches all of them. Acceptable —
we do the full refetch on FSStat and update all entries.

### 4. Storage hot-plug (defer)

If the user inserts an SD card mid-session, our `s.storages`
list is stale. Same for hot-removal. This is real but rare and
edge-case enough to defer. Closing the gap requires periodic
re-enumeration or USB-event-driven re-enumeration on the bridge
side. **Mark explicitly out of scope for this implementation;
file separately if it becomes a complaint.**

### 5. Storage name collision

Two storages with the same description (some phones report two
"Internal storage" entries — rare but real). `sanitizeName`
collapses both to the same path → bug. Fix: in `initStorages`,
if a sanitized name collides with an existing entry, append a
disambiguator (`-2`, `-3`).

```go
// pseudocode in initStorages
seen := map[string]int{}
for _, st := range storages {
    base := sanitizeName(st.Description)
    if n := seen[base]; n > 0 {
        base = fmt.Sprintf("%s-%d", base, n+1)
    }
    seen[sanitizeName(st.Description)]++
    storagePath := "/" + base
    // ...
}
```

## Sequence

1. **Patch vendored go-nfs** — pass path to FSStat. Small diff.
   Verify against current single-storage device that nothing
   regresses.
2. **Implement per-storage FSStat in bridge** — `StorageForPath`
   helper + handler change. Verify by mounting a phone, running
   `df -h /Volumes/<phone>.local/Internal\ storage` and same for
   `/SD card/`.
3. **Per-storage refresh on FSStat.** Verify by writing a file
   to one storage, waiting for Finder to refresh, confirming the
   free-space number drops by the file size.
4. **Storage-name collision handling.** Synthetic test with a
   device that fakes two same-named storages, or just unit-test
   `initStorages` directly.
5. **Phone-with-SD test** — borrow or use the Architect's
   physical device(s). Verify the cardinal-sin scenario doesn't
   trigger anymore.

## Out of scope

- **Storage hot-plug.** Filed above; defer.
- **Multi-device** (one phone + a camera both mounted at once).
  Separate plan: [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md) (TBD).
- **Cross-storage rename optimization.** MTP doesn't have a
  cross-storage move RPC; we'd need copy+delete (already what
  we do for cross-directory renames). No optimization possible
  without a protocol change.
- **Storage-specific encryption or access controls.** Not a thing
  in MTP, not a thing for our user.

## Receipts

Current single-storage limitation that motivates the work:

- [bridge/nfs/handler.go:38–43](../bridge/nfs/handler.go) — the
  aggregate `FSStat` that needs to become per-storage.
- [bridge/mtp/session.go:155–180](../bridge/mtp/session.go) —
  `FreeBytes()` / `TotalBytes()` sum across all storages.

Already-working multi-storage data layer:

- [bridge/mtp/session.go:296–323](../bridge/mtp/session.go) —
  storage enumeration and top-level directory registration.
- [bridge/mtp/session.go:340–377](../bridge/mtp/session.go) —
  `populateDir` threads `StorageID` through every child.
- [bridge/nfs/fs.go:235, 252, 266, 350, 359](../bridge/nfs/fs.go)
  — `StorageID` carried on every write.

Reference patterns (what we're borrowing):

- [docs/OPENMTP-NOTES.md](OPENMTP-NOTES.md) — full forensics.
  Multi-storage section verbatim from 2026-05-10 Explore pass:
  storageId threaded through every API call
  ([FileExplorerKalamDataSource.js:82–110](../../openmtp/app/data/file-explorer/data-sources/FileExplorerKalamDataSource.js));
  storage description used verbatim from phone-side PTP
  ([helpers.go:69–80](../../openmtp/ffi/kalam/native/helpers.go)).
- [docs/SWIFTMTP-NOTES.md](SWIFTMTP-NOTES.md) — full forensics.
  Multi-storage section verbatim from 2026-05-10 Explore pass:
  per-storage capacity inline
  ([SidebarView.swift:178–181](../../SwiftMTP/SwiftMTP/Views/SidebarView.swift)).

Vendored go-nfs:

- [bridge/vendor/github.com/willscott/go-nfs/nfs_onfsstat.go](../bridge/vendor/github.com/willscott/go-nfs/nfs_onfsstat.go)
  — the patch site for option 1.
- [docs/GO-NFS-NOTES.md](GO-NFS-NOTES.md) — context on vendoring
  + patch policy.
