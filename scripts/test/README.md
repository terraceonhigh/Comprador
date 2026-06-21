# scripts/test — cascade-investigation harness

A standardized test harness for reproducing the v0.3.3-cascade bug
across different builds (ad-hoc vs notarized vs historical commits).
Replaces the ad-hoc copy-paste commands that bit us with the trailing-`&`
issue and inconsistent log paths.

## Workflow

In **two tmux panes**:

**Pane A — observer pane** (foreground log capture, leave running):

```
./scripts/test/tail.sh <variant>
```

Where `<variant>` is one of the names in `dist-compare/` (e.g. `prod`,
`notarized`, `92d4e6d5`). The script writes to a deterministic path
(`/tmp/test-<variant>-<timestamp>.log`) and tees to your terminal so
you can see live output. Ctrl-C to stop after the test.

**Pane B — operator pane** (two chained commands per test):

```
sudo ./scripts/test/setup.sh <variant>
# (now: perform the drag-drop manually in Finder. Observe behavior.)
# (Ctrl-C the tail.sh pane in Pane A to stop log capture.)
sudo ./scripts/test/finish.sh <variant>   # log path auto-detected from /tmp
```

`setup.sh` runs `recover → clean → install → launch` in sequence and exits
when the mount is live. `finish.sh` runs `analyze → recover` in sequence,
auto-detecting the most recent log file for the variant if you don't pass
a path explicitly. Two commands per test, predictable order, all failure
modes named.

If you want to run the underlying steps one-at-a-time (e.g. for debugging
a script), they're still individually invokable: `recover.sh`, `clean.sh`,
`install.sh`, `launch.sh`, `analyze.sh`.

## What the scripts predetermine

- **Log paths**: `/tmp/test-<variant>-<HHMMSS>.log` — no need to
  remember the redirect target. The analyze script accepts the path
  as an argument so the operator doesn't have to remember which run
  is which.
- **Grep patterns**: `cache.beginPrefetch`, `JUKEBOX`, `Comprador
  bridge:` (the NSLog'd stderr from the Swift parent), `Adding
  notification request CE85` (the "Check your phone" file-transfer
  prompt that signals USB-claim race).
- **Verdict heuristic**: counts of each pattern decide which
  hypothesis fits the run.
- **No trailing `&`**: tail.sh uses foreground `tee`, which kept the
  log capture working in earlier sessions where backgrounded
  redirects silently dropped data.

## What's NOT automated

- The drag-drop itself — that's manual in Finder.
- Phone replug — when the bridge can't claim USB (ptpcamerad race),
  `analyze.sh` reports it but the operator has to physically replug.
- Spotlight cache state — we can't precisely control whether the
  per-volume index is cold or warm at test time.

## Known footguns these scripts protect against

- **`clean.sh` walking into an active or stale NFS mount.** The
  2026-05-18 morning test hit this exact failure mode: Python's
  `shutil.rmtree` descended into `~/Library/Application Support/
  Comprador/Volumes/XQ-BT52` (a still-mounted hard,nointr NFS share),
  wedged on a kernel uninterruptible syscall, and cascaded Finder +
  keyboard input. The same evening it fired a *second* time after the
  initial fix — the kernel had reaped the mount-table entry but a
  stale dirent for `XQ-BT52/` survived, and `stat()` on it during
  rmtree wedged just as hard. `clean.sh` now runs **two** safety
  checks before touching anything:
  1. Mount-table grep — refuses on any `.local:/` mount.
  2. `os.listdir(Volumes)` inside a fork-bounded 5 s timeout — refuses
     if any entry survives regardless of mount-table state, with
     timeout-then-refuse on the pathological readdir-wedge case.

  Both checks point at `sudo recover.sh` as the prerequisite.
  `setup.sh` always runs `recover.sh` before `clean.sh`, so the
  chained path is safe by construction. The safety logic has a
  standalone regression test at `probe_volumes_test.py` exercising
  the absent / empty / occupied cases against synthetic temp dirs.
- **Trailing `&` on log captures silently dropping data.** Bit us
  earlier. `tail.sh` is foreground-only with `tee`.
- **Inconsistent log paths across runs.** `tail.sh` uses a
  deterministic `/tmp/test-<variant>-<HHMMSS>.log` pattern, and
  `finish.sh` auto-detects the most recent one for the variant.

## Variants

See `dist-compare/` for available variants. Add a new one by building
its `.app` and dropping it into `dist-compare/` with the
`Comprador-<name>.app` naming convention.

| Variant | In-binary BuildID (authoritative) | Note |
|---|---|---|
| `prod` | `6941a487-dirty` | originally labeled HEAD `32ee45cd`; actual bundle was rebuilt from `6941a487` with cprLog-conversion changes in a dirty working tree. Confirmed via the bridge stderr `Comprador build:` line in the 2026-05-18 evening prod control run. |
| `dev` | (verify with `make app-swiftc` + check Info.plist) | `prod` with `SWIFT_DEBUG=1`. Build identity unverified — re-stamp before relying on it. |
| `notarized` | `aef6c89b` (per letter 15 — verify before relying) | Developer ID + stapled. |
| `92d4e6d5` | `92d4e6d5` (per letter 15 — verify before relying) | Historical commit, code-equivalent to retracted v0.3.3. |
| `step2` | `54c01f78` | Priority-queue refactor in [bridge/mtp/session.go](../../bridge/mtp/session.go) with no `PriorityLow` callers — behaviourally a no-op for the current execution path. Confirmed inert vs `prod` by the side-by-side prod control run on 2026-05-18 evening (see [docs/MISTAKES.md entry 4](../../docs/MISTAKES.md)). |
| `step3` | `74702901` | Chunked prefetch on `PriorityLow`. The actual cascade fix — yields the session goroutine between 16 MB libmtp chunks, so a high-priority NFS RPC arriving mid-prefetch waits at most one chunk's latency (~600 ms) rather than the full multi-minute libmtp transfer. **The discriminating test is the yield test, not the cascade test** — start a large-file prefetch (Attenborough), then drag a small file or browse a different directory; with `prod`/`step2` that operation waits ~4–6 min, with `step3` it should complete in seconds. |

**Trust the in-binary BuildID, not the table.** Variants get rebuilt
in place during iteration and the README drifts. Every variant log
starts with `Comprador build: <BuildID>` (the in-Info.plist
`CFBundleVersion` is the same string, verifiable via
`/usr/libexec/PlistBuddy -c "Print :CFBundleVersion"
dist-compare/Comprador-<variant>.app/Contents/Info.plist`). If a
test makes a claim about which commit a variant represents,
verify the stamp before publishing the claim.
