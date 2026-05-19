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

**Pane B — operator pane** (one command at a time):

```
sudo ./scripts/test/recover.sh         # kill everything, force unmount
./scripts/test/clean.sh                # wipe user-side caches/prefs
./scripts/test/install.sh <variant>    # cp -R from dist-compare/ to /Applications
./scripts/test/launch.sh               # open the app, poll for mount
# (now: manually drag-drop in Finder. Capture the result behavior.)
./scripts/test/analyze.sh <variant> <log-path>   # grep, verdict
```

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

## Variants

See `dist-compare/` for available variants. Add a new one by building
its `.app` and dropping it into `dist-compare/` with the
`Comprador-<name>.app` naming convention.

| Variant (as of 2026-05-18) | Source | Signing | Hardened runtime |
|---|---|---|---|
| `prod` | HEAD `32ee45cd` | ad-hoc | ✓ (via app-swiftc) |
| `dev` | HEAD `32ee45cd` + SWIFT_DEBUG=1 | ad-hoc | ✓ |
| `notarized` | HEAD `aef6c89b` | Developer ID + stapled | ✓ |
| `92d4e6d5` | historical commit | ad-hoc | ✓ |
