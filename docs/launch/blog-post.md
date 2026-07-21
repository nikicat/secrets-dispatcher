# Your AI coding agent can read every secret in your keyring

*On Linux, any process you run — including your AI coding agent — can silently
read every unlocked credential in your keyring. I couldn't see when it happened,
so I built a tool that makes it visible — and I've run it in front of my own
keyring for about five months now. Here's what it's actually for, what it isn't,
and what living with it taught me about which part matters.*

---

## The thing that bugged me

I run coding agents like everyone else now — Claude Code, Cursor, aider. They
execute shell commands, read files, install packages, make commits. At some
point it clicked that these agents run **as me**. And on Linux, "as me" means
they can call the freedesktop [Secret Service](https://specifications.freedesktop.org/secret-service/)
over D-Bus and read anything in my unlocked keyring — GitHub tokens, cloud
keys, database passwords — with a one-liner:

```bash
secret-tool lookup service github
```

No prompt. No log. I would never know it happened.

Same story for signing. `git commit -S` hands whatever it's given to GPG, which
signs it. So an agent — or a compromised dependency, or a CI step — can produce
a commit cryptographically signed by *me*, with content I never looked at.

## Why this is the default

The Secret Service model is **session-level**. You unlock the keyring once at
login, and from then on every process running as your user is trusted equally.
There is no notion of "Firefox may read the GitHub cookie, but this random
script may not." The keyring authorizes at the *session* boundary, not the
*application* boundary.

That is a meaningful gap compared to other platforms — macOS Keychain has
per-application ACLs, and mobile OSes sandbox apps from each other — but it was
a perfectly reasonable design for the Linux desktop of 2008. It's a more
uncomfortable one in an era where the programs you run write and execute their
own code on your behalf.

## This isn't hypothetical

GitGuardian's [State of Secrets Sprawl 2026](https://www.gitguardian.com/state-of-secrets-sprawl-report-2026)
found AI-assisted commits leak credentials at roughly **twice** the rate of
human-only ones. Sophos reported legitimate AI coding agents tripping
attacker-detection rules, with credential access the single largest category of
flagged activity — agents enumerating and decrypting the OS credential store.
And 2025 brought self-replicating credential-stealing worms to npm (Shai-Hulud)
and malicious editor extensions harvesting API tokens. An agent doesn't have to
be *malicious* to leak a secret — it just has to be careless with one it could
read and you never saw it touch.

## What I actually want

Not to lock agents out — I want them to work. I want a **decision point and a
record**. When something reads a secret, show me:

- **which process** — the whole chain, `claude-code → node → secret-tool`, not
  just "dbus-daemon asked";
- **which secret**;
- and let me **approve, deny, or "always allow this one."**

Plus an audit log so I can look back and see what touched what.

## What I built

**secrets-dispatcher** is a proxy that speaks the Secret Service protocol on
both sides. It registers as the keyring, so applications talk to it unchanged;
behind it sits your real keyring (gnome-keyring, KeePassXC, gopass). Every
request pauses for approval — via a web UI, a desktop notification, or the CLI.
Trust rules let you auto-approve known-safe patterns (your browser, tools from a
given project directory) and prompt for everything else. The same gate wraps
`git commit -S`, so you see the repo, message, and changed files before your key
ever signs.

> **[demo GIF here — app requests a secret → approval prompt → access granted]**

## What five months of it has actually been like

Two honest observations from living with it.

**What I *touch* most is the commit gate — but it's not why I keep it.** Day to
day, the prompts I actually click are `git commit -S` confirmations: an agent
wants to commit, and I get a look at the repo, message, and changed files before
my key signs. That's genuinely useful — it doubles as a "review what the agent is
about to commit" checkpoint — but I'll be first to say it's replaceable (a git
`pre-commit` hook or an agent PreToolUse hook gets you most of the way), and it
only works if you sign your commits at all.

**What I'd actually miss is the part I never see anymore.** Every tool that
legitimately needs a secret got an auto-approve rule in the first week, and after
that the secret prompts went quiet. That silence *is* the value: it's not that I
approve reads all day — it's that my keyring stopped being wide open to every
script on the box, and the one time something unexpected reaches for a credential,
I'll see it. The gate earned its keep by disappearing. That's the hard thing to
show in a demo — you can't screenshot the absence of a worry — but it's the reason
it's still installed five months on.

## The honest part

This is **not a sandbox** and it does **not** add a privilege boundary. It runs
as the same user. A determined malicious process on your machine can still do
plenty of other things — read files, `ptrace` siblings, and so on. What
secrets-dispatcher does is close one specific, silent, zero-friction path — the
Secret Service API — and give you a log and a choice at it.

Think of it as a **smoke detector for your keyring, not a vault door.** If you
need true isolation, run the agent in a container or under a separate user. This
is for the (large) middle ground where you want the convenience of a shared
desktop keyring without the blindness that comes with it.

Two limits worth stating plainly, because a sharp reader will raise them:

- **It doesn't see your `.env` files** — or `~/.aws/credentials`, or any secret an
  agent reads straight off disk, which is honestly the *most common* way agents grab
  credentials today. secrets-dispatcher closes the keyring / D-Bus path, which the
  file-scanning tools ignore. They're complementary; for on-disk secrets, sandbox.
- **The GNOME keyring folks would call same-user protection ["security theater."](https://wiki.gnome.org/Projects/GnomeKeyring/SecurityPhilosophy)**
  They're right that it can't *stop* a determined malicious process — but "I can't
  stop malware" and "I can show you, and gate, what your everyday agents and apps
  read" are different claims, and only the second one is being made here.

### "Why not just…?"

- **…sandbox the agent (container / bubblewrap)?** Great if you do — and this is
  complementary, not competing. But most people run agents directly on the host
  for convenience, and in that setup this is your only per-application control.
- **…use KWallet's or KeePassXC's per-app prompt?** If you're on KDE or use
  KeePassXC, you already have a version of this — and if it covers you, great. This
  differs by being **backend-agnostic** (it proxies *any* Secret Service keyring),
  showing the **full process chain** rather than a single PID, keeping a **persistent
  audit log**, and extending the same gate to **git commit signing**.
- **…not store secrets in the keyring?** Then every libsecret-using app stops
  finding them. The keyring is where they look; opting out breaks the ecosystem.
- **…rely on AppArmor / SELinux / Flatpak?** Those confine *packaged* apps by
  profile. They don't give you a per-request approval + audit view across
  arbitrary processes, and an agent you `curl | sh` isn't under a profile at all.

## Try it — 30 seconds, fully reversible

```bash
secrets-dispatcher try
```

It detects your keyring, slips in front of it, and prints a local URL. Trigger a
lookup and watch the prompt appear:

```bash
secret-tool store --label=demo service demo
secret-tool lookup service demo   # → approval prompt
```

**Ctrl-C puts everything back exactly.** Nothing permanent, no root, your config
is never touched.

## Where this goes — you tell me

I built this because it scratched my own itch, and I honestly don't yet know
whether it's a tool a lot of people want or a niche of one. So this is a real
question, not a rhetorical one:

**Would you actually run this? If not, what's the blocker?**

Repo, issues, and a longer README: **[github.com/nikicat/secrets-dispatcher](https://github.com/nikicat/secrets-dispatcher)**. I read everything.

---

<!-- DRAFT NOTES (delete before publishing):
- Insert the animated demo GIF where marked — it carries this whole post.
- The "Why not just X" section may be sharpened by the landscape-scan findings
  (real incidents of agents/extensions exfiltrating secrets make a strong opener).
- Consider a one-line lede incident if the research surfaces a concrete case.
- Publish on: personal blog first (canonical), then dev.to / lobste.rs / HN.
-->
