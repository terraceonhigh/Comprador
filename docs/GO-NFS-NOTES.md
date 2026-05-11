# Notes on go-nfs

The NFSv3 server library we vendored for Comprador's NFS pivot.
Local clone at `~/Labs/go-nfs/`, last fetched showing upstream HEAD
`209a01f` from 2026-04-13 (master, "Merge pull request #149 from
ncw/fix-eof"). License Apache 2.0, author Will Scott. 716 KB total
(49 Go files).

## What go-nfs is

A pure-Go NFSv3 server implementation. You provide a `billy.Filesystem`
(from `go-billy/v5`); go-nfs handles the wire protocol, RPC mux,
mount handshakes, READDIR/READ/WRITE/COMMIT/CREATE/RENAME/REMOVE/etc.
The user-facing API is small:

```go
listener, _ := net.Listen("tcp", ":2049")
nfs.Serve(listener, nfshelper.NewNullAuthHandler(billyFS))
```

It is what Comprador's bridge-side NFS server is built on top of.

## Why we have it

We pivoted from WebDAV to NFSv3 in Comprador v0.3.0 to escape the
~90-second WebDAV mount-time delay on macOS 15.4+
([PIVOT-NFS.md](PIVOT-NFS.md), [letter 06](../correspondence/06-after-the-quota-impasse/letter.md)).
That pivot needed a Go-native NFS server library. go-nfs was the
viable choice; libnfs-go (the obvious-sounding alternative) is
NFSv4-only and macOS speaks NFSv3 (see
[LIBNFS-GO-NOTES.md](LIBNFS-GO-NOTES.md)).

## What we vendored

`bridge/vendor/github.com/willscott/go-nfs/` is a snapshot of the
upstream codebase minus tests and the `example/` directory. Vendored
because:

1. We applied a behavioral fix (see below) and wanted the patch to
   travel with the repo.
2. `go mod` vendoring guards against any unexpected upstream change
   between releases.
3. The license is Apache 2.0; bundling is allowed with attribution
   preserved (the vendored copy keeps `LICENSE` in tree).

## The patch we made

CHANGELOG v0.3.0 records: "Vendored `willscott/go-nfs` with one patch
(`nfs_onwrite.go` responds `unstable` instead of `fileSync`) so
macOS NFSv3 clients know they must follow up writes with a COMMIT
RPC."

Status check 2026-05-09: the vendored copy and the upstream master
both currently emit `unstable` for staged writes
([nfs_onwrite.go:106](../../bridge/vendor/github.com/willscott/go-nfs/nfs_onwrite.go)).
Either upstream absorbed an equivalent fix, or our patch was
authored against an older revision and current master coincidentally
matches. Either way: **the behavior we need is in the vendored
code today, and matches upstream.**

The lesson the patch encoded:

- **macOS NFSv3 client never spontaneously sends COMMIT** if the
  server reports `fileSync` — the client assumes the server already
  durably persisted the write.
- **Comprador's bridge stages writes to disk before flushing to MTP**,
  so it must report `unstable` and *expect* a follow-up COMMIT to
  trigger the actual MTP send.
- **macOS still doesn't reliably send COMMIT promptly** — it batches.
  We added a 2-second idle-flush timer in the staging registry
  ([bridge/nfs/](../../bridge/nfs/), see CHANGELOG v0.3.0) so the
  bridge force-commits when the client goes quiet.

This is the kind of NFSv3 protocol-shape detail that is one paragraph
in RFC 1813 but takes two days to discover empirically. Worth
preserving.

## Other files we depend on

The bridge-side NFS adapter at [bridge/nfs/](../../bridge/nfs/) wraps
go-nfs's `Handler` interface. Files we reach into:

- `nfs.go` — the entry point: `nfs.Serve(listener, handler)`.
- `handler.go` — the `Handler` interface we implement on the MTP
  side. Read this when adding a new NFS RPC method.
- `nfs_onwrite.go` — the source of the `unstable` write-stability
  semantics. Our patch lives here in spirit; the behavior is now
  upstream-equivalent.
- `nfs_oncreate.go` — exclusive-create mode is currently
  `NFSStatusNotSupp` upstream
  ([nfs_oncreate.go:43](../../bridge/vendor/github.com/willscott/go-nfs/nfs_oncreate.go),
  also recorded in MISTAKES.md "NFS pivot (go-nfs)" §1). We map
  exclusive→guarded in our adapter, which is acceptable on a
  single-client USB device.

## Things to steal — already stolen

We are already using go-nfs's full surface for what we need. The
"things to steal" question is closed for this repo.

## Things to consider for future work

- **Upstreaming the `unstable`-write semantics fix** if our patch
  proves to be a delta vs. upstream master. Status check above
  suggests it is no longer a delta — but if it is, a small PR with
  the Comprador-discovered macOS behavior as the motivation would
  be neighborly.
- **Upstreaming exclusive-create support.** [nfs_oncreate.go:43](../../bridge/vendor/github.com/willscott/go-nfs/nfs_oncreate.go)
  has a TODO comment from upstream itself. We map exclusive→guarded
  in our adapter; the upstream-correct fix is to implement
  exclusive properly. Not on our roadmap; on Comprador's "shelved"
  list at best.
- **Adding a `Commit()` hook to the `Handler` interface.** Currently
  the COMMIT RPC is handled inside go-nfs without surfacing to the
  handler. We work around this with our staging-registry idle-flush
  timer; a cleaner integration would let the Handler hear COMMIT
  events directly. Discussed in [MVP-NFS.md](MVP-NFS.md). Probably
  a willscott-side decision to make.

## Things to *not* steal

There's nothing in go-nfs's design we'd reject; it's a small,
focused library and we've adopted its shape directly. The only
thing to be cautious about:

- **Don't expose go-nfs to untrusted networks.** It assumes a
  trusted client. Auth is "null" by default (no auth) or AUTH_UNIX
  (which is trivially spoofable). Comprador binds to 127.0.0.1
  only; do not bind to 0.0.0.0.

## Receipts

Vendored copy:

- [bridge/vendor/github.com/willscott/go-nfs/](../../bridge/vendor/github.com/willscott/go-nfs/)
- [nfs_onwrite.go:106](../../bridge/vendor/github.com/willscott/go-nfs/nfs_onwrite.go) — write-stability response (the patched/equivalent line)
- [nfs_oncreate.go:43](../../bridge/vendor/github.com/willscott/go-nfs/nfs_oncreate.go) — exclusive-create TODO

Upstream:

- https://github.com/willscott/go-nfs
- License: Apache 2.0

In Comprador:

- [bridge/nfs/](../../bridge/nfs/) — our adapter
- [CHANGELOG.md v0.3.0](../CHANGELOG.md) — "Vendored willscott/go-nfs with one patch"
- [MISTAKES.md "NFS pivot (go-nfs)"](MISTAKES.md) — exclusive-create caveat, root acknowledgement requirement
- [MVP-NFS.md](MVP-NFS.md) — the pivot plan, references go-nfs throughout
- [PIVOT-NFS.md](PIVOT-NFS.md) — the original pivot scoping doc
