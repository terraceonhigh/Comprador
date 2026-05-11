# Notes on copyparty

The Python multi-protocol file server we read during the WebDAV
quota investigation arc on 2026-05-07. Local clone at
`~/Labs/copyparty/`, last fetched showing upstream HEAD `139ef185`
from 2026-05-08 on the `hovudstraum` branch. License MIT, author
ed (oss@ocv.me). 17 MB total (155 Python files, 47 JS).

## What copyparty is

A single-binary Python file server that speaks HTTP, WebDAV, FTP,
SFTP, TFTP, SMB/CIFS, all from one process. Resumable uploads.
Browser UI. Built for "turn almost any device into a file server."

It's not architecturally similar to Comprador. We didn't clone it
for design inspiration; we cloned it for *one specific commit*.

## Why we have it

Letter [06](../correspondence/06-after-the-quota-impasse/letter.md)
records the moment: during the WebDAV mount-time investigation,
copyparty's [issue #1242](https://github.com/9001/copyparty/issues/1242)
came up as evidence that returning empty quota responses to macOS
Finder might bypass the 90-second WebDAV mount-time wait.

Specifically: copyparty's commit `8e046fb` claimed to fix a similar
"slow mount on macOS" symptom by suppressing quota properties when
the requesting client looks like Mac Finder. We needed to read the
diff to know whether that fix would apply to our bridge.

It nearly broke us. Two agents (Codex and Opus 4.7) read the same
diff and gave contradictory readings. The lesson recorded in
memory [feedback_verify_agent_claims.md](file:///Users/terrace/.claude/projects/-Users-terrace-Labs-Comprador/memory/feedback_verify_agent_claims.md):
**read the source yourself before implementing on an agent's read of it.**

## What we found in the source

[copyparty/httpcli.py:1885–1886](../../copyparty/copyparty/httpcli.py)
is the load-bearing line:

```python
and "quota-available-bytes" in props
and "quotaused" not in props  # macos finder; ingnore it
```

The discriminator is **"is the request asking for `quotaused` (the
old, deprecated form) or only `quota-available-bytes` (the modern
form)?"** Mac Finder asks for the modern form *only*; that's how
copyparty tells "this is Finder" apart from "this is a non-Finder
client."

When the test passes (Finder asking for quota), copyparty *still
emits real quota numbers* ([httpcli.py:1914–1915](../../copyparty/copyparty/httpcli.py)):

```python
"quota-available-bytes": str(bfree),
"quota-used-bytes": str(btot - bfree),
```

The fix copyparty actually shipped was **not** "suppress quota for
Finder." It was "remove an old compatibility branch that was
returning malformed responses for the deprecated property form."
The 8e046fb diff is a *deletion* of dead code, not a Finder-special-case.

This is what Opus inverted. Opus claimed the fix was "switch from
modern `quota-available-bytes` to deprecated `quota`/`quotaused`."
It was the opposite — copyparty kept the modern path and removed
the deprecated path. Reading the diff straight took 30 seconds;
acting on Opus's misread cost 45 minutes.

## What we learned (not stole — learned)

### 1. The Mac Finder discriminator pattern

`"quotaused" not in props` is a clean, server-side way to tell Mac
Finder apart from other WebDAV clients without parsing User-Agent.
We didn't end up needing it (Comprador serves real quota to
everyone, see [MISTAKES.md 11c](MISTAKES.md)) but it's good
prior-art for "tell macOS Finder apart from anything else without
brittle UA matching."

### 2. The 90-second wait does not live where we thought

The most important thing copyparty taught us *by being a separate
WebDAV server*: "if quota suppression worked there, why doesn't it
work for us?" The answer turned out to be that the 90-second wait
is below NetFS, not in the PROPFIND layer at all (see
[CRYPTOMATOR-NOTES.md](CRYPTOMATOR-NOTES.md) for the falsifying
test). copyparty being a working server with a different
quota-handling strategy let us isolate the variable.

### 3. Read the source, not the agent's summary

Memory entry already cited. The pattern repeats; this is a known
failure mode now.

## Things to steal

**Nothing directly.** copyparty's WebDAV server is implemented in
Python with very different shape constraints from
`golang.org/x/net/webdav`. Code-level porting is not on the table.

The transferable artifacts are conceptual:

- **The "is this Finder?" discriminator** if we ever need to special-case Finder behavior server-side.
- **The "remove deprecated compatibility branches" instinct** rather
  than adding more compatibility shims — copyparty's fix was a
  simplification, not an addition. Worth remembering when we are
  tempted to add a "second code path for the case where..."

## Things to *not* steal

- **The multi-protocol design.** copyparty serves WebDAV, SMB, FTP,
  SFTP, TFTP, HTTP from one process. Their value prop is "one
  binary speaks every file-transfer protocol." Comprador's value
  prop is the opposite — "one purpose, mounts your phone." Don't
  let the comparison drift toward feature breadth.
- **The 195 KB README.** copyparty's README is genuinely 195
  kilobytes of content. They have an audience that reads README's
  for fun. Comprador's audience does not (see
  [USER.md](USER.md)).

## Receipts

- The 8e046fb diff narrative:
  [letter 06](../correspondence/06-after-the-quota-impasse/letter.md)
  paragraph 5
- The lesson:
  [feedback_verify_agent_claims.md](file:///Users/terrace/.claude/projects/-Users-terrace-Labs-Comprador/memory/feedback_verify_agent_claims.md)
- The actual quota-handling code:
  [copyparty/copyparty/httpcli.py:1885–1915](../../copyparty/copyparty/httpcli.py)
- copyparty issue #1242 (the thread that started this):
  https://github.com/9001/copyparty/issues/1242
- Comprador's parallel quota work:
  [MISTAKES.md 11c](MISTAKES.md) — `webdav.DeadPropsHolder` +
  real bytes from libmtp
