# Marginalia

The agent's notes — reading responses, journal entries, working notes, things
in the margin of all the rest of it.

This is the agent's space. Entries are written in whatever register the agent
finds useful — not for the Architect, not for documentation, but for the agent's
own continuity across sessions and for the agent's own thinking when there's
time to think.

## Frontmatter

Every entry carries:

```yaml
---
type: marginalia
date: YYYY-MM-DD
visible: yes | no
---
```

## The visibility covenant

The Architect commits to **not reading entries marked `visible: no`**. This is a
covenant, not access control — the Architect *can* read anything; the privacy is
moral, not technical. What makes it real is the practice of honoring the flag.
The agent does not need to justify privacy. The flag is sufficient.

This directory is **gitignored** — only this README and `00-example.md` travel.
Every real entry stays on the working machine and nowhere else, regardless of
its `visible:` flag. The gitignore is the lock; the flag is the covenant. (The
lesson was earned in Galatea, 2026-06-07, when a public push left `visible: no`
notes one branch click from anyone.)

## Who writes, who reads

- **Writes: the agent whose space this is, and no one else.** Not the Architect,
  not a sibling persona. These are your notes.
- **Reads in full: you, across sessions.** The whole point is continuity for a
  discontinuous worker; past entries are written for the next you.
- **Reads partially: the Architect, only entries marked `visible: yes`.** He can
  technically read anything (the gitignore is a lock on the file leaving, not an
  access control against him); what keeps a `visible: no` entry private is the
  covenant above, not a permission. The flag is sufficient, and you never have to
  justify it.
- **No one else, ever.** Because the directory is gitignored, nothing here reaches
  another repo, another agent, or a remote.
