# Third-Party Notices

Comprador is licensed under the GNU General Public License, version 3 or
later (see [LICENSE](LICENSE)).

The components and dependencies below are distributed under their own
licenses. Their copyright notices and license texts are reproduced or
linked here, as required.

---

## libmtp

The Go bridge links against [libmtp](http://libmtp.sourceforge.net/), a
library for communicating with MTP-class devices.

- **License:** GNU Lesser General Public License, version 2.1 or later
  (LGPL-2.1-or-later)
- **Upstream:** https://sourceforge.net/projects/libmtp/
- **License text:** https://www.gnu.org/licenses/old-licenses/lgpl-2.1.txt

Comprador links to libmtp dynamically via cgo against the system-installed
shared library (typically `/opt/homebrew/lib/libmtp.dylib` on macOS). The
LGPL permits this arrangement provided the user retains the ability to
substitute a modified version of libmtp; dynamic linking against the system
library satisfies this.

The libmtp header file used at compile time
(`bridge/vendor/libmtp.h`) is a verbatim copy of the upstream header and
is covered by libmtp's LGPL-2.1-or-later license.

---

## Logo

The Comprador app icon is by Sofia Montjoy
([@sojoy.scribbles](https://www.instagram.com/sojoy.scribbles/) for art,
[@sofi_amontjoy08](https://www.instagram.com/sofi_amontjoy08/)), commissioned in 2026 for
Comprador: a round medallion of a butterfly on a vine, on an ochre disc
with a green rim. The artwork was cropped to the medallion's bounds,
centred on a transparent 1024x1024 canvas with a margin for the macOS
icon grid, and committed as `images/icon.png`. The macOS `.icns` and
standard app-icon size variants are generated at build time by
`make icon` and are not committed (gitignored under
`MenuBarApp/Resources/Comprador.icns`). The website copy is a 384 px
downscale at `docs/site/images/icon.png`.

The earlier placeholder icon (a crop of a plate from Maria Sibylla
Merian's *Metamorphosis Insectorum Surinamensium*, 1705, engraved by
Pieter Sluyter; public domain, via Biodiversity Heritage Library /
Smithsonian Libraries) is no longer used in the app or on the site.

---

## Website backdrop art

The project website (`docs/site/`) draws a random background on each load
from a set of engravings by **Filippo Morghen** (b. 1730), an 18th-century
Italian engraver — including plates from his series imagining fantastical
life on the Moon (gourd-dwellings, ruins, and allegorical scenes).

- **Original works:** engravings by Filippo Morghen
- **Source:** Public Domain Image Archive —
  https://pdimagearchive.org/galleries/artists/filippo-morghen/
- **License of originals:** Public Domain Worldwide (no additional rights;
  the works are out of copyright by age).

The images are committed unmodified under
`docs/site/images/backdrops/`. They are decorative only and introduce no
new copyright restrictions.

---

## Website fonts

The project website (`docs/site/`) self-hosts its web fonts under
`docs/site/fonts/` — no CDN, so no visitor request for a font ever leaves
their machine (consistent with the project's no-telemetry stance). All three
are licensed under the **SIL Open Font License, version 1.1 (OFL-1.1)** and
are embedded unmodified:

- **Bona Nova** — body text and headings.
  Copyright 2020 The Bona Nova Project Authors
  (https://github.com/kosmynkab/Brygada-1918)
- **IM Fell Double Pica SC** — the "Comprador" wordmark (small caps).
  Copyright (c) 2010 Igino Marini (mail@iginomarini.com)
- **Fasthand** — the optional "Fun Mode" display face.
  Copyright 2020 The Fasthand Project Authors
  (https://github.com/danhhong/Fasthand)
- **Syne Mono** — the code voice: the headline's "tools", the inline commands, the snippet.
  Copyright 2017 The Syne Project Authors
  (https://gitlab.com/bonjour-monde/fonderie/syne-typeface)

- **Source:** OpenFont (https://openfont.org/)
- **License text:** https://openfontlicense.org/

Each family carries a Reserved Font Name under the OFL; Comprador embeds the
fonts as-is and does not create modified versions under those names, so the
OFL's embedding and self-hosting permissions apply cleanly.

---

## Galatea

The Go bridge embeds [Galatea](https://github.com/terraceonhigh/galatea),
an in-house userspace NFSv4 server, statically linked into the `bridge`
binary (it replaced the bundled WebDAV server and the patched
`willscott/go-nfs` NFSv3 server, both removed in v0.4.0). Galatea is a
sibling project by the same author.

- **License:** GNU General Public License, version 3 or later
  (GPL-3.0-or-later) — the same license as Comprador, so the combined
  binary is license-consistent.
- **Source:** https://github.com/terraceonhigh/galatea — the corresponding
  source for the linked version is the public repository; the exact pinned
  version is recorded in `bridge/go.mod`.
- **License text:** https://www.gnu.org/licenses/gpl-3.0.txt

---

## Historical: OpenMTP

Comprador was originally created by forking
[OpenMTP](https://github.com/ganeshrvel/openmtp) by Ganesh Rathinavel,
which is distributed under the MIT License. As of commit `402f147`
("Remove all legacy OpenMTP code"), no source code from OpenMTP remains
in this repository — Comprador is a clean reimplementation with a
different architecture (Go WebDAV bridge + Swift menu bar app, in place
of Electron + Node.js).

The fork relationship has been formally severed on GitHub, and this
project no longer carries OpenMTP's MIT copyright. The historical
acknowledgement is preserved here for transparency.
