# Vendored: buildbarn/bb-remote-execution — the NFSv4 server

Galatea's NFSv4 server, lifted from
[`bb-remote-execution`](https://github.com/buildbarn/bb-remote-execution)'s
`pkg/filesystem/virtual/nfsv4` — the production NFSv4.0 + 4.1 COMPOUND engine and
open-state machine. This is the "lift, don't write" core of the project: the
112 KB + 95 KB of protocol logic is bb-rex's, carried as faithfully as the
de-coupling allows.

- **Source:** the `references/bb-remote-execution` clone,
  @ `ed02b7a0f9e276690552b5dd8032e8d442f0985a`.
- **License:** Apache-2.0 (the repo `LICENSE`; bb-rex is one module — the same
  license already reproduced under `internal/bb/LICENSE` covers it, but this is a
  distinct upstream repo: its `LICENSE`/`AUTHORS` are not separately copied here
  because the code is small and the license identical; note the provenance).
- **Package:** `nfsv4`, placed at `internal/` because the server is Galatea's
  engine behind the eventual `galatea.Mount`, not public API (the public surface
  is `pkg/virtual`).

## What is here

`nfs40_program.go`, `nfs41_program.go`, `opened_files_pool.go`,
`minor_version_fallback_program.go` — copied; and `system_authenticator.go` —
rewritten.

## Modifications, all deliberate and recorded

1. **Import paths rewritten** (mechanical, via a throwaway `go run` rewriter):
   - `bb-remote-execution/pkg/filesystem/virtual` → `…/galatea/pkg/virtual`
   - `bb-storage/pkg/{filesystem,filesystem/path,clock,random}` → `…/galatea/internal/bb/…`
   - `go-xdr/…` → `…/galatea/internal/xdr/…`
   With `pkg/virtual` aliased onto the same vendored leaf types (DEC-015), the
   server's `virtual.*` references resolve with no type conversion.

2. **`system_authenticator.go` rewritten** — bb-rex mapped AUTH_SYS onto a
   JMESPath-configured `auth.AuthenticationMetadata` (its sole use of
   `bb-storage/pkg/{auth,jmespath,eviction}`, the gateway to OTel/GCP/Prometheus).
   Replaced with a minimal localhost AUTH_SYS authenticator that parses the
   credential body and attaches uid/gid to the context. See DEC-011 / DEC-016.

3. **Prometheus stripped from `nfs40_program.go`** — the four open-owner
   `NewCounter` vars, their `sync.Once` registration, and four `.Inc()` call
   sites removed (observability-only; no behavioural change). The separate
   `metrics_program.go` decorator was **not lifted** (a Prometheus-wrapping
   `Program`; Galatea wants no NFS-op metrics on a single-user mount).

4. **Two `NfsV4Nfsproc4Null` value receivers → pointer receivers**
   (`nfs40Program`, `nfs41Program`) — `go vet` flagged them as copying the
   struct's `sync.Mutex` by value. Harmless (Null is a no-op) but vet-unclean.

5. **One upstream bug fixed** in `nfs41_program.go` — `append(slot.current`
   `SequenceWaiters)` with no value (a no-op) left a concurrent same-slot 4.1
   SEQUENCE waiter unregistered and deadlocked on `<-ch`. Fixed to
   `append(…, ch)`. `go vet` caught it; it is bb-rex's bug, not the lift's. See
   `docs/MISTAKES.md` M-005.

## Types added to `pkg/virtual` for this lift

The hand-cut `pkg/virtual` (DEC-005) had not reproduced everything the server
references. R2d added: `byte_range_lock_set.go` (`ByteRangeLock`,
`ByteRangeLockSet`, `ByteRangeLockType` + constants — lifted verbatim, import-free)
and `handle.go` (`HandleResolver`, a one-line func type). Also re-exported
`Format` / `UNIXFormat` and reverted `VirtualSymlink`'s `pointedTo` and
`Attributes.symlinkTarget` to `path.Parser` (the DEC-005 `string` simplification),
matching bb-rex so the lift needs no signature adapters.

## What was dropped (left in `references/`)

The entire CAS/handle-allocator/FUSE/WinFSP surface of bb-rex's `virtual` package
(see `docs/coupling-map.md`), `metrics_program.go`, and all `*_test.go` (the
gomock test mountain — a Galatea smoke COMPOUND test replaces it at R3, per
DEC-011).

## Verified

`go build/vet/test/fmt ./...` green, and `go list -deps ./internal/nfsv4 | grep
buildbarn` returns **nothing** — the server is fully de-coupled. Behavioural
verification (a COMPOUND round-trip) is R3.
