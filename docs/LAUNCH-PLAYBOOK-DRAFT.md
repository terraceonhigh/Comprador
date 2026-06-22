<!--
🚧 DRAFT — distribution and launch playbook for v0.4.0 🚧

Written 2026-05-11 in dialogue with the Architect, after the
MacDroid competitive briefing and the SEO + colleague-copy
research passes; refreshed 2026-06-21 after v0.4.0 shipped and
the dedicated SEO/off-page plan landed. This is one Mercer's
strategic read, not committed plan. Edit freely.

This playbook is the *strategy/sequencing* layer. The keyword
strategy, winnable long-tail clusters, positioning table, and the
full off-page/directory breakdown now live in
[docs/SEO-PLAN.md](SEO-PLAN.md) — cross-referenced below rather
than duplicated.

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

> **Status (2026-06-21):** v0.4.0 has **shipped** — a notarized,
> signed, stapled `.dmg` is live on GitHub Releases (commit
> `fbbb8b02`) and the website is **live** at
> <https://terraceonhigh.github.io/Comprador/>. The product is
> launch-ready; the work in front of us is the distribution
> sequence below, no longer a pre-tag blocker list.

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

### 2. Homebrew — personal tap now, official cask later

`brew install --cask comprador` is a credibility marker, but the
**official** Homebrew Cask is gated: it needs roughly **≥225 stars /
90 forks**, and the repo is currently at **0 stars**. So the official
cask is a *post-launch unlock*, not a Day-1 move — the
launch → stars → cask sequence is the whole game (see
[SEO-PLAN.md §5](SEO-PLAN.md)).

What *is* available today, with no notability gate, is a **personal
Homebrew tap**: `brew install --cask` works immediately against our
own tap. Stand that up now for the technical-adjacent crowd; their
single-command install ("just install Homebrew first, then this") is
what they evangelize to less-technical friends. File the official
homebrew-cask PR once the star threshold is cleared.

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
  the substrate (**Galatea**, an in-house userspace **NFSv4** server
  over loopback; the WebDAV layer, the `willscott/go-nfs` NFSv3 path,
  and the privileged root helper were all removed in v0.4.0) or the
  multi-device subprocess model
- Press contact: the project email and the architect's name

Outreach is one personal email per blogger, *not* a broadcast. Tone:
"we built this, here's what's unusual about it, you might find it
interesting." Mention the unusual thing — the NFS pivot, the
ImageCaptureCore investigation, the concurrent multi-device — that
gives the writer a story angle they can use.

**Get the Android File Transfer framing right in every piece of
copy.** Do *not* write "discontinued in May 2024" — that's vendor SEO
myth, not fact. The accurate, more-damning line: Google **quietly
orphaned** it (the download link was removed in early 2024 and the
page now serves a Windows-only app); it's **unmaintained and broken
on Apple Silicon / recent macOS**. Full framing in
[SEO-PLAN.md §3](SEO-PLAN.md).

Scales poorly. Matters a lot for the first wave.

## The one-shot wave — do once, time it well

### 5. Show HN or r/mac launch at v0.4.0

Hacker News specifically rewards the *author-tells-the-story* format.
Comprador has unusually good narrative material:

- The ImageCaptureCore investigation that turned out to be a dead end
- The NFS pivot that eliminated the 90-second WebDAV mount wait — and
  the second pivot to **Galatea**, an in-house userspace **NFSv4**
  server, whose multi-minute read tolerance let us delete the entire
  NFSv3 prefetch/JUKEBOX workaround — and means you can now stream a
  video straight off the phone (play it, scrub it) where NFSv3 timed out
- The helper-free architecture (turns out `mount -t nfs` to localhost
  works unprivileged on macOS, so the root helper was vestigial and
  is gone)
- The concurrent multi-device support that none of the in-tree
  references actually do
- The cgo callback buffer-reuse fix that's the difference between
  shipping multi-device and OOMing the bridge

Each of those is a Show HN paragraph. The post is *"here's what we
learned"*, not *"look at our product."* The former lands harder
because it's information about the platform, not a sales pitch.

**Timing matters.** Show HN after the Free Four have had a week to
chew (and after the AlternativeTo.net account has cleared its ~1-week
submission delay — start that account *now*; see
[SEO-PLAN.md §6](SEO-PLAN.md)). Cold Show HN, with no personal
Homebrew tap, no press coverage, no GitHub Sponsors button, lands
flatter — readers click through and find a less-furnished home.
Letting the credibility signals accumulate first means the readers
who do click through arrive at a project that already looks
established. Best window is Tue–Thu morning US time; stay in-thread.

**Channel discipline is load-bearing — there are two kinds of channel
and they are not interchangeable.** The full list lives in
[SEO-PLAN.md §5](SEO-PLAN.md); the rule that governs this playbook:

- **Announce channels** welcome a launch post: **Show HN**,
  **r/macapps** (check self-promo rules first), **r/opensource /
  r/degoogle / r/fossdroid** (the no-telemetry fit), and **r/swift /
  r/macosprogramming** (the Galatea NFSv4 / cgo-libmtp architecture
  earns goodwill and stars). Tailor the register per channel — don't
  recycle copy.
- **Be-helpful-only channels** will **ban cold promotion.** The
  device, e-reader, and Garmin communities — **MobileRead**,
  **forums.garmin.com**, **r/kobo, r/onyx_boox, r/Garmin,
  r/NintendoSwitch**, the Android/Pixel/Samsung subs, **r/mac**, and
  the **MacRumors Forums** — are not launch surfaces. There you answer
  existing "how do I transfer files / why won't my device show up"
  threads and link Comprador only when it genuinely answers the
  question. This is a slow, ongoing helpful presence, not a Day-0
  blast.

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

## Prerequisites before the announcement wave

v0.4.0 has tagged and shipped, so these no longer block the *release*
— but they remain prerequisites for the *announcement wave*, since
both shape the first impression a press/HN reader forms. Confirm
their current state against [TODO.md "Pre-launch UX items"](../TODO.md)
and [docs/PRE-LAUNCH.md](PRE-LAUNCH.md) before Day 0 — **flag: I have
not verified whether either shipped in v0.4.0; the CHANGELOG doesn't
mention them explicitly.**

- **User-facing disclosure of the `ptpcamerad` kill.** Comprador
  temporarily preempts other USB-camera-reading apps (Image Capture,
  Photos auto-import). Honest disclosure in three places: the welcome
  window, the website FAQ, the README's "What works" section. The
  Apple-conventions and colleague-copy surveys both confirmed our
  competitors imply *fully automatic, no friction*; surfacing this
  small friction up front beats discovery-by-bug-report after the
  press wave lands.
- **Update detector with Homebrew-aware suppression.** The playbook
  ships across two distribution channels at once (direct .dmg and the
  Homebrew tap/cask). Sparkle handles the direct path; Homebrew-
  installed copies need the Sparkle prompt suppressed so the in-app
  update flow doesn't bypass `brew upgrade --cask comprador`. Without
  this, Homebrew users get a confusing double-update experience and
  may publicly complain — bad for the credibility marker the cask was
  supposed to be.

Confirm both are in the shipped build, then proceed to Day 0.

## Concrete first-week sequence when v0.4.0 ships

In rough order, assuming the architect has a free week to run the
announcement wave (the v0.4.0 tag and DMG are already done):

1. **Day -7 (now):** Create the **AlternativeTo.net** account — it
   has a ~1-week submission delay, so it gates the whole sequence.
   Add **GitHub repo topics** (`mtp`, `android-file-transfer`,
   `macos`, `finder`, `nfs`, `libmtp`, `usb`) — trivial, do today.
   Both per [SEO-PLAN.md §6](SEO-PLAN.md).
2. **Day 0:** v0.4.0 already live (notarized DMG on GitHub Releases,
   website live). GitHub Sponsors button enabled. Press kit drafted
   (screenshots, GIF, two-paragraph pitch, one-paragraph technical
   note). Soduto reciprocal-mention email drafted.
3. **Day 1–3:** Press emails sent (5–10 indie Mac bloggers,
   personalized). Soduto email sent. **Personal Homebrew tap** stood
   up (no notability gate). AlternativeTo.net listing submitted once
   the account clears.
4. **Day 4–7:** Responding to early press replies; letting any blog
   coverage appear. PR to **awesome-mac** + niche awesome-lists.
5. **Day 7–10:** With the tap live and at least one blog mention,
   **Show HN** drafted and submitted (Tue–Thu AM US). Parallel
   announce-channel posts (**r/macapps**, **r/opensource /
   r/degoogle / r/fossdroid**, **r/swift / r/macosprogramming**) in
   distinct registers. **Not** the device/e-reader subs — those are
   be-helpful-only (see the channel-discipline note above).
6. **Day 10+:** Triage. Respond to issues, capture feedback. Begin
   the slow be-helpful presence in MobileRead / Garmin / device subs.
   The **official** homebrew-cask PR unlocks once the repo clears the
   ~225-star gate. Write the postmortem letter for the next Mercer.

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
