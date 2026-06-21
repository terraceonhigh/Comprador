# Vendored: buildbarn/bb-storage

The bb-storage utility packages the NFSv4 server depends on, copied into
Galatea's tree (copy + import-rewrite, the same mechanism as `internal/xdr` —
DEC-010) so the module resolves with **no external dependency**. This realizes
the "vendor" arm of the type fork (DEC-011): `pkg/virtual`'s leaf types re-point
to these vendored types, and the lifted server (R2d) imports them unchanged.

- **Source:** the `references/bb-storage` clone (see `../../references/README.md`),
  bb-storage @ `37f0e8bf7f18221f9ecbc074efbb2866c59d87c7`.
- **License:** Apache-2.0. The upstream `LICENSE` is reproduced here as `LICENSE`.

## What is here

| Package | Path | What it provides |
|---|---|---|
| `path` | `filesystem/path/` | the pathname parser/scope-walker stack — `Component`, `Parser`, `Format`, `UNIXFormat`, `EmptyBuilder`, `Resolve`, the scope walkers — used by `pkg/virtual` (symlink targets) and the server (`pathParserToLinktext4`). |
| `filesystem` | `filesystem/` | the leaf types the FSAL contract references: `FileType` (+ constants), `FileInfo`/`NewFileInfo`, `RegionType`, `DeviceNumber`, and `DeterministicFileModificationTimestamp`. |
| `clock` | `clock/` | the `Clock` interface + `SystemClock`, used by the NFSv4 server for lease/timestamp handling. Stdlib-only; copied verbatim (no rewrite, no strip). |
| `random` | `random/` | the `SingleThreadedGenerator`/`ThreadSafeGenerator` family, used by the server to mint state IDs / verifiers. Stdlib-only (`crypto/rand`, `math/rand`); copied verbatim. |

## Modifications, all deliberate and recorded

1. **Import paths rewritten** `github.com/buildbarn/bb-storage/pkg/` →
   `github.com/terraceonhigh/galatea/internal/bb/` (only `filesystem` →
   `filesystem/path` is an internal cross-reference; nothing else external).

2. **gRPC-status error wrappers stripped to stdlib** in `path` (the package's
   *only* external coupling — `bb-storage/pkg/util` + `google.golang.org/grpc/{codes,status}`,
   all in error-return positions). The mechanical rewrite, per DEC-011 / STATUS's
   R2b recipe:
   - `status.Error(codes.X, "m")` → `errors.New("m")`
   - `status.Errorf(codes.X, "f", a…)` → `fmt.Errorf("f", a…)`
   - `util.StatusWrap(err, "m")` → `fmt.Errorf("m: %w", err)`
   - `util.StatusWrapf(err, "f", a…)` → `fmt.Errorf("f: %w", a…, err)`

   Touched: `absolute_scope_walker`, `relative_scope_walker`,
   `loop_detecting_scope_walker`, `component`, `unix_format`, `builder`, `trace`,
   `virtual_root_scope_walker_factory`, `windows_format`. **Consequence:** path
   errors lose their gRPC status code and become plain stdlib errors. Galatea has
   no gRPC layer to inspect them, so nothing downstream depended on the code; the
   server maps path failures to a generic NFSv4 status regardless.

3. **`device_number.go` reimplemented dependency-free.** bb-storage's
   `device_number_unix.go` used `golang.org/x/sys/unix` for dev_t packing
   (`Mkdev`/`Major`/`Minor`). That would be Galatea's only non-stdlib runtime
   dependency. Replaced with the macOS/BSD dev_t encoding inlined in stdlib
   (Galatea is macOS-only). The exact raw encoding is immaterial in practice —
   NFSv4 carries device numbers as a major/minor `specdata4` pair, never the raw
   dev_t, and Galatea's backends serve no device nodes — but the encoding is
   macOS-correct. The original `device_number_unix.go` / `device_number_nonunix.go`
   (the windows stub) are dropped in favour of one unconstrained `device_number.go`.

## What was dropped

- All `*_test.go` (they pull `bb-storage/internal/mock` + `testutil`, dev deps).
- `path/local_format_windows.go` (`//go:build windows`).
- The entire `filesystem` local-disk backend: `local_directory_*.go` (darwin/
  unix/freebsd/linux/windows). These are bb-storage's real-disk implementation —
  not needed; Galatea's data source is a pluggable FSAL, not the host disk. They
  also carried the same grpc-status tail and, on darwin, `unix.O_SEARCH`.

## Verified

`go build ./... && go vet ./... && go test ./...` green; `go fmt ./...` clean. A
Galatea smoke test (`filesystem/path/smoke_test.go`) resolves a happy path and
confirms the two stripped error paths still return non-nil errors.

Vendored at R2b. Parser/type correctness is upstream's; the smoke test guards the
strip.
