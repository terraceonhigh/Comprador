# Comprador security: threat model + invariants

First-pass threat model for Comprador. Captures the surfaces the
app touches, the invariants we maintain, and what we explicitly
track. Not an audit. Use it as a checklist when reviewing PRs
that touch USB, networking, privileged helpers, or library
loading.

## User model assumption

Per [USER.md](USER.md), the user is non-technical and will not
debug failures. Harm-per-incident is high even when
probability-per-incident is low, so the threat model leans
defensive: surfaces are kept narrow by default, invariants are
documented so they can be grepped against future PRs.

## What Comprador touches

| Surface | Privilege | Risk shape |
|---|---|---|
| `com.apple.security.device.usb` entitlement | User-level USB | Bridge can read/write any USB device, not just MTP/PTP. Today the bridge filters by vendor/product; a regression that loosened the filter would expose other USB-attached hardware (Yubikeys, smartcards, audio interfaces). |
| Bridge subprocess (`bridge` in `Contents/Resources/`) | User-level | Reads/writes everything the user can. A compromise of the bridge — libmtp CVE, libusb CVE, our own parser bug, a malicious phone — is a user-level compromise. |
| Bundled libmtp / libusb (`Contents/Frameworks/`) | User-level | CVEs upstream become Comprador CVEs. Library validation is disabled (`com.apple.security.cs.disable-library-validation`) because we ship third-party dylibs. |
| NFS server on loopback | Bound `127.0.0.1` | **Invariant: never `0.0.0.0`.** A regression that bound to a routable interface would expose the phone's filesystem to the LAN. |
| mDNS hostname registration (`<DeviceName>.local`) | User-level dns-sd | Two consequences. (a) name collision with an existing LAN service can mask or misdirect that service; (b) on multi-user machines, the active user's phone name is advertised to the whole network. Untested under collision. |
| `killall ptpcamerad` | User-level | Process-name-based; limited blast radius. Recoverable: launchd respawns in ~60 ms. **Not** privileged in MAS terms; sandbox prohibits this. |
| ~~Privileged helper (`comprador-helper`, `SMAppService.daemon`)~~ | — | **REMOVED in v0.4.0.** Was the single largest privilege-escalation surface in the bundle (a root LaunchDaemon + Unix-socket RPC). It existed to launder root for `mount_nfs`; once loopback NFS mounts proved to work unprivileged it was vestigial, kept only for a cosmetic `/etc/hosts` volume label. Gone entirely — no root daemon, no admin prompt, smaller attack surface. The `SMAppService` *login item* (`SMAppService.mainApp`, non-privileged) is unrelated and stays. |
| Subprocess execution (Swift → bridge, `mount -t nfs`, `pkill`) | User-level | Argument lists are static array-form; no shell expansion, no user-supplied strings reach argv unvalidated. The volume label derived from the device name is sanitised app-side before it reaches `mount`. All subprocesses run as the user — no root component since the helper was removed in v0.4.0. |
| Reading from phone over MTP | User-level | A malicious phone can respond to GETs with crafted streams. Bridge currently buffers some metadata in memory; a parser bug in libmtp's PTP layer would execute in the bridge process. Bridge runs as the user, so impact is "compromise of the bridge subprocess." |
| Writing to phone over MTP | User-level | Bridge accepts NFS WRITEs from the kernel and stages them, committing to the phone via `bridge/staging` + the `mtpfsal` FSAL (the Galatea NFSv4 backend; the old willscott `MTPFileSystem` was removed in v0.4.0). A path-construction bug in the FSAL's write/rename path could address the phone at unintended paths — but the phone is the *destination*, not the host, so impact is contained to phone-side files. |
| Phone-side sensitive data | User-level | Files surfaced via Finder include whatever's on the phone — credentials, Authenticator backups, screenshots of secrets. Same shape as any USB drive mount. Not a Comprador-introduced risk; worth being honest about. |
| Distribution: notarized DMG from GitHub Releases | — | Notarization catches some malware patterns; isn't an audit. No auto-update mechanism, so silent supply-chain attack is impossible but silent CVE patching is also impossible. Stale installs sit on user machines indefinitely. |

## What Comprador deliberately doesn't touch

These omissions eliminate whole categories of risk and are part
of the security posture by design:

- **No telemetry, no Sentry, no analytics.** No phone-home means
  no server to compromise, no API key to mishandle, no
  identifiable usage data to leak. Memory of the
  [OPENMTP-NOTES.md](OPENMTP-NOTES.md) Sentry-rejection decision.
- **No account, no sign-in, no subscription.** No credentials to
  store, no auth token to leak.
- **No background networking.** The bridge binds to loopback; the
  Swift app makes no outbound connections.
- **No automatic updates.** User-driven download from GitHub
  Releases. Trade-off: also no silent CVE patching.

## Invariants

These are properties we maintain across all changes. A PR that
violates one should not merge without explicit threat-model
review.

1. **NFS server binds to `127.0.0.1` only.** Never `0.0.0.0`.
   Never a routable interface. The phone's filesystem is exposed
   only to processes on the host running Comprador.
2. **No privileged helper.** The root `comprador-helper` daemon was
   removed in v0.4.0 (loopback NFS mounts unprivileged, so it was
   vestigial). The bundle ships no root component. Re-introducing
   one requires a deliberate threat-model review.
3. **No shell interpolation of user-supplied strings into argv.**
   Subprocess spawns use array-form argument lists.
4. **No phone-home.** No outbound network connections from any
   Comprador binary. (Bridge's loopback NFS server does not
   count.)
5. **Bundled dylibs are pinned, signed, and bundled via
   `@loader_path`.** No `DYLD_LIBRARY_PATH` reliance (SIP strips
   it anyway per [MISTAKES.md §18](MISTAKES.md)).

## Tracked items

- **Upstream libmtp and libusb CVEs.** No formal subscription
  today. Manual cadence: check
  [libmtp upstream releases](https://sourceforge.net/projects/libmtp/files/libmtp/)
  and the
  [libusb GitHub releases](https://github.com/libusb/libusb/releases)
  on each Comprador release (v0.x.0). If either has shipped a
  security fix since our last bump, rebuild and ship.
- **macOS sandbox / entitlement policy changes per release.**
  Apple periodically tightens what counts as a privilege; the
  `requestUploadFile` deprecation in macOS 14 is one example
  ([RESEARCH-IMAGECAPTURECORE.md](RESEARCH-IMAGECAPTURECORE.md)).
  Worth a quick scan of WWDC sandbox/entitlement session notes
  per major macOS release.

## Deferred / not-yet-tracked

- **Static analysis of the Go bridge.** `go vet` + `gosec` on
  every PR would be useful. Not currently in CI. File for v0.4+.
- **Fuzz testing of the libmtp binding layer.** The cgo
  callbacks are the most exposed surface to potentially-malicious
  phone responses. Not done. Not on the immediate roadmap.
- **Threat model for multi-device.** N concurrent bridges = N×
  the bridge surface area. [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md)
  should reference this doc; this doc should grow a multi-device
  section when that implementation lands.

## How to update this document

When a PR adds a new surface, add a row to "What Comprador
touches." When a PR establishes a new invariant, add it to
"Invariants" with the PR number. When an external policy change
(macOS, libmtp, etc.) changes our risk shape, add a note under
"Tracked items" or "Deferred."

This doc is a checklist, not a marketing document. Keep it
factual and grep-able.
