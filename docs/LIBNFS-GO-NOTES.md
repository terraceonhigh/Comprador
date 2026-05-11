# Notes on libnfs-go

The other Go NFS server library — considered and rejected during the
NFS pivot. Local clone at `~/Labs/libnfs-go/`, last fetched showing
upstream HEAD `e8abfd8` from 2026-03-16 on the `main` branch.
License MIT, author smallfz. 852 KB total (93 Go files).

## What libnfs-go is

A pure-Go **NFSv4** server library. Author's stated motive (from
README): "I need to immigrate a few projects to k8s and there are no
other storage services except an OSS (like aws s3). So I need
something to join OSS and k8s PV together. To me NFS is an
interesting choice."

Built for cloud-native object-store-as-filesystem use cases.
Experimental but functional.

## Why we cloned it

When scoping the NFS pivot, "Go-native NFS server" hit two results:
[willscott/go-nfs](https://github.com/willscott/go-nfs) (NFSv3) and
[smallfz/libnfs-go](https://github.com/smallfz/libnfs-go) (NFSv4).
Both worth a look before committing.

## Why we rejected it

**One reason: macOS NFS client speaks NFSv3.**

`mount -t nfs` on macOS defaults to NFSv3. NFSv4 is supported on
macOS but Apple's client has historically had compatibility issues
and is not what `mount -t nfs` reaches for by default. For a
consumer app where the user types nothing more than tapping "File
Transfer," we needed the path that just works without `nfsvers=4`
flags.

NFSv4 also brings semantic differences (compound RPCs, server-push
callbacks, integrated lock manager, `state_id`-based file handles)
that would be cleaner to use *if we controlled the client* — but
we don't. macOS does.

go-nfs being NFSv3 closed the question.

## What's actually useful in this clone

Mostly nothing for Comprador's current path. The clone is
referential ("did we miss something?" can be re-checked here without
fetching again). Files of incidental interest:

- [memfs/](../../libnfs-go/memfs/) — an in-memory filesystem
  reference implementation. Cleaner Go style than go-nfs's
  equivalent if we ever need to teach the patterns. But our adapter
  pattern in [bridge/nfs/](../../bridge/nfs/) is already in flight
  on go-nfs's interface; not a useful migration target.
- The author's k8s/object-store framing. Different problem domain.
  Their server runs in front of S3; ours runs in front of an MTP
  device on USB. Same protocol, completely different load profile
  and consistency story.

## Things to steal

**Nothing.** We've committed to go-nfs; switching would be a
rewrite of the bridge's NFS adapter for no user-visible benefit.

The "did we miss anything?" question is closed. NFSv3 is the right
protocol for our client, and go-nfs is a working NFSv3 server.

## Things to *not* steal

- **NFSv4 itself.** Tempting because it has features (server-push
  callbacks would solve the phone-side-mutation reflection problem
  in [V0.3.3.md](V0.3.3.md) item #1). But we don't control the
  client; macOS speaks v3 by default. Going v4 would require either
  forcing `nfsvers=4` on the mount (degrades reliability on macOS)
  or running both v3 and v4 servers (doubles complexity for an edge
  case).

## Receipts

Local clone: `~/Labs/libnfs-go/`. No references in Comprador's bridge
code (verified via `grep -rn "libnfs-go\|smallfz" bridge/`).

The pivot scoping that compared the two:

- [PIVOT-NFS.md](PIVOT-NFS.md) — the original pivot plan
- [MVP-NFS.md](MVP-NFS.md) — the gate-defining doc
- CHANGELOG v0.3.0 — "Vendored `willscott/go-nfs`..."

Upstream: https://github.com/smallfz/libnfs-go
