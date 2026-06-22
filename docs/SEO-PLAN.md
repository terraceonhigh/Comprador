# SEO & Discovery Plan — Comprador

> Research compiled 2026-06-21 from four parallel research passes (keyword/intent,
> competitor positioning, SERP/ranking-content landscape, off-page/distribution).
> **No hard search volumes anywhere in this document** — every demand signal below
> is *ordinal* (Google autocomplete position, SERP composition, forum frequency),
> never absolute. Do not let anyone insert fabricated volume numbers. Before
> committing real content budget, validate cluster ordering against a real
> volume tool (Keyword Planner / Ahrefs / SEMrush).

---

## 1. The one strategic conclusion

The SERP for "Android File Transfer for Mac" splits into two worlds:

- **World A — affiliate/listicle SERPs (do NOT fight head-on).** The head terms
  (`android file transfer alternative mac`, `mtp mac`, `connect android to mac`)
  are owned by aged, link-heavy, well-funded commercial sites — Eltima's
  **MacDroid** + **Commander One** network (including the soft-branded "review"
  site `android-file-transfer-mac.com`), **Tenorshare**, **iMobie**, **dr.fone** —
  plus incumbent **OpenMTP**. Displacing them for positions 1–3 is a backlinks +
  domain-age game, not an on-page-quality game. OpenMTP ranks #1 for `mtp mac`
  with a *thin* page purely on authority/brand. A brand-new single page enters
  mid-page-1 at best and climbs slowly.

- **World B — community/long-tail SERPs (THIS is where we win).** A large set of
  device-specific and troubleshooting queries return Garmin Forums, MobileRead,
  kboards, Apple Discussions, Reddit, and personal blogs — **no commercial page
  owns them.** That vacuum is the opening.

**Strategic read:** don't out-listicle the listicles. Win the procedural and
device long-tail with focused pages, harvest "People Also Ask" with FAQ content,
and treat the head terms as a slow brand/backlinks build, not a page-structure
problem.

---

## 2. Winnable keyword clusters (World B — build here first)

Ordered by rank-ability × differentiation. "Currently owned by" shows the (beatable)
incumbent.

1. **Newer-Kindle via MTP — freshest, most underserved.** 2024+ Kindles (base,
   Paperwhite 12th-gen, Colorsoft, Scribe) switched from USB mass-storage to MTP;
   the SERP hasn't caught up. Owned by blogs/forums, not tools.
   - `mount kindle as drive mac`, `connect kindle to mac usb`, `kindle not showing
     up on mac`, `transfer files to kindle scribe mac`, `kindle paperwhite mtp mac`
2. **Garmin not showing in Finder.** Owned by Garmin Forums + a Medium post.
   - `garmin not showing in finder mac`, `garmin mtp mac`, `access garmin folder on mac`
3. **Competitor-brand + frustration** (cheapest qualified traffic).
   - `openmtp not working`, `openmtp alternative`, `openmtp m4`, `macdroid free
     alternative`, `commander one alternative`, `handshaker alternative` (abandoned
     tool — orphaned users)
4. **"AFT not working on [current macOS]"** — rots each OS release; farms neglect it.
   Publish a template page, refresh per release.
   - `android file transfer not working sonoma/sequoia/tahoe`, `mtp mac apple silicon`,
     `mac not recognizing android phone`, `android file transfer no device found mac`
5. **Nintendo Switch captures to Mac.** Owned by HowToGeek / personal blogs.
   - `transfer switch screenshots to mac usb`, `nintendo switch mtp mac`
6. **Camera / DCIM access** without Photos.app.
   - `fujifilm mtp mac`, `sony camera mtp mac finder`, `access dcim folder camera mac`
7. **"Free / open-source" modifier** (our structural wedge; top tools are paid).
   - `free mtp app mac`, `open source mtp mac`, `android file transfer mac free open source`

**Head-adjacent, least-farmed (realistic secondary targets):** `mtp mac`,
`mount android phone on mac`, `mount android as drive mac` — more technical
phrasings the affiliate sites under-optimize.

**Trust-intent (easy, high-conversion for an OSS app):** `is android file transfer
safe`, `is openmtp safe`.

---

## 3. Competitive positioning

### The dead incumbents (demand drivers)
- **Android File Transfer (Google).** The orphaned giant. **Accurate framing
  (use this, not vendor myth):** Google *quietly removed the download link* around
  Feb 2024 (timed to the Android 15 preview); the page was later fully repurposed
  to Windows-only **Quick Share**. There was **no formal "discontinued in May 2024"
  announcement** — that's vendor SEO copy. The defensible, more-damning line:
  *"Google quietly orphaned it; it's unmaintained and broken on Apple Silicon /
  Sonoma / Sequoia, and the only official replacement (Quick Share) is
  Windows-only."* Notorious failures fuel the highest-intent query in the space:
  **"android file transfer mac not working"** (4 GB per-file cap; "Could not
  connect to the device"; breakage on macOS 14/15).
- **HandShaker (Smartisan).** Abandoned (last build Feb 2022); orphaned users.

### Live competitors
- **OpenMTP** — free/OSS community default; our closest positioning rival. Owns
  *"Tired of expensive, outdated, bug-heavy Android File Transfer apps? … Safe,
  Transparent, Open-Source and FREE."* **Structural gap: it's a separate Electron
  window, NOT a Finder mount;** reviewers cite needing to babysit a window, plus
  mid-transfer disconnects and post-sleep flakiness.
- **MacDroid (Eltima)** — dominant paid player; **already markets "Mount Android as
  a disk on Mac / edit in Finder."** Free tier is one-way only; PRO $19.99/yr or
  $34.99 lifetime. **Buried weaknesses:** its fast path is **ADB mode (needs
  Developer Options + USB debugging)** — the exact friction Comprador avoids — and
  it prompts for **system-extension / file-system permissions** on first launch.
- **Commander One (Eltima)** — power-user file manager; MTP behind $29.99 PRO;
  files trapped in a dual-pane window, never in Finder's Locations; the Mac App
  Store build can't mount at all (sandboxing).
- **AnyDroid (iMobie)** — migration suite; $39.99/yr; separate window, not a mount;
  perma-"50% off" anchor pricing.
- **FUSE/CLI true-mount tools** (go-mtpfs, jmtpfs, mtp-mount) — mount real volumes
  but need macFUSE kext and are CLI-only; not consumer apps. `mtp-mount` (Rust,
  kext-less via FUSE-T) is the only true *technical* analog to Comprador's thesis.
- **Wireless alternatives** (LocalSend, KDE Connect, PairDrop) — push/send only,
  none a browseable mount.

### The differentiator — it's the COMBINATION, not "Finder mount" alone
"Native Finder mount" is **not** unique (MacDroid markets it; FUSE tools do it).
The defensible wedge is the full stool, where every rival is missing ≥1 leg:

| Property | OpenMTP | MacDroid | Commander One | AnyDroid | FUSE/CLI | **Comprador** |
|---|---|---|---|---|---|---|
| Free & open-source | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| True Finder volume (not an app window) | ❌ | ✅ | ❌ | ❌ | ✅ | ✅ |
| Nothing to babysit / open | ❌ | ✅ | ❌ | ❌ | ✅ | ✅ |
| No kernel/system extension | ✅ | ❌ | ⚠️ | ⚠️ | ❌ | ✅ |
| No developer mode / USB debugging | ✅ | ❌ (ADB) | ✅ | ✅ | ✅ | ✅ |
| Consumer-grade (notarized, auto-detect, menu bar) | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ |

**Positioning sentence the data supports:** *"Your phone shows up in Finder like a
USB drive. No app to open, no kernel extension, no developer mode, no payment —
free and open source."*

**Underserved angles to own:** (1) the "AFT not working" funnel answered *honestly*;
(2) "no window to babysit / nothing to learn"; (3) "no developer mode, ever"
(counters MacDroid's ADB path); (4) "no kernel extension, no scary permission
prompt"; (5) **trust as a category** — the whole SERP is self-dealing vendor
listicles; a genuinely free, no-upsell, no-telemetry, *polished* OSS tool can own
the honest-broker lane OpenMTP holds on Reddit but no one holds on Google.

---

## 4. Page architecture (resolves the lean-page tension)

The ranking format for these queries is a hybrid product+how-to with FAQ +
comparison table + device sections — **far more than the current lean poster.**
Resolution: **don't bloat the homepage.**

- **`index.html` stays the lean poster** — conversion surface + brand + the
  invisible SEO already shipped (title/meta/OG, JSON-LD, robots/sitemap, one quiet
  AFT-alternative line).
- **SEO-heavy content lives on satellite pages**, each a focused how-to targeting
  one winnable World-B query:
  - `/kindle` — connect a newer Kindle to Mac over USB (MTP)
  - `/garmin` — Garmin not showing in Finder on Mac
  - `/switch` — Nintendo Switch screenshots to Mac over USB
  - `/android-file-transfer-not-working` — troubleshooting template, refreshed per macOS release
  - `/vs` or `/compare` — honest comparison table (the §3 grid)
- **On-page pattern for each satellite** (from the winners): benefit H1 + the
  accurate AFT-orphaned hook → "what you'll need" → numbered steps with a
  screenshot each → short comparison block → FAQ with question strings lifted from
  PAA. (Note: Google has deprecated HowTo/FAQ *rich results* for non-authoritative
  sites, so expect no visual stars/accordion — but the structured numbered DOM and
  Q&A still win featured-snippet selection and long-tail.)

---

## 5. Off-page / distribution

**Announce channels** (post the launch directly): **Show HN** (best single-shot
payoff for a no-Electron, no-telemetry, native Go/Swift OSS tool — post when repo +
README are polished, Tue–Thu AM US, stay in-thread); **r/macapps** (verify self-promo
rules first); **r/opensource, r/degoogle, r/fossdroid** (no-telemetry fit);
**r/swift / r/macosprogramming** (the Galatea NFSv4 / cgo-libmtp architecture earns
goodwill + stars).

**Be-helpful-only channels** (cold promo gets you banned — answer existing
"how do I transfer files" threads, link naturally): r/GooglePixel, r/samsung,
r/Android, r/mac, r/MacOS; and the high-signal niche-pain hubs **MobileRead**,
**forums.garmin.com**, **r/kobo, r/onyx_boox, r/RemarkableTablet, r/Garmin,
r/NintendoSwitch**, **MacRumors Forums**.

**Directories / backlinks** (ranked payoff):

| Site | Payoff | Effort | Notes |
|---|---|---|---|
| **AlternativeTo.net** | Highest | Low | SEO centerpiece. List as alternative to AFT/OpenMTP/MacDroid/Commander One; users filter Free+OSS. **New accounts wait ~1 week to submit — create the account NOW.** |
| **GitHub repo topics** | High | Trivial | `mtp`, `android-file-transfer`, `macos`, `finder`, `nfs`, `libmtp`, `usb`. Do today. |
| **awesome-mac** (~106k★) + niche awesome-lists | High | Low | PR per CONTRIBUTING; "native, no-Electron" framing fits. |
| **Homebrew personal tap** | Med-High | Low | `brew install --cask` works immediately, no notability gate. |
| **Homebrew official cask** | High | Med | **Gated: needs ≥225★ / 90 forks (repo is at 0).** Post-launch unlock; the launch→stars→cask sequence is the whole game. |
| **Product Hunt** | Med | Med-High | A process, not an event; coordinate with HN day. |
| **MacUpdate directory** | Low-Med | Low | Ad-heavy; modest payoff. |
| **Sparkle feed → Latest/update-checkers** | Indirect | — | Passive discovery once shipping Sparkle + cask. |

**Avoid:** Softonic / CNET Download (bundleware reputation — contradicts our
no-telemetry positioning), auto-scraping mirror sites, paid "submit to 100
directories" / paid-backlink schemes, and cold-promoting in device/e-reader subs.

---

## 6. Immediate, time-sensitive actions

1. **Create the AlternativeTo.net account now** (1-week submission delay).
2. **Add GitHub repo topics** (trivial).
3. **Fix the AFT framing** anywhere we imply "discontinued in 2024" → use the
   "quietly orphaned / Quick Share is Windows-only / broken on Apple Silicon" frame.
4. **Refresh [docs/PRE-LAUNCH.md](PRE-LAUNCH.md)** — its pitch predates the v0.4.0
   NFSv4 substrate and is stale; it's the source for HN/Reddit post copy.

---

## Caveats (carried from the research, do not lose)
- **No hard volumes** — all demand signals are ordinal. Validate with a real
  keyword tool before committing content budget.
- **SERP features** (featured snippet / PAA / video carousel) were *inferred* from
  result mix and on-page structure, not observed live. Confirm with an incognito /
  SERP-API pull before locking FAQ question strings to actual PAA phrasing.
- **PAA question strings** were not machine-extractable (JS-gated); the FAQ list is
  inferred from SERP titles + autocomplete.
- Do **not** cite the MacroPlant "~$20" figure (product defunct, price unverifiable).
