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

## golang.org/x/net

The Go bridge uses the `golang.org/x/net/webdav` package.

- **License:** BSD 3-Clause
- **Upstream:** https://pkg.go.dev/golang.org/x/net
- **Copyright:** Copyright (c) 2009 The Go Authors. All rights reserved.

```
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

---

## Logo

The Comprador app icon is derived from a plate in Maria Sibylla
Merian's *Metamorphosis Insectorum Surinamensium* (1705), depicting
the pomegranate (*Punica granatum*) with associated Surinamese
butterflies and caterpillars. The plate is engraved "P. Sluyter
Sculp." (Pieter Sluyter).

- **Original work:** Maria Sibylla Merian, *Metamorphosis
  Insectorum Surinamensium*, 1705
- **Source institution:** Biodiversity Heritage Library / Smithsonian
  Libraries and Archives
- **Source URL:**
  https://pdimagearchive.org/images/0b96d3ac-8cf3-4ffd-865f-f906a2fd7962/
- **License of original:** Public Domain Worldwide (no additional
  rights). The work is well out of copyright by age (Merian d. 1717,
  Sluyter d. ~1730).

**Modifications for Comprador:** the full plate was cropped to a
square framing the brown eyespot-butterfly perched on the small red
pomegranate flower (upper-center-left of the original). The
intruding tip of the Blue Morpho wing in the upper-right corner of
the crop region was retouched out so the eyespot-butterfly stands
alone. The cropped square is committed as a single PNG at
`images/icon.png` (1660×1660). The macOS `.icns` and standard
app-icon size variants are generated at build time by `make icon`
and are not committed (gitignored under
`MenuBarApp/Resources/Comprador.icns`).

These modifications introduce no new copyright restrictions; the
derivative remains in the public domain.

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
