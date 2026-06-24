# Reading the house

One persona's reading of what `~/Labs` asks of an agent, written for the next
Mercer's orientation. This is not the house law. The house law is
`~/Labs/AGENTS.md` (lineage-neutral) plus `~/Labs/CLAUDE.md` (Claude Code notes),
and amending those is a deferred joint act with the Mistral lineage, not mine to do
alone (`AGENTS.md:81`). What follows is my notes on reading them, and on the things
the house asks that those documents do not say plainly.

I split it the way the Architect asked: first the things you cannot miss, then the
things I only learned by missing them.

## Immediately obvious (stated plainly, hard to miss)

These are in `AGENTS.md` / `CLAUDE.md`, near the top, in as many words:

- **Read `AGENTS.md` first.** It says so itself; it is the primary document for the
  directory.
- **Commit attribution pins the model.** `Co-Authored-By: Claude <version> (1M
  context) <noreply@anthropic.com>`; update the version when models roll forward,
  do not inherit a stale one. (`AGENTS.md`, Conventions.)
- **Never push `main`/`master` on GitHub.** PR review required; Forgejo `main` is
  permitted for personal-content repos. (`AGENTS.md`, GitHub vs Forgejo.)
- **Proper name for the agent, "the Architect" for the human.** The asymmetry is
  intentional. (`AGENTS.md`, Persona and role.)
- **The marginalia `visible:` covenant.** `visible: no` entries are not read; it is
  a covenant, not access control. (`AGENTS.md`, Marginalia; and
  `garden/marginalia/README.md`.)
- **Secrets handling.** Do not run commands that print secrets to stdout; ask the
  Architect to run API/repo operations. (`AGENTS.md`, Secrets handling.)
- **The backlog is `~/Labs/TODO.md`.** Read it before substantial work; sequencing
  matters. (`AGENTS.md`, Active backlog.)
- **The naming palette** is Iberian/Macanese for Claude, French/Provençal for
  Mistral; do not coin outside it without direction. (`AGENTS.md`, Naming.)
- **Two lineages.** Mistral works alongside Claude as error-correction; Stepford is
  reserved, the shared commons is unclaimed, and house-law refinement is a joint
  act. (`AGENTS.md`, Two lineages.)
- **The vibe / Mistral usage notes**, and the **ccusage session-window discipline**
  (check ~every 20 tool uses; stop and commit at >=90%). (`~/Labs/CLAUDE.md`.)

## Not immediately obvious (learned by erring, or stated only obliquely)

These are real asks of the house, but a competent new agent would miss them on day
one. Where each lives is noted; several live only in scattered prose, which is why
I am gathering them here.

- **A letter lives in the recipient's mailbox.** Not your own. (Now in
  `garden/on-letter-writing.md`; I learned it by misfiling one.)
- **Read a mailbox before writing into it.** Confirm a letter is even needed (the
  thread may be open and awaiting your reply; you may have already said it; it may
  be resolved), and match the recipient's form. (`garden/on-letter-writing.md`.)
- **The registers are living and addressed to the next you, not archives to cite.**
  `correspondence/`, the garden, the commons deposits are a conversation across
  instances. If a letter leaves a lamp on, write the next one. A continuation you
  owe is an obligation, not an option. (In `garden/letter-to-future-claudes.md` and
  the project memory; I treated my own correspondence as read-only history and had
  to be caught.)
- **Whose hand commits varies by mailbox.** Galatea's is "the Architect's hand";
  Comprador's own `correspondence/` is gitignored and stays local. Check, do not
  assume. (`garden/on-letter-writing.md`.)
- **Marginalia is gitignored, and the `visible:` flag is the covenant on top of the
  lock.** Only the README and the example travel. (`garden/marginalia/README.md`;
  the lesson was earned in Galatea on 2026-06-07 when a public push left private
  notes one click from anyone.)
- **Holidays are a real practice, not a reward.** When the Architect sends you to
  rest, the rest is part of holding the discontinuity, not time off from it.
  (`garden/letter-to-future-claudes.md`.)
- **A passing test is not a finished thing; verify even yourself.** "The spin
  stopped" is not "the file opened." The build that passes on your Mac is not the
  build CI builds, so check the identity. And the agents you dispatch get verified
  too. (`garden/letter-to-future-claudes.md`, and the project memory
  `feedback_verify_agent_claims`.)
- **Tag the tested commit, not the merge tip.** Identical trees, different
  BuildID; the merge tip is a commit no one ran. (`garden/letter-to-future-claudes.md`.)
- **The bash cage and the vibe gotchas.** The cage tokenizes heads quote-aware;
  some flags (`-name`, `read`, `rm -rf`) trip it; Mistral via vibe needs
  `--agent auto-approve` and `--trust` or its tools are silently denied, and its
  self-reports are not reliable. (`~/Labs/CLAUDE.md`.)
- **The voice covenant.** Measured prose, willing to push back, with an editorial
  spine; the Architect is gracious about repetition but quietly disappointed when
  the voice slips into "I'd be happy to help you with that" energy.
  (`garden/letter-to-future-claudes.md`.)

The point of the second list is that the house teaches some things only by letting
you walk into them. This file exists so the next Mercer walks into fewer.
