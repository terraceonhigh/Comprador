# Who Comprador is for

This is a model, not data. It captures the working assumption we
make decisions against. Sharpen it as we learn from real users.

The model is deliberately short. A persona doc that runs forty pages
is a persona doc nobody reads when they need to make a call.

## The user

Someone who:

- **Owns an Android phone and a Mac.** A less common pairing than
  All-Apple or All-Google. They picked one or the other for reasons
  that aren't tribal — work supplied the Mac, a partner has an
  iPhone but they prefer Android, the Android phone takes better
  photos, they like Pixel cameras, etc.
- **Did not pick the Mac for tinkering.** They like it because it's
  pretty and works. They do not have Homebrew installed. They have
  never opened Terminal voluntarily.
- **Used to use Android File Transfer.** Google killed it as a
  standalone macOS app at the end of 2024. They knew AFT was clunky;
  they tolerated it. Now it's gone, or it stopped working after a
  macOS update, and they're looking for the next thing.
- **Tried OpenMTP, briefly.** They downloaded the 360 MB Electron
  app, opened it once, found the web-feeling UI ugly or sluggish or
  confusing, and closed it. They might not remember the name. They
  remember the feeling.
- **Knows what File Transfer mode is on their phone** because they
  have used it before. They do not know what MTP, PTP, USB-C alt
  mode, IOKit, NFS, or WebDAV are, and have no reason to learn.
- **Will not enable Developer Options.** Even if a forum post tells
  them it's two taps. They have been online long enough to know that
  Developer Options is "the part of the phone that breaks things."
- **Will not learn a second file manager.** Finder is the thing on
  the Mac that holds their files. If something is "not in Finder,"
  it might as well not exist.

How often, what for:

- Plugs in **once a week, sometimes once a month.** Not daily.
- Wants to move **5–500 photos at a time.** Occasionally a video.
  Rarely anything bigger than a few GB.
- Sometimes the direction is phone → Mac (offload the photo roll).
  Sometimes Mac → phone (sideload music, podcasts, an offline movie
  for a flight).
- Does not want to back up the phone. Does not want to sync the
  phone. Just wants to copy specific files, the way a USB stick
  works.

## What they want

Their phone's storage to appear in Finder, like a thumb drive does.
Drag files in either direction. Eject. Done.

That is the entire affirmative content.

## What they don't want

- Permission prompts during setup beyond the macOS-native ones they
  can't control
- Settings to configure
- A menu-bar app to attend to (it can be there; they will not look
  at it unless something is wrong)
- A new file manager UI to learn
- Knowledge of "ports", "mounts", "WebDAV", "NFS", "MTP", "ptpcamerad"
- A subscription, an account, a sign-in
- A right-click → Open dance on first launch
- Gatekeeper warnings
- Notifications they have to dismiss

## What they tolerate

- One physical action per session: plug in, tap **File Transfer**.
- A few seconds of mount-time wait. Probably up to ten before they
  start to wonder if it's broken.
- Files appearing on the phone in approximately the right place.
  They will navigate the phone's directory tree to find them; they
  do not expect Comprador to know that "music goes in Music."
- A standard Finder copy progress bar. They trust progress bars.
- A clean ejection from the menu bar. We get to claim that surface
  for the eject action only.

## What breaks the trust

These are the failures that make them stop using the app:

- **A failure they have to debug.** "Error code -36" with no clear
  remedy. They will not know what -36 is and will not look it up.
- **A file disappearing or appearing partially copied.** If Finder
  says "copied" and it's not on the phone, the app is broken to
  them, regardless of what the protocol layer thinks.
- **Two volumes appearing in Finder when they plugged in one phone.**
  They will pick the wrong one and lose a copy into a stale mount.
- **The volume going stale and not refreshing** after a phone-side
  change. They delete a photo on the phone, it still appears on the
  Mac; they conclude the app is "out of sync" and stop trusting it.
- **Comprador appearing to do nothing for 15+ seconds.** No icon
  movement, no status text, no progress.
- **A copy that says "succeeded" in Finder but isn't actually on the
  phone.** This is the worst class of failure — silent corruption of
  the trust contract. Worse than an error.

## How decisions get tested against this

When choosing between options, ask:

1. **Does this add a step the user has to perform?** Bad. Even if
   the step is "click Allow once." The step you don't add is the
   one that doesn't break.
2. **Does this remove a step?** Good.
3. **Does this introduce a state where the user has to debug
   something?** Bad — even if the failure mode is rare. They will
   not debug; they will quit.
4. **Does this make a failure mode silent vs. visible?** Visible is
   better, but only if the user can act on the visibility. A red
   error icon they can't do anything about is worse than no icon.
5. **Does this introduce technical vocabulary into the UI?** Bad.
   "MTP", "WebDAV", "writeseq cap", "session goroutine" — none of
   these belong in user-visible text.
6. **Does this require the user to read documentation before it
   works?** Bad. The README is for us and for technical-adjacent
   contributors; a non-technical user should not have to consult it
   to use the app.

## Worked examples

How this model has been used (or should have been) in past calls:

- **The async-commit upload shortcut, rejected
  ([letter 03](../correspondence/03-end-of-day-2026-05-06/letter.md)).**
  "Done in Finder no longer means on phone — that's an unacceptable
  risk." The reason the risk is unacceptable: silent corruption of
  the trust contract is the worst failure class for this user.
- **macFUSE / kernel extensions / SIP-disable, rejected
  ([CLAUDE.md](../CLAUDE.md), [TODO.md:296](../TODO.md)).** Each
  imposes setup steps. Each fails the "does this add a step" test.
- **The 90-second WebDAV mount wait, escaped via NFS pivot
  ([PIVOT-NFS.md](PIVOT-NFS.md)).** Closer to "Comprador appearing
  to do nothing for 15+ seconds" — past that threshold, the app
  feels broken. The NFS pivot drops mount time below 1 s; correct
  call.
- **Notarization, paid for at v0.2.3.** "First launch is just a
  double-click" eliminates the right-click → Open dance. Worth $99/yr.
- **Auto-killing `ptpcamerad` rather than asking the user to close
  Image Capture.** SwiftMTP's choice (tell the user) fails the
  "does this require knowledge of the system" test. Our choice (do
  it for them) eats engineering complexity to keep the user
  ignorant of the conflict — correct for this user.

## What we don't claim about them

- Their age, gender, profession, location.
- Whether they're "tech-savvy." The negation list above is the
  shape we have, not a demographic.
- Their patience for long-running operations. We've assumed
  ~15 s of apparent inactivity is the cliff; this is not measured.
- Their tolerance for the app being unsigned. We already pay for
  notarization; this is not an open question for them.
- The phone they're using. Comprador supports any vendor in
  `VendorIDs.plist` and any PTP-class device; the test phone in our
  development is a Sony Xperia 10 III but the user's is whatever
  they happen to own.

## What's deliberately undecided

These are the assumptions worth flagging — if a real user pushes
back on them, the assumption is open for revision.

- **Whether they would prefer a native app window** like SwiftMTP
  if Finder integration meant any cost. We've assumed the answer
  is "no — the Finder integration *is* the product." A user who
  says "I'd rather have a small app to open" is data we should
  take seriously, not dismiss as wrong.
- **Whether they care about transfer speed or transfer reliability
  more.** We've optimized for reliability (truncation guard,
  resumable-uploads architecture, 102 keepalive) on the assumption
  that "I dragged it and it didn't make it" is worse than "I
  dragged it and waited longer than I expected." Probably right;
  not measured.
- **Whether multi-device mounting matters.** Currently single device
  at a time. SwiftMTP supports multi-device. We've assumed our user
  has one phone; a user with a phone and a tablet, or a phone and a
  camera plugged simultaneously, is unaccounted for.

## How to update this document

When you make a decision that turned on this model, add it to
"Worked examples" with a one-line explanation of which test it
passed or failed.

When you encounter a real user (in a GitHub issue, a forum thread,
a friend's reaction), add their actual behavior to the appropriate
section. Real data overrides this model.

When the model contradicts itself or fails to settle a call you
needed to make, write down the gap. Gaps are how this document
improves.
