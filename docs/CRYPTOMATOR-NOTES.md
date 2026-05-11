# Notes on Cryptomator

The Java vault-encryption tool we read during the WebDAV mount-time
investigation arc on 2026-05-07. Local clone at
`~/Labs/cryptomator/`, last fetched showing upstream HEAD `472b10a8a`
from 2026-05-04 on the `develop` branch. License GPLv3. 50 MB total
(413 Java files).

## What Cryptomator is

A Mac/Windows/Linux application that encrypts files into a vault
directory, then exposes the unlocked contents as a mounted volume so
the user can interact with their plaintext via Finder/Explorer. The
encryption is the product; the **mount layer is an implementation
detail** they had to solve along the way — and they hit the same
problems Comprador hit.

Their relevance: of all the apps shipping a "user-mountable Finder
volume backed by a userspace process" on Mac, Cryptomator was the
one that picked the same battle and could be read for prior art.

## Why we have it

Letter [06](../correspondence/06-after-the-quota-impasse/letter.md)
records the moment: after copyparty's quota fix didn't transplant
to our bridge, the architect suggested cloning Cryptomator to see
how *they* handled WebDAV-on-Mac. We needed a falsifying test for
"is the 90-second wait in the WebDAV server, or below NetFS?"

What we did: cloned, found their AppleScript-based mount path,
**ran an AppleScript mount against our running bridge**, and timed
it. **93 seconds**, indistinguishable from `NetFSMountURLSync`.
Both paths hit the same chokepoint. The 90 seconds lives below NetFS.

Both quota-suppression and AppleScript mount were thereby
falsified as escape routes from the WebDAV-on-Mac wait. The
remaining option was to leave WebDAV entirely — which became the
NFS pivot.

## What's actually in this clone

The current Cryptomator codebase has factored mount providers into
**separate plugin jars** loaded via `org.cryptomator.frontend.*`
packages (Maven dependencies). The clone here is the orchestrator,
not the implementations:

- [src/main/java/org/cryptomator/common/mount/Mounter.java](../../references/cryptomator/src/main/java/org/cryptomator/common/mount/Mounter.java)
  — picks a provider per OS. Notable: defaults to FUSE-T on Mac,
  not WebDAV. WebDAV is a fallback.
- The AppleScript mounter and the WebDAV server (`MacAppleScriptMounter.java`,
  `DavFolder.java`, `OSUtil.java` referenced in letter 06) live in
  separate Cryptomator subprojects (`webdav-nio-adapter`,
  `cryptofs`, etc.) — not in this clone. Those were either an
  earlier monolithic version or a sibling repo that was online at
  the time of the investigation.

## What we learned (not stole — learned)

### 1. WebDAV-on-Mac with working uploads structurally requires the 90 s wait on macOS 15.4+

This is the core finding the Cryptomator clone helped us prove. Two
mount paths, two different protocols (NetFS API + osascript-driven
Finder mount), same wait. The variable is below us, in the kernel's
WebDAV state machine. Letter 06 is explicit: "every PROPFIND-side
fix has been tried and falsified. The only way out is off WebDAV
entirely."

### 2. Production apps default *off* WebDAV on Mac

Cryptomator's default Mac mount is **FUSE-T** (their `FuseTMountProvider`,
in [Mounter.java](../../references/cryptomator/src/main/java/org/cryptomator/common/mount/Mounter.java)).
WebDAV is a fallback for users who can't or won't install FUSE-T.

This is significant: Cryptomator has the same "we ship to non-technical
users on Mac" constraint Comprador has, and they picked FUSE-T —
which requires a separate installer — over WebDAV. Their reasoning
is presumably the same 90-second-wait we hit. They eat the install
ceremony rather than ship the wait.

We picked NFS instead, with no installer (NFS is in the kernel).
Different escape from the same wall.

### 3. Pluggable mount providers as a code shape

[Mounter.java](../../references/cryptomator/src/main/java/org/cryptomator/common/mount/Mounter.java)
delegates to discoverable `MountService` implementations. Each
provider declares capabilities (`MOUNT_AS_DRIVE_LETTER`,
`MOUNT_TO_EXISTING_DIR`, etc.). The orchestrator picks one based on
the user's OS and what they have installed.

Comprador doesn't need this — we have one mount strategy per
platform (NFS on Mac, nothing else) — but the *shape* is worth
recognizing if we ever ship multiple mount paths (e.g., NFS for
modern Mac, WebDAV fallback for older or for users who report NFS
problems). If we go there, look at this file for the dispatch
pattern.

## Things to steal

**Nothing code-level.** Different language (Java), different
problem domain (vault decryption, not phone access), different
target audience (privacy-conscious power users, not non-technical
photo-offloaders).

The architectural artifact worth carrying:

- **"Default off WebDAV on Mac" is the right call.** Two
  independent products converged on this: Cryptomator picked
  FUSE-T; Comprador picked NFS. Both because WebDAV-on-Mac has
  problems neither could engineer around. If you're ever tempted
  to revisit "could we make WebDAV work for us?", the answer
  Cryptomator gives is no.

## Things to *not* steal

- **GPLv3 contagion.** Cryptomator is GPLv3; we're MIT (inherited
  from OpenMTP). Don't lift code into our tree even if it would be
  useful — license incompatible.
- **FUSE-T as a mount option.** It's a viable choice but it
  reintroduces the install-ceremony cost we explicitly ruled out.
  See [USER.md](USER.md) on permission prompts during setup.
- **The plugin-architecture for mount providers.** YAGNI for
  Comprador's current scope. If we ever ship multiple mount
  strategies, revisit.

## Receipts

- The investigation arc:
  [letter 06](../correspondence/06-after-the-quota-impasse/letter.md)
  — search for "Cryptomator" and "AppleScript"
- The 93-second AppleScript-mount measurement:
  letter 06 paragraph 6
- The conclusion (WebDAV-on-Mac wait lives below NetFS):
  [MISTAKES.md](MISTAKES.md) entry for WebDAV mount time
- The pivot it forced:
  [PIVOT-NFS.md](PIVOT-NFS.md), [MVP-NFS.md](MVP-NFS.md),
  CHANGELOG v0.3.0
- Cryptomator's mount orchestrator:
  [src/main/java/org/cryptomator/common/mount/Mounter.java](../../references/cryptomator/src/main/java/org/cryptomator/common/mount/Mounter.java)
- Cryptomator on GitHub: https://github.com/cryptomator/cryptomator
