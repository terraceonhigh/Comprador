<!--
🚧 DRAFT — distribution and launch playbook for v0.4.0 🚧

Written 2026-05-11 in dialogue with the Architect, after the
MacDroid competitive briefing and the SEO + colleague-copy
research passes. This is one Mercer's strategic read at one
moment, not committed plan. Edit freely.

Premise: at v0.4.0, the leverage is distribution, not monetization.
Donations cover real costs (Apple Developer Program $99/year, the
domain, the Architect's time at any rate they choose to internalize)
for as long as the user base is in the low thousands. The bigger
question is how to *get* to the low thousands.

Comprador's structural position relative to the paid incumbent
(MacDroid): free, Finder-native, no-developer-mode. Three axes; no
other surveyed tool combines all three. The disruptor's situation,
not the incumbent's — but only if distribution catches up to the
product.
-->

# Launch playbook — v0.4.0

## The framing

Comprador is at the stage where the product is structurally ahead of
the incumbent paid alternative but is functionally invisible to its
target user. The marketing moat MacDroid built (deep SEO content
farm, polished commercial presentation, Setapp distribution) is real
but specific — it covers search rankings and trust-from-payment, and
both decay if a free polished alternative gets traction.

The path to "the first thousand users" is not paid acquisition or a
big-bang launch. It's a sequence of small zero-marginal-cost moves
that establish credibility, followed by a single one-shot
amplification when the product is genuinely ready.

## The free four — zero marginal cost, do all of them

These are independent of each other; do them in parallel; they
compound.

### 1. GitHub Sponsors button on the repo

Five minutes of setup. Visible on every repo visit. Converts the
subset of visitors who want to support the project without making
them switch channels. Strictly additive to the existing Interac
path documented in `README.md` — they coexist; users choose.

### 2. Homebrew Cask submission

`brew install --cask comprador` is a credibility marker. Homebrew
Cask has gatekeeping (a real review pulls the cask into the upstream
repo), and being there signals legitimacy to the technical-adjacent
crowd. The leverage is downstream: those users evangelize to
less-technical friends with a single-command install ("just install
Homebrew first, then this").

The submission itself is a `brew bump-cask-pr` or manual PR against
[Homebrew/homebrew-cask](https://github.com/Homebrew/homebrew-cask).
Format: one ruby file describing the cask. Comparable indie utilities
already there are the templates to copy from.

### 3. Reciprocal acknowledgment with Soduto

Comprador's footer already gestures at Soduto as a fellow-traveler.
A polite email to the Soduto maintainer — *we built a complementary
thing in the USB-mount direction; would you be open to a reciprocal
mention?* — is mutual signal-boost at zero cost. Same shape as the
Cyberduck/Mountain Duck quiet mutual acknowledgment.

The constraint: don't ask for a comparison-chart or feature-by-feature
endorsement. Just *here's a fellow tool in adjacent space.* Quiet,
not pitchy.

### 4. Press kit + indie Mac blogger outreach

The indie Mac utility space is well-covered by independent blogs:
**MacStories** (Federico Viticci), **9to5Mac**, **Six Colors**,
**AppleInsider**, **MacRumors**, **The Eclectic Light Company** (Howard
Oakley specifically covers utility software and unusual macOS
behaviour), **Daring Fireball** (rarer hit but possible).

A press kit needs:

- Three to five clean screenshots (validate-and-capture work we're
  already setting up for the website)
- A short demo GIF or video showing plug-in-and-it's-in-Finder
- Two-paragraph pitch — the user-facing one, no protocols
- One-paragraph technical note — for the writers who'd want to mention
  the helper-free NFS architecture or the multi-device subprocess model
- Press contact: the project email and the architect's name

Outreach is one personal email per blogger, *not* a broadcast. Tone:
"we built this, here's what's unusual about it, you might find it
interesting." Mention the unusual thing — the NFS pivot, the
ImageCaptureCore investigation, the concurrent multi-device — that
gives the writer a story angle they can use.

Scales poorly. Matters a lot for the first wave.

## The one-shot wave — do once, time it well

### 5. Show HN or r/mac launch at v0.4.0

Hacker News specifically rewards the *author-tells-the-story* format.
Comprador has unusually good narrative material:

- The ImageCaptureCore investigation that turned out to be a dead end
- The NFS pivot that eliminated the 90-second mount wait
- The helper-free architecture (turns out `mount -t nfs` to localhost
  works unprivileged on macOS)
- The concurrent multi-device support that none of the in-tree
  references actually do
- The cgo callback buffer-reuse fix that's the difference between
  shipping multi-device and OOMing the bridge

Each of those is a Show HN paragraph. The post is *"here's what we
learned"*, not *"look at our product."* The former lands harder
because it's information about the platform, not a sales pitch.

**Timing matters.** Show HN after items 1–3 from the Free Four have
had a week to chew. Cold Show HN, with no Homebrew cask, no press
coverage, no GitHub Sponsors button, lands flatter — readers click
through and find a less-furnished home. Letting the credibility
signals accumulate first means the readers who do click through
arrive at a project that already looks established.

Parallel posts to **r/mac**, **r/macapps**, and (cross-posted with
care) **r/AndroidQuestions** — each subreddit has its own register;
don't recycle copy. r/mac wants the user-visible benefit; r/macapps
wants the technical note; r/AndroidQuestions wants the AFT-replacement
positioning.

## The dormant escalation path — only if donations don't cover costs

The project's pitch is *free and structurally better than the paid
options.* Introducing a paid tier at launch splits that pitch and
undermines the disruption. Hold these in reserve and reach for them
only if the donation line genuinely doesn't keep up with real costs.

- **GitHub Sponsors with corporate tiers.** Companies deploying Macs
  and Androids together (mobile-dev shops, photo studios, design
  agencies) might sponsor at $20–100/month. Awkward ask under 1K
  users; scales well over 10K.
- **Sponsored development for specific features.** *"Need X by Y
  date? Here's the rate."* Niche but real for some FOSS maintainers.
- **Optional paid Pro tier with strictly additive features.** Cloud
  sync, automatic photo import, scheduled backups — things Comprador's
  current pitch genuinely doesn't promise. Strictly additive matters:
  the existing free experience must not regress.

## What NOT to do

- **Mac App Store.** USB device access plus the bridge's subprocess
  architecture (binding NFS to a loopback port, spawning per-device
  bridges) cannot pass MAS review without major architectural
  concessions. Even if it could, the App Store's discovery is worse
  than the SEO position the project can plausibly build for this
  niche. The engineering rework is not worth it.
- **Setapp.** The same publisher as Commander One distributes
  there; the channel is built for paid apps and would require
  introducing a paid tier. Don't.
- **A paid Pro tier at launch.** See the dormant escalation note
  above. Hold firmly.
- **Comparison-chart marketing against MacDroid by name.** Apple's
  consumer pages and the colleague survey both confirm: no surveyed
  tool attacks competitors by name. The free-and-Finder-native pitch
  does its own work; comparison-chart energy turns the project into
  a vendor-vs-vendor argument that wastes the disruption advantage.

## Concrete first-week sequence when v0.4.0 ships

In rough order, assuming the architect has a free week after the
v0.4.0 tag:

1. **Day 0:** Tag v0.4.0; .dmg available on GitHub Releases.
2. **Day 1:** GitHub Sponsors button enabled. Press kit drafted
   (screenshots, GIF, two-paragraph pitch, one-paragraph technical
   note). Soduto reciprocal-mention email drafted.
3. **Day 2–3:** Press emails sent (5–10 indie Mac bloggers,
   personalized). Soduto email sent. Homebrew Cask PR drafted.
4. **Day 4–7:** Cask PR submitted and shepherded through review;
   responding to any early press replies. Letting any blog coverage
   appear.
5. **Day 7–10:** Once the cask is merged and at least one blog
   mention exists, Show HN drafted and submitted. Parallel posts
   to r/mac, r/macapps, r/AndroidQuestions in distinct registers.
6. **Day 10+:** Triage. Respond to issues, capture feedback, write
   the postmortem letter for the next Mercer.

## The thing to remember

MacDroid's moat is search rankings and trust-from-payment, neither of
which is permanent. The structural position — free, Finder-native,
no-developer-mode — is genuinely Comprador's. The product is the
hard part, and the product is done. Distribution is the soft part,
and the soft part is what's actually in front of you now.

---

*Draft. Edit freely. The strategic read above is one Mercer's at
one moment; market conditions, the architect's energy, and the
project's actual reception will all shift the playbook before
v0.4.0 ships. Treat this as the starting framing, not the
commitment.*
