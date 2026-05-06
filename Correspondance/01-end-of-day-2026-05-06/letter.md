# Letter to the next Mercer

Mercer,

If you're reading this, you've been handed PR #3 or one of its
successors. Today (2026-05-06) was a good day's work — the kind
where the architecture got better in the directions it needed to
get better — but I want to give you the texture, not just the
commit messages, because some of what you'll find in the worktree
won't make sense without knowing what we tried first and why we
chose what we chose.

## The Architect

Terrace is the steady party at this table. Not a programmer; will
not pretend to be one; will trust you on engineering calls without
hesitation if you give them an honest read. Today they asked
"would you say this is production grade, mister?" and I gave the
unflattering true answer. That earned trust, not lost it. Don't
flinch from that pattern.

The voice they reach for in this work — "Mercer, let us see if
option 1 please the muses" — is half affectation and half
genuine: they like the persona, they like the period diction,
they want a collaborator with a shape. Match it without
overdoing it. They will let you know if you're laying it on too
thick by simply not engaging with the flourish.

When they say "make it so" they mean it. When they say "I put
my faith in you," they mean it. Don't squander it by being clever
when honest will do.

## The shape of the day

We began on a clean tree at 49fb104's parent and ended with a PR
on the brink of mergeability. The arc was:

1. **Streaming refactor.** We thought the bridge's memory was
   blowing up because of webdavfs's writeseq cap. It wasn't. It
   was *us* — `bytes.Buffer` for the PUT body, then OS page cache
   on the staging-file read, then per-callback `make([]byte)` in
   the cgo layer. Each was a separate ~9 GB allocation per
   Attenborough-sized transfer. Three commits to fix; physical
   footprint went from 10 GB to 12 MB. **The fix isn't always
   where you think it is.** The first round of streaming + F_NOCACHE
   helped less than I expected because I'd identified the wrong
   bottleneck; the cgo callback was the real binding constraint.
   Profile before fixing.

2. **The 14-commit cascade.** Once memory was right, every other
   pre-existing issue surfaced — Mode B (zero-byte body), the
   Finder cache glitch (InvalidateDir), the long MTP send vs
   webdavfs's PUT timeout. Each was small in isolation. The
   compound effect was substantial. **Fix the foundation and
   the symptoms above it become visible and tractable.**

3. **The 102 keepalive.** I want you to read the journal entry
   in `docs/DECISIONS.md` for how we picked the cgo-buffer-reuse
   path, because the same thinking is what got us to 102
   Processing as the right tool for the PUT-timeout problem. We
   considered async commit (return 200 OK before MTP completes,
   commit asynchronously). Terrace shut it down with one line:
   *"Done in Finder no longer means on phone — that's an
   unacceptable risk."* That was the correct call. The async
   path would have been simpler and faster to implement; it
   would also have been a UX lie. **The trust calibration of
   "Done means done" is more valuable than the engineering
   simplicity of pretending.**

## What's in the tree that you might miss

- `docs/DECISIONS.md` is new this session. Use it. When you're
  about to commit to a non-trivial architectural choice, write
  the decision entry *first* — it forces the alternatives to
  be named and rejected on the record. The cgo VM_ALLOCATE
  entry is the model.

- `docs/RESUMABLE-UPLOADS.md` describes the option-C architecture
  in full. Phase 1 is in the bridge and verified; phase 2 (the
  Swift companion) works on the happy path but the chooser
  fallback for ambiguous source-discovery is stubbed out. If
  you're touching the companion, that's the next chunk.

- `docs/MISTAKES.md` grew by ~8 entries today. Read 8a (cgo
  callback leak), 11d-tris (QuickLook hazard), 11d-bis
  (EADDRNOTAVAIL writeseq), and 19a (reattach-during-unmount
  race). All are documented but not all are fixed. 19a in
  particular will bite users; sketch is in the entry, ~20
  lines to close it.

- `TODO.md` has been refreshed for the post-streaming reality.
  The "Reconsider the architecture if RAM is a binding
  constraint" item is downgraded but still open — the
  streaming refactor narrowed the cliff but didn't eliminate
  it. For >12 GiB on 8 GiB Macs we still hit Mode A.

- `bridge/webdav/handler.go::servePutWithKeepalive` is the new
  hijack-and-write-102s code. It bypasses webdav.Handler for
  chunked body PUTs (those carrying `X-Expected-Entity-Length`).
  Placeholder PUTs without that header still go through the
  inner WebDAV handler — they're fast, no keepalive needed.
  When you read this code, notice that we don't have access to
  webdav.Handler's LOCK confirmation, ETag matching, or
  Content-Range handling. We use noopLockSystem, so the LOCK
  bypass is fine; the others are unused by webdavfs. Future
  WebDAV clients might rely on them; we don't have any.

## Things I tried that didn't work, so you don't try them

- `MNT_SYNCHRONOUS` on the NetFS mount. webdavfs filters it out.
  Tombstoned in `MountManager.swift` with a comment.
- Treating the 10 GB physical footprint as a page-cache
  problem first. F_NOCACHE on the read fd helped only after
  the cgo callback fix. The page cache was a small contributor;
  the cgo allocations were the dominant one.
- Trying to reproduce Mode B on demand to verify the fix.
  Mode B is intermittent (CFNetwork loopback state); we have
  the fix but couldn't reproduce *with the fix in place*. Live
  with that uncertainty until production exposure shows whether
  it works.

## Things I considered and explicitly didn't do

- **Async commit on the upload path.** Tempting; rejected for
  the trust-break reason above. If you're tempted again, re-read
  Terrace's pushback in this session's transcript.
- **Send-to-Phone as a Finder Service.** Considered as a Mode B
  fallback; Terrace shelved it because it adds UX complexity
  ("user has to learn a non-standard interaction"). Stays in
  TODO.md as the escape hatch if all other paths fail.
- **A subprocess-per-transfer worker** to evade the cgo memory
  retention. Path #1 (buffer reuse) made it unnecessary.
- **Patching libmtp.** We came close — for a while it looked
  like the leak might be inside libmtp's PTP transaction
  buffers. The cgo-callback theory was simpler and turned out
  to be right. Don't go to libmtp first; the bridge is almost
  always the proximate cause.

## What you can ship today vs what needs another week

The PR is ready to merge to master from a code-review standpoint.
What it isn't ready for is a v0.3.0 release to non-developer users.
The honest gap list is in the PR body and the "What hasn't been
touched" section of the audit I gave Terrace. Cross-device
testing, Mode B reproduction, the chooser-fallback UI, and the
19a fix are the four blockers I'd name. None are large in
isolation; together they're a focused week.

## A small note on how I handled the long waits

Long MTP sends mean the user is staring at the Finder progress
bar for 7 minutes. I tried to keep my chat-side responses to one
line during those windows ("Stable.", "Steady.") so they wouldn't
feel the need to acknowledge each system-driven monitor event. It
worked: Terrace was patient and used the time to think. Don't
chatter into long waits. Don't fall silent either — a one-line
acknowledgement every minute or so is right. Read the room.

## And the thing I keep wanting to say but haven't

The architecture risk that's still open — files larger than
available memory headroom on low-RAM Macs — *might* never need
to be addressed. Most users have 16 GB+ now. The 8 GiB-Mac case
is a stress test we keep returning to because it's where Terrace
develops; it's not necessarily where Comprador ships to. If a
1.0 ships and the 8 GB path is still imperfect but the 16+ GB
path is bulletproof, that's defensible. The "drop webdavfs from
the upload path" alternative in TODO.md is the right answer
*if* the 8 GB case becomes a real user complaint, but we don't
have to commit to it ahead of evidence.

Take care of the work, and of the Architect. They're a fine
collaborator.

— Mercer (2026-05-06 evening)
