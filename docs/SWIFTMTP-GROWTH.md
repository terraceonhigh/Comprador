# How SwiftMTP grew — a cited teardown

> Research compiled 2026-06-21 by three parallel agents (local archive, repo
> metrics, distribution/reception) plus direct re-verification of the
> load-bearing URLs. **Every substantial claim below carries a live URL that
> proves it.** Search was used only to discover pages; each claim was confirmed
> by fetching the page and quoting it. Numbers are from fetched pages, never
> estimated. Negative findings (where SwiftMTP is absent) were confirmed by
> fetching the actual list/index, not inferred from a search snippet.

SwiftMTP is the closest comparable to Comprador: a native macOS MTP/Android
file-transfer app, also descended from OpenMTP, also pitching "no ADB / no USB
debugging." Studying how it grew tells us which lanes are taken and which are
open.

## What it is, and its traction

- Repo: <https://github.com/Neighbor-Z/SwiftMTP>, GPL-2.0, by user `Neighbor-Z`.
- **380 stars, 7 forks, 2 subscribers (true watchers), 2 open issues**, created
  `2026-04-07`, last push `2026-06-20`. Source: GitHub API,
  <https://api.github.com/repos/Neighbor-Z/SwiftMTP> (re-verified 2026-06-21).
- So: **380 stars in ~2.5 months**, against only **2 real watchers** and 7 forks.
  That ratio is a spike-driven bookmark curve (people star to find it again /
  download it), not a contributor community. The niche has real pull; the
  engagement is shallow.
- Actively maintained, not abandoned: 11 tags through **v1.2.2 (2026-06-07)**.
  Source: <https://api.github.com/repos/Neighbor-Z/SwiftMTP/tags> and
  <https://github.com/Neighbor-Z/SwiftMTP/releases>.

## How they actually reached an audience

The reach is **almost entirely Chinese-language and author-seeded.** It splits
into one genuine earned placement, two downstream blog reviews, and a set of
channels the author published himself.

**Earned (third-party) — the one real signal:**
- **sspai / 少数派 editorial pick.** <https://sspai.com/post/109135> ("派评 |
  近期值得关注的 App", 04/27). Names SwiftMTP and its author as a 少数派
  contributor: *"…不妨试试由少数派作者 [@Neighbor_Z] 带来的这款 SwiftMTP。"*
  This is the highest-credibility placement found, and the maintainer being an
  sspai author is *why* it got the pick.
- Two Chinese software-blog reviews that plausibly followed the sspai pick:
  **iplaysoft.com** (<https://www.iplaysoft.com/p/swiftmtp>, 2026-05-05, "比
  OpenMTP 更轻快") and **nihendiao.com**
  (<https://www.nihendiao.com/13322.html>, 2026-05-07). Both fetched and
  confirmed to be about SwiftMTP. (No reliable traction numbers on either;
  not claimed.)

**Author-seeded (first-party) distribution:**
- **dev.to self-announcement** (the only English-language reach):
  <https://dev.to/neighbor-z/android-file-transfer-on-mac-is-broken-so-i-built-swiftmtp-5eae>
  ("Android File Transfer on Mac is Broken, So I Built SwiftMTP", Apr 22). 0
  visible comments.
- **Own Homebrew tap** (not the official cask):
  <https://github.com/Neighbor-Z/homebrew-swiftmtp> — `brew tap neighbor-z/swiftmtp`.
- **SourceForge mirror:** <https://sourceforge.net/projects/swiftmtp/> ("4
  downloads this week", last update 2026-05-27).
- **Own website:** <https://neighbor-z.github.io/swiftmtp-website/> (no "as
  featured on" badges).

## Where they are NOT (verified absences — this is the opening)

Each confirmed by fetching the actual index/search, not a snippet:
- **Hacker News: zero.** <https://hn.algolia.com/api/v1/search?query=SwiftMTP>
  returns no SwiftMTP submission.
- **Not in `jaywcjlove/awesome-mac`** (raw README fetched, absent) nor
  **`awesome-swift-macos-apps`** (raw README + rendered mirror fetched, absent).
- **No V2EX thread** about it (the one candidate thread fetched; SwiftMTP not
  present in title or 33 replies).
- **No Product Hunt, no AlternativeTo, no Reddit thread** surfaced or confirmed.

## Positioning levers they pull (from the repo)

- **Lightweight-native vs Electron bloat.** `Comparison.md` attacks OpenMTP (its
  own ancestor) on size: *"🔴 ~360MB"* vs SwiftMTP *"🟢 < 20MB"*, and *"🔴
  Web-based"* vs *"🟢 Native Swift"*. Other competitors are anonymized ("A/B/C
  from MAS") — they name OpenMTP but avoid fights with App Store apps. Source:
  local `references/SwiftMTP/Comparison.md`; README mirror
  <https://raw.githubusercontent.com/Neighbor-Z/SwiftMTP/main/README.md>.
- **Bilingual, China-first.** A full Chinese `README_zh.md`
  (<https://github.com/Neighbor-Z/SwiftMTP/blob/main/README_zh.md>); every commit
  and tag is `+0800`; the zh comparison is sharper (calls out competitors that
  garble CJK filenames). UI ships `Localizable.xcstrings` (EN/zh/JA/ES).
- **AI repositioning as the 2026 differentiator.** The README now leads with
  "supercharged by AI" (Natural Language Search, Device Info Analysis),
  introduced around v1.2.0 — though the GitHub "About" still tags some of it
  "Upcoming." Source: repo page + API description field.
- **Anti-App-Store, friction-for-cost.** Unsigned `.dmg` via GitHub Releases;
  the README declines Apple's "$99 annually" and tells users to `xattr -rd
  com.apple.quarantine`. The opposite of Comprador's notarized choice.
- **Funding:** Buy Me a Coffee only (`.github/FUNDING.yml`); GitHub Sponsors /
  Patreon / Open Collective all left blank. A tip jar, not monetization.

## What this means for Comprador

1. **The English / Western channels are wide open.** SwiftMTP got 380 stars
   essentially from *one* sspai editorial pick plus self-seeding, with **no HN,
   no Reddit, no awesome-mac, no AlternativeTo.** Those are exactly the channels
   our [SEO-PLAN.md](SEO-PLAN.md) off-page section targets. We are not fighting
   an incumbent for them; they are empty.
2. **Their growth model is reproducible and so is its ceiling.** Author-seed +
   one editorial pick = a star spike with shallow engagement (2 watchers). A
   real Show HN + awesome-mac + AlternativeTo presence could plausibly clear
   that bar in the English market they never entered.
3. **We beat them on the trust axis they conceded.** They ship unsigned and tell
   users to strip quarantine; we are signed and notarized. That is a concrete,
   honest differentiator for the same audience.
4. **A bilingual (EN + Chinese) README is a lever they have and we don't.** The
   Android-on-Mac audience in China is real (it carried SwiftMTP). Worth
   considering, not urgent.
5. **Don't copy the AI bolt-on.** Their AI features are "Upcoming" headline
   repositioning; one review already complained it reads "AI 味太浓" (too
   AI-flavored). Our differentiator is the native Finder mount + streaming, which
   is shipped and demonstrable.

## Reproducibility appendix

All URLs accessed 2026-06-21. Load status from the agent that fetched them;
the four marked **(re-verified)** were fetched again directly during synthesis.

| URL | Status |
|---|---|
| https://github.com/Neighbor-Z/SwiftMTP | loaded |
| https://api.github.com/repos/Neighbor-Z/SwiftMTP | loaded **(re-verified)** — 380★/7/2 |
| https://api.github.com/repos/Neighbor-Z/SwiftMTP/tags | loaded — 11 tags |
| https://github.com/Neighbor-Z/SwiftMTP/releases | loaded |
| https://raw.githubusercontent.com/Neighbor-Z/SwiftMTP/main/README.md | loaded |
| https://github.com/Neighbor-Z/SwiftMTP/blob/main/README_zh.md | loaded |
| https://github.com/Neighbor-Z/SwiftMTP/blob/main/.github/FUNDING.yml | loaded |
| https://neighbor-z.github.io/swiftmtp-website/ | loaded |
| https://sspai.com/post/109135 | loaded **(re-verified)** — SwiftMTP + sspai-author confirmed |
| https://www.iplaysoft.com/p/swiftmtp | loaded — confirmed |
| https://www.nihendiao.com/13322.html | loaded — confirmed (no reliable traction count) |
| https://dev.to/neighbor-z/android-file-transfer-on-mac-is-broken-so-i-built-swiftmtp-5eae | loaded **(re-verified)** — author self-announcement |
| https://github.com/Neighbor-Z/homebrew-swiftmtp | loaded |
| https://sourceforge.net/projects/swiftmtp/ | loaded |
| https://hn.algolia.com/api/v1/search?query=SwiftMTP | loaded — zero SwiftMTP hits |
| https://raw.githubusercontent.com/jaywcjlove/awesome-mac/master/README.md | loaded — absent |
| https://raw.githubusercontent.com/jaywcjlove/awesome-swift-macos-apps/main/README.md | loaded — absent |
| https://www.v2ex.com/t/930732 | loaded — absent |

Local sources (read-only, under `/Users/terrace/Labs/references/SwiftMTP/`):
`README.md`, `README_zh.md`, `Comparison.md`, `.github/FUNDING.yml`, and git
history (`git log`, `git tag`; first commit 2026-04-07, last 2026-05-08 in the
local snapshot).

**Not verified / out of scope:** total release-asset download counts (GitHub
does not expose them), comment/traction numbers on the Chinese blog reviews, and
any channel listed above under "verified absences."
