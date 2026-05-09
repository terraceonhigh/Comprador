# MVP for the NFS migration

**Status — 2026-05-08:** scope and acceptance criteria for shipping
Comprador's NFS path as the default mount surface, replacing the
WebDAV path that ships in v0.2.3. Authored by Mercer after the
verification session that uncovered the silent-write-loss bug in the
Phase 2b code on master and fixed it via a go-nfs response-stability
patch plus a bridge-side idle-flush timer.

## Goal

A non-technical user plugs in their Sony Xperia (or any vendor in
`VendorIDs.plist`), taps **File Transfer** on the phone's USB
notification, and within a handful of seconds sees the phone in
Finder. They drag files between Mac and phone; the bytes land. They
eject from the menu bar; the volume disappears cleanly. No 90-second
hang at mount time. No `defaults write`, no Terminal, no `sudo`.

## In scope (must work for MVP)

These are the operations the menu-bar app must support end-to-end via
the Finder UI, on a real device, before this is shipped as a `.dmg`:

1. **Mount via the menu-bar app.** Plug → tap File Transfer → mount
   appears in Finder Locations within ≤5 s of the USB notification
   tap. The Phase 3 helper-driven `mount_nfs` path is the production
   path; `--nfs` direct dev path is for engineers only.
2. **Browse.** Storage roots and at least three levels of nested
   directories enumerate without errors. `.trashed-` prefixed entries
   visible (matches WebDAV behaviour).
3. **Read.** Drag a small file (≤1 MB) and a large file (≥256 MB) from
   phone → Mac in Finder. Both round-trip MD5-identical.
4. **Write — atomic copy.** Drag a small and a large file from Mac →
   phone in Finder. Bytes land at the user-visible final name (not
   the `.tmpXXXX` Finder uses internally). Round-trip MD5-identical.
   This requires `fs.Rename` (currently `ErrNotSupported`).
5. **Delete.** Move a file to Trash via Finder, or `rm` via the mount.
   File disappears from the phone.
6. **Eject.** Click "Eject" in the menu-bar menu. Volume unmounts,
   bridge process stops, USB session releases. Replug works.
7. **Unplug recovery.** Yank the cable mid-session. Menu bar reflects
   detach within ≤2 s. Mount cleared. No zombie processes.

## Out of scope for MVP (deferred without apology)

These are real gaps but they don't block first ship of the NFS path.

- **`.AppleDouble` / `._xattr` companion files committed to the phone.**
  macOS Finder writes 4 KB sidecars; they currently land on the device.
  Ugly but functional. Filter or absorb in v0.3.1.
- **Stale `df` free-bytes.** `FSStat` returns startup numbers. Cosmetic;
  Finder's "X GB available" string will be wrong after writes.
- **Ownership / mtime cosmetics.** Files show as `root:wheel`,
  modification times don't reflect the phone's view. Cosmetic.
- **Multi-storage devices** (phones with SD cards). Single-storage is
  the dominant case; SD-card support is its own design problem.
- **Concurrent multi-device.** One phone at a time.
- **Quick Look thumbnails** — known regression hazard, do not invest.
- **WebDAV fallback path.** The WebDAV code stays in tree as
  `bridge/webdav/` so we can pivot back if a critical NFS bug surfaces
  post-ship, but the menu-bar app spawns the bridge with `--nfs` only.
  WebDAV path is engineering-accessible, not user-accessible.

## Architectural acceptance criteria

These are properties of the implementation that must hold at ship:

- **Mount time ≤ 5 s wall clock.** Verified empirically against
  WebDAV's 90 s. Today's measurement: 0.01 s for `mount_nfs` itself;
  add ≤ 4 s of helper RPC + bridge boot + USB enumeration. Total
  budget 5 s end-to-end is generous.
- **No silent write loss.** Every successful Finder write must reach
  the device. Specifically: the staging design must commit on idle
  even when the NFS client never sends COMMIT (verified true of macOS
  15.4+). The 2-second idle-flush timer is the contract here.
- **No half-written files visible.** Finder's atomic-copy pattern
  (write temp, rename to final) must end with the user-visible final
  filename and no leftover `.tmpXXXX` on the phone. This requires
  `fs.Rename` to either:
  - Implement copy + delete (slow but correct), OR
  - Defer the staging-to-MTP commit until after a rename, then commit
    under the new name (fast, requires care with the staging registry).
  MVP picks copy+delete; the optimised form is post-ship work.
- **go-nfs patch is durable.** The fix to
  `nfs_onwrite.go` (respond `unstable` not `fileSync`) cannot live
  only in `~/Labs/go-nfs` on `gala`. It must be either:
  - A fork at `terraceonhigh/go-nfs` pinned in `go.mod` via a
    commit-hash require, with `replace` removed, OR
  - Upstreamed and depending on a tagged release.
  MVP picks the fork; upstream is post-ship.
- **Build identity stamped.** The shipped binary must report its
  build SHA via `BuildID`. Already present (`60075e64-dirty` in this
  session — the dirty bit must be clean before shipping).

## Verification plan

The acceptance test that gates the release. Running it requires the
phone in File Transfer mode and a clean Finder session.

1. **Setup.** Quit any running Comprador. Unplug phone if connected.
   Run the new build from `Applications/Comprador.app`.
2. **Mount.** Plug phone, tap File Transfer. Stopwatch starts when
   Finder shows the volume. Target: ≤ 5 s.
3. **Browse.** Open the volume in Finder. Click into `Internal shared
   storage / DCIM / Camera`. Open three nested directories.
4. **Read small.** Drag any small file from phone → Desktop. Run
   `md5` against a known reference (or against the same file pulled
   later, comparing both reads).
5. **Read large.** Drag a ≥ 256 MB file (use the test file from
   today's session, or a movie) from phone → Desktop. Time it; should
   complete at ~20 MB/s = ~13 s for 256 MB. MD5 round-trip identical.
6. **Write small.** Drag a known small file Mac → phone. Wait 5 s.
   Drag it back. MD5 identical to source. Verify final filename has
   no `.tmp` suffix.
7. **Write large.** Drag a ≥ 256 MB file Mac → phone. Wait until
   Finder reports done (~13 s upload + 2 s idle-flush + 12 s MTP
   send = ~27 s wall clock). Drag back, MD5 identical.
8. **Delete.** Move a file to Trash via Finder right-click. Verify
   gone from the phone (re-list parent directory; should not appear).
9. **Eject.** Click menu-bar Eject. Volume disappears. Run `mount |
   grep nfs` — should be empty. Bridge process exits.
10. **Unplug recovery.** Mount again. Yank cable. Within 2 s, menu bar
    shows "no device". Replug, tap File Transfer; mount re-establishes.

A pass on all ten gates this is shippable.

## Implementation gaps to close before verification

In execution order:

1. **`fs.Rename`** ([bridge/nfs/fs.go:156](../bridge/nfs/fs.go:156)) —
   currently `ErrNotSupported`. Implement as copy + delete: read source
   from MTP (or staging), `OpSendFile` under destination name, `OpDelete`
   the source object, update `ObjectMap` for both ends. If source is in
   staging (Finder rename of a just-written temp), no MTP read is needed
   — re-key the staging entry and let the idle timer commit it under
   the new name.
2. **Idle-flush timer** ([bridge/nfs/write.go](../bridge/nfs/write.go)) —
   already implemented in this worktree, uncommitted. Commit it.
3. **go-nfs response-stability patch** — already implemented in
   `~/Labs/go-nfs`, uncommitted in that clone. Push to a fork at
   `terraceonhigh/go-nfs`, update `bridge/go.mod` to require that fork
   at a specific commit, drop the absolute-path `replace` directive.
4. **Menu-bar app spawns bridge with `--nfs`.** Currently the Swift
   `BridgeProcess.swift` may not pass the flag. Verify and fix.
5. **App build pipeline** — `make app` or `make dist` produces a
   bundled `.app` with the bridge + helper. Already in master; verify
   the new bridge binary is included.

## Distribution

- **Version bump:** `v0.3.0` (new minor — visible mount-protocol
  change).
- **CHANGELOG entry:** lead with "Replaced WebDAV with NFSv3 for
  mount; eliminated 90-second mount-time wait on macOS 15.4+."
  List the staging-flush fix as a sub-bullet so a reader can grep
  for it later if symptoms recur.
- **Tag and push.** Memory: as of 2026-05-05 releases are
  `git tag vX.Y.Z && git push --tags`-only; the GitHub Actions
  workflow does the build, signs, notarises, attaches the `.dmg`.
- **Release notes** highlight the mount-time win (the user-perceptible
  change) and the protocol switch (the technical change). Keep it to
  ~5 lines. The MISTAKES.md / PIVOT-NFS.md trail is for engineers,
  not release readers.

## Post-MVP backlog

Tracked here so they don't get lost, not implemented in this cut:

- Filter `._xattr` / `.AppleDouble` companions before MTP commit
- Refresh `FSStat` free bytes after each commit
- Map MTP mtime → NFS ModTime properly
- Fast-path rename (no copy when source is in staging)
- SD-card / multi-storage device support
- Upstream the go-nfs `unstable` patch to willscott/go-nfs
- Quick Look hazard mitigation (carryover from WebDAV findings)
