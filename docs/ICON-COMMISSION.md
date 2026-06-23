# Icon Commission: Sofia Montjoy

Working masterdoc for the Comprador app-icon commission. Part 1 records what
has actually been agreed and sent (the source of truth, so nothing lives only
in an Instagram thread). Part 2 is the engineering translation: what a
non-technical visual artist needs spelled out to deliver an icon that works as
a real macOS app icon, and the clean split of who does what.

Last updated 2026-06-22.

---

## Part 1: The brief as sent

### Who and where
- Artist: **Sofia Montjoy**, reached on Instagram.
- Status: deal struck, deposit paid, Phase 1 (sketches) in her court.

### The concept communicated
Verbatim themes from the thread, in the order they were given:
- "an icon that describes mediation between two worlds"
- "being the translation layer"
- "enabling interactions between cultures"
- Name origin, as told to her: "Comprador comes from Portuguese, for the local
  translator at ports who made colonial trade possible."
- What the software does, in her words to design against: a Mac app. You install
  it, plug in an Android phone (or a drone, or a smartwatch), and its files
  appear so you can browse them.

### Style direction given
- "Preferably organic or baroque or rococo."
- "Art Nouveau is also acceptable."
- Phase 1 ask: "look at the links I sent you to develop a few monochrome
  sketches."
- "the Iberian element, essentially."
- "something that is colonial, like the churches and old buildings from our
  hometowns."
- Latitude granted: the current icon "is the placeholder, but you are welcome to
  do anything that matches with this website."
- Sofia floated a butterfly; left open.

### References sent to her
| Link | What it is | Why it matters |
|---|---|---|
| `terraceonhigh.github.io/Comprador/` | The live landing page | Sets the palette and mood: parchment ground, pomegranate-red accent, teal links, antique-engraving backdrop, IM Fell wordmark. The icon should sit on this site without clashing. |
| `en.wikipedia.org/wiki/Comprador` | The word's meaning | The intermediary/translator concept. |
| `pdimagearchive.org/images/0b96d3ac-…` | A public-domain reference image | Inspiration. |
| `github.com/terraceonhigh/Comprador/blob/master/images/icon.png` | The current placeholder icon | A 1705 Merian/Sluyter pomegranate engraving (public domain; see [NOTICES.md](../NOTICES.md)). She may keep, reinterpret, or replace it. |
| Figma "App Icon Template (iOS, iPadOS, watchOS 27)" | A grid/safe-area template | Useful for proportions. Note it is an **iOS** template, not macOS; see Part 2 on why that is fine for the master but not the whole story. |

### Deliverable as stated so far
- "principly, I would like a 1024x1024 SVG or PNG."
- That is the only spec given. Part 2 fills in everything a usable icon also
  needs that has not yet been said out loud.

### Commercial terms (agreed)
- **Price: $65 CAD total.** ("$65, but depends on how much detail" → settled.)
- **30% up front, 70% on completion.**
- **Deposit paid: $19.50** (30% of $65) by e-transfer.
- **Balance owing: $45.50** on delivery.
- E-transfer to **Montjoysofia76@gmail.com**.

### Open items not yet answered
- **Deadline.** She asked "when do you need it for?" and it was never answered.
  Set one.
- **What "completion" means** for the 70%: is the monochrome menu-bar version
  (Part 2) inside the $65, or a separate ask? Worth naming before she starts so
  the second 70% is unambiguous.
- **Source files.** Nobody has asked for the layered/editable original yet. Ask
  (Part 2 explains why it is worth more than the flattened PNG).
- **Revision rounds.** How many sketch concepts in Phase 1, and how many rounds
  of changes are included.

---

## Part 2: What a non-technical artist needs to deliver a working icon

The brief she has is an *artistic* brief. It is missing the *technical*
constraints that make a beautiful 1024px image actually function as a macOS app
icon. None of these are her fault to know, and most of them are not hers to
solve. The right model is to **carry the technical burden ourselves** and ask
her only for the artwork. The two lists below draw that line.

### The split: what she delivers vs. what we handle

**Sofia delivers** one design in **two forms**, plus the source:
1. The **full-colour app icon** (Dock/Finder): master artwork at 1024×1024 px,
   square, full-frame.
2. The **monochrome glyph** (menu bar): the same central motif reduced to a flat,
   single-colour silhouette on a transparent background, no shading or shadow.
   She is already sketching in monochrome, so this is a small extra step from the
   same mark, not a second project.
3. The **editable source file** (Illustrator/Affinity/Figma/Procreate, whatever
   she works in), with layers intact, so we can produce the small sizes without
   going back to her for every tweak.

The two-form requirement is structural, not cosmetic: it means the design must be
built around **one strong central shape** that survives being stripped to a flat
silhouette. That is a spec, so it stays in the outward brief; our guesses about
*what* that shape should be do not (see the note below).

**We handle (no need to involve her):**
- Generating every required pixel size (the 10-PNG set: 16, 32, 128, 256, 512,
  each at 1× and 2×) and packing the `.icns` via `make icon`.
- **Applying the rounded-rectangle "squircle" mask and drop shadow ourselves**
  (decision 2026-06-22: Comprador ships the standard rounded Mac tile, and the
  classic `.icns` pipeline does not do this for free, so we add a masking step).
- Color-profile conversion for the build.
- Wiring any monochrome version in as the menu-bar template image, if and when
  we commission one (see Part 3).

Telling her this split up front is a kindness and a risk-reducer: she stops
worrying about "do I round the corners? what sizes? what file format for Xcode?"
and just makes one strong picture.

### The constraints that decide whether the art works

These are the ones a non-technical artist (and a non-technical client) most
often miss. In rough priority order:

1. **It has to read at 16×16 pixels.** This is the single most important and
   least obvious constraint. The icon will appear shrunk to a thumbnail in the
   Finder sidebar and the menu bar. Baroque/rococo detail is gorgeous at 1024px
   and turns to mud at 16px. The fix is not "less detail everywhere" but a
   **clear primary silhouette**: one recognizable shape that survives being
   shrunk, with the fine engraving as a second layer that only rewards a close
   look. Ask her to check her own sketch by viewing it at the size of a
   fingernail. If she can still tell what it is, it works. (This is likely why
   you both landed on monochrome sketches first: silhouette before ornament.)

2. **Design on a full square, but keep the important part in a safe zone.**
   Modern Apple icons (iOS already, macOS Tahoe now too) are **masked by the
   system** into the rounded "squircle." So:
   - Deliver a **full-bleed square**, art going edge to edge. Do **not** round
     the corners herself or draw the icon as a small badge floating on white.
   - But keep the essential motif inside the central ~**80%** ("safe zone"), so
     the mask never clips anything that matters.
   The Figma template she has is iOS-flavored, but this rule (full-bleed square +
   safe zone, system does the masking) is the same one we want for the Mac, so
   the template is fine to design against. We apply the actual Mac mask on our
   end.

3. **No text and no words in the icon.** Apple's own guidance, and it never reads
   at small sizes anyway. The wordmark lives on the website, not in the icon.

4. **Flat and front-facing.** The current convention is a head-on, flat
   composition, not a 3D object photographed at an angle, and not skeuomorphic
   chrome. An engraving aesthetic is flat by nature, so this suits her direction.

5. **Color space: sRGB is the safe deliverable.** If she can give Display P3,
   lovely, but sRGB is the no-surprises default and we can handle conversion
   either way. She does not need to think hard about this; "export as sRGB" is
   enough.

6. **The monochrome version is a real, separate artifact**, not just a sketch
   stage. For the menu bar, macOS wants a **flat, single-color shape on a
   transparent background**, no gradients and no shadows. The system recolors it
   (it shows up white in a dark menu bar, dark in a light one). So the monochrome
   deliverable should be the **silhouette of the motif, solid, on transparency**,
   not a grayscale shaded version. Worth stating plainly so she does not hand
   back a gray painting.

7. **Provenance of any borrowed imagery.** If she builds on the pomegranate
   engraving or any reference, it needs to be her own work or verifiably public
   domain, because we track image attributions in [NOTICES.md](../NOTICES.md).
   The current placeholder is PD (Merian/Sluyter, 1705). Just confirm anything
   she incorporates is clean.

### Concept and motifs (INTERNAL ONLY, deliberately not sent to her)

Decision 2026-06-22: the outward brief gives her **the links and the specs, and
nothing else**. We do not hand her a prescribed concept, a mood board in words,
or a motif list, so her own reading can flourish. The website, the Wikipedia
page, and the placeholder carry all the meaning and mood she needs; let them do
that work rather than steering her with our guesses.

The notes below are kept for *our* reference (so we can recognise a strong
direction when we see it), not to be pasted to her:

- Concept in one line: Comprador is the intermediary, the port translator who let
  two worlds trade; the app does the same for a Mac and a phone.
- Motifs that would reduce well to a glyph, if she happens to land near them: a
  pomegranate (continuity with the placeholder, and the crown gives a clean
  silhouette); a port arch or gateway; a key or bridge; a butterfly (the
  placeholder is from Merian's *Metamorphosis*). These are ours to *recognise*,
  not hers to be *told*.

### One flag worth raising with her
The template she has is iOS/iPadOS/watchOS. Comprador is a **Mac** app. For the
master artwork it makes no practical difference (full-bleed square + safe zone is
the shared rule, and we do the Mac packaging). But it is worth one sentence to
her so she does not chase iOS-specific minutiae that do not apply here.

---

## Part 3: Verified requirements and decisions (2026-06-22)

Researched against live Apple sources (HIG, WWDC25 sessions 361 + 247, archived
icon docs) and the repo's actual wiring. Three decisions were taken; they are
recorded here so the brief and the build stay aligned.

### The two conventions, and which we use
- **Classic (Big Sur to macOS 15):** the designer bakes the rounded rectangle,
  shadow, and ~10% gutter (artwork in an 824x824 area inside the 1024 canvas).
  macOS was historically the exception that did not auto-mask.
  ([Apple forum 670578](https://developer.apple.com/forums/thread/670578))
- **New (macOS 26 "Tahoe" + Icon Composer, WWDC25):** the system masks, shadows,
  and applies the "Liquid Glass" material; the designer delivers flat, full-bleed,
  layered art and bakes nothing.
  ([WWDC25 361](https://developer.apple.com/videos/play/wwdc2025/361/))

**Decision: classic `.icns` only, for now.** Comprador targets back to macOS 13
and uses the `sips` to `iconutil` pipeline. Icon Composer's `.icon` format would
force a Xcode 26 / macOS 26 build dependency, so it is parked as possible future
polish, not built now.

### Decision: ship the rounded "squircle" tile, and bake it ourselves
Comprador will use the standard rounded Mac app-icon tile (so it reads as native
in Finder and the Dock), **not** a hard square. The current `make icon` pipeline
only scales the raw square PNG; it does **not** apply a mask or shadow. So this is
a real build gap to close: add a rounded-rectangle mask plus drop shadow step to
`make icon`, applied to Sofia's flat square master. This keeps her instruction
("deliver full-bleed square, do not round the corners") correct, and keeps the
shape entirely on our side.

### Decision: menu-bar version decided after her first sketches
The menu bar is a separate icon surface. Today it uses **SF Symbols** only
(`externaldrive`, `externaldrive.fill`, `externaldrive.badge.xmark`) for four
states (idle, connecting, mounted, error), at 18pt via `NSStatusItem`. A custom
brand glyph there must be a **template image**: a pure-black shape on
transparency (only the alpha channel survives recoloring), roughly a 16pt glyph
inside the 22pt bar, supplied as PDF/SVG or @1x/@2x PNG with `isTemplate = true`.
([HIG: the menu bar](https://developer.apple.com/design/human-interface-guidelines/the-menu-bar),
[Bjango](https://bjango.com/articles/designingmenubarextras/))

Tension to resolve later: the menu bar currently signals **state** through
different symbols; one custom glyph cannot do that. So a monochrome deliverable
would most likely become the idle/brand mark while the state badges stay SF
Symbols, unless we widen the commission. Held until her concepts land, since the
mark may or may not reduce cleanly to a 16pt silhouette.

### The size set we generate (reference)
macOS wants 5 point sizes x 2 scales = 10 PNGs: 16, 32, 128, 256, 512, each at 1x
and 2x. `512@2x` is the 1024 image. The `.iconset` naming plus `iconutil -c icns`
route is what the Makefile already does. Notarization does not inspect icons at
all; it checks signing and hardened runtime only.

### Current wiring, for reference
- Source: `images/icon.png`, 1660x1660 (the Merian pomegranate placeholder).
- Build: `make icon` runs `sips` then `iconutil` to produce
  `MenuBarApp/Resources/Comprador.icns` (gitignored), referenced via
  `CFBundleIconFile`. No asset catalog.
- Menu bar: `AppDelegate.swift` `updateIcon(state:)`, SF Symbols only.
