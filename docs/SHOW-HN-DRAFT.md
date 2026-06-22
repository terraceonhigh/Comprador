# Show HN draft

> Draft for the launch. HN rewards a plain title and a technical, honest story
> over a pitch. Lead with what it is, then the interesting engineering, then the
> limits. No em dashes, no marketing voice. Post Tue to Thu, morning US time;
> stay in the thread for the first several hours to answer.

## Title (pick one)

- `Show HN: Comprador - mount an Android phone in Finder, no kernel extension`
- `Show HN: Comprador - an Android File Transfer replacement that mounts in Finder`

First is plainer and leads with the technical hook (no kext). Prefer it.

## Body

Google quietly orphaned Android File Transfer. The download vanished from
android.com in early 2024, the page now points to a Windows-only app, and the
old tool was never updated, so it fails on Apple Silicon and recent macOS. I
wanted my phone to just show up in Finder like a USB drive, so I built Comprador.

It's a menu-bar app plus a small Go bridge. The bridge talks to the phone with
libmtp over cgo and serves the phone's filesystem as a userspace NFSv4 volume on
a loopback port. macOS mounts NFS natively, so there's no kernel extension, no
macFUSE, no File Provider extension, and no developer mode or USB debugging on
the phone. You plug in, tap File Transfer, and the phone is in the Finder
sidebar.

The part I found most interesting: the serving layer started as a patched
willscott/go-nfs NFSv3 server, and NFSv3's RPC-timeout window made long libmtp
reads (multi-minute, on large files) impossible without an ugly prefetch
workaround. Moving to a userspace NFSv4 server (a sibling project, statically
linked) removed that constraint entirely. NFSv4's floor tolerates the slow
reads, so the workaround is gone, and a real consequence falls out of it: you
can play or scrub a video straight off the phone in Finder without copying it to
the Mac first. The NFSv3 path timed out on exactly that.

It's signed and notarized, free, and GPLv3. Works with Android phones and also
with anything else libmtp recognizes as MTP or PTP: cameras, the newer Kindles
(which switched to MTP in 2024), Garmin music watches, the Nintendo Switch.

Limits, honestly: Apple Silicon and macOS 13+ only, no Intel build. USB only, no
wireless. The one rough edge is a connect-time race with macOS's own
ptpcamerad over the USB interface, which the app works around by seizing and
re-enumerating the device; a replug fixes the rare case it loses.

Source and a download are linked. I'd genuinely like to hear where it breaks on
hardware I haven't tested.

GitHub: https://github.com/terraceonhigh/Comprador

## Suggested first comment (the technical deep-dive HN tends to ask for)

A few details I left out of the post for length:

- The whole MTP session runs on a single goroutine because libmtp isn't
  thread-safe; every NFS operation is marshalled onto it and blocks on a reply
  channel. That serialization is also what makes concurrent multi-device work:
  each phone gets its own bridge subprocess, mount, and session, so two phones
  are just two independent loopback NFS servers.
- The NFS server binds to 127.0.0.1 only. There is no outbound network from any
  of it: no telemetry, no phone-home, no auto-update. The loopback NFS server is
  the only listener.
- Android lies about a file's size for a moment right after a write (it reports
  0 during a finalize window), which on a naive mount makes a just-written file
  read back empty. The bridge refuses to let a device-reported 0 overwrite a
  size it already knows.

## To have ready before posting

- The streaming-off-the-phone demo as a short clip or GIF. This is the hero; it
  is the one thing no competitor can show.
- A personal Homebrew tap live so `brew install --cask` works for the HN crowd.
- Be at a keyboard for the first few hours.
