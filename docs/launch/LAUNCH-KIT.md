# Launch kit — secrets-dispatcher

Drafts for a validation launch. **The goal is a go/no-go decision, not stars.**
Every post ends with the same direct question, because the *answers* are the
deliverable.

> **Landscape scan is in — three findings that shape everything below:**
> 1. **Reframe away from "secure your keyring."** The keyring-prompt primitive is
>    prior art (KWallet, KeePassXC) and GNOME's own maintainers call same-user per-app
>    protection ["security theater"](https://wiki.gnome.org/Projects/GnomeKeyring/SecurityPhilosophy).
>    Lead instead with what has **no competitor**: per-commit **GPG signing approval**
>    and the **backend-agnostic audit log + process-chain visibility**. Keep the AI angle
>    (the problem is hot and well-timed), but claim *visibility + control*, never *security boundary*.
> 2. **`.env` is addressable — via direnv (lead with this).** Agents commonly read plaintext
>    `.env` off disk (which the keyring gate doesn't see). The fix: a keyring lookup in `.envrc`
>    — `export API_KEY=$(secret-tool lookup …)` — so there's no plaintext on disk and every fetch
>    is gated + logged. This bridges to the hottest agent-secrets discourse. (Honest: once
>    exported it's an env var — not a boundary against a process already in that shell.)
> 3. **Ammo for openers** (cited): GitGuardian 2026 — AI-assisted commits leak secrets
>    ~2× the human rate; Sophos — AI agents decrypting OS credential stores (credential
>    access = largest flagged category); 2025 worms Shai-Hulud (npm) / GlassWorm (VS Code).

---

## 0. The plan (read first)

**Wedge (creator's 5-mo use + scan):** "your AI coding agent runs as you — see and
gate what it reads." **Secret-access gating is the core value** — framed as
*visibility + control for careless agents*, never a security boundary (concede the
"security theater" critique up front). It's valuable on its own, independent of
signing. The **commit / GPG-signing gate is the most-frequent touch and the best
live demo, but it's replaceable** (agent PreToolUse / git hooks) and needs signing
setup — so it's a strong *also-does* and proof-point, **not the headline**.
Differentiate from KWallet/KeePassXC on backend-agnostic + process-chain + audit +
agent-agnostic. Say plainly it doesn't cover `.env`/on-disk secrets. Remote-proxy
stays "also does."

**Sequencing — warm up before the one-shot:**
1. **Days 1–3, soft launch** → Linux/GNOME + agent pockets where self-posts are welcome
   (r/gnome, an AI-dev Discord's public `#show-your-project`, your Mastodon) *and* answer the
   two live existing threads (KeePassXC #9024; the r/ClaudeAI `.env` thread). No cold DMs; don't
   ad-board other projects' issue boards. Shake out compat bugs, sharpen the pitch.
2. **Day ~7, main launch** → publish the [blog post](blog-post.md), then Show HN + Lobsters
   the *same morning* (~8–10am US Eastern, weekday) + an r/archlinux or r/gnome `[OC]`. Treat
   **HN + blog as a *concept* play** (Mac-heavy, most can't run it) — measure discourse, not
   installs. Be present all day to answer. (See §0b for why.)

**The one line that matters most**, appended to every post:
> **Would you actually run this? If not, what's the blocker?**

**Kill / continue criteria (write these down now, decide *after*):**
- **Continue** if, within ~2 weeks: ≥1 "I installed it and it caught something / I'll
  keep using it," ≥5 substantive "I have this problem too" reactions, and a
  handful of unsolicited issues or questions. Engaged-but-small beats a spike of
  drive-by stars.
- **Freeze** if even the sharpest pitch to the most-affected audience gets shrugs
  ("neat, but I'd just not give agents keyring access"). *The content of the
  objections is the signal* — "I'd never proxy my keyring" (abandon), "works but
  needs KDE" (niche-continue), and silence (freeze) point to very different calls.

**Do not add telemetry.** For a keyring/privacy tool it would poison trust with
exactly this audience. Live with coarse signal: release download counts, AUR
votes, stars — and mainly the comments.

---

## 0b. Reach reality — applicability & retargeting

**The audience is capped by applicability.** Linux-only (freedesktop Secret Service); the
smooth one-command setup works on **GNOME (gnome-keyring)** or **gopass-secret-service** —
KDE/KWallet/KeePassXC/other desktops need manual setup, and Mac/Windows can't run it at all.
So the addressable slice is **Linux ∩ (gnome-keyring | gopass-secret-service) ∩ runs-agents ∩
security-conscious** — narrow, with a permanent OS ceiling. Own this in every post so nobody
feels baited.

**Reweight reach → hit-rate.** Most of HN's reach is non-addressable (Mac users). Treat **HN +
the blog as a *concept* play** — the *problem* is cross-OS (a Mac dev's agent reads their
Keychain/`.env` too), so the writeup travels even if the tool doesn't; measure discourse, not
installs. Put the *validation* posts where the addressable users actually are:

| Venue | Why |
|---|---|
| **r/gnome** | GNOME users can run the one-command setup — the sweet spot |
| **r/archlinux** | power users; AUR package exists |
| **aider / r/LocalLLaMA** | Linux-heavy agent users who feel the `.env`/keyring pain |
| **r/selfhosted**, NixOS/Arch forums | Linux, security-conscious tinkerers |
| **r/linux `[OC]`** | broad, but budget for a "not for me" fraction (KDE/Sway, non-agent) |

**Lead angle everywhere:** the **direnv technique** — *"how I keep secrets out of `.env` on
Linux"* — not "gate your keyring." It's useful even to non-installers and sidesteps the
"keyring theater" critique.

**Expectation reset:** success = an *engaged* response from that concentrated GNOME-Linux-agent
niche; near-silence *from that niche* is a clean freeze signal. Broad numbers aren't the bar.

### Retargeted post drafts (direnv angle, scope owned)

**r/gnome / r/archlinux `[OC]`:**
> **[OC] A per-app approval + audit gate for the GNOME keyring — and a way to keep secrets out of `.env`**
>
> gnome-keyring is all-or-nothing: locked (nothing readable) or unlocked (every process you run
> — coding agents included — reads every secret over the Secret Service, silently, no log). I
> wanted a middle state, so I built a proxy that sits in front of the keyring and prompts per-app
> (auto-approve the tools you trust, get asked about the rest), with an audit log of who read what.
>
> The bit I use most: with direnv, `export API_KEY=$(secret-tool lookup …)` in `.envrc` keeps the
> secret in the keyring, never in a plaintext `.env` — so an agent can't `cat .env`, and each fetch
> is gated + logged. ~5 months in.
>
> MIT/open source — please read the code before trusting it with your keyring; that's the point.
> Reversible in one command (Ctrl-C restores everything), same-user (visibility + control, not a
> sandbox). Verified on GNOME/gnome-keyring + gopass-secret-service; KDE/others need manual setup.
> **Would you run this? If not, what's the blocker?** <repo>

**Show HN opener** — rework §1's first line to lead with the direnv/`.env` hook and state the
scope: *"On Linux, your coding agent runs as you — it can read every secret in your keyring
(`secret-tool lookup …`) and any plaintext `.env`, silently. Here's a setup I've run ~5 months
that closes the keyring path and keeps secrets out of `.env` (direnv + a gating proxy)…"* — then
keep §1's honest-scope + the ask, and add "Linux-only; verified on GNOME/gopass-secret-service."

---

## 1. Hacker News — "Show HN"

Submit the **repo URL** (or the blog post) as the Show HN, then immediately add
the comment below. HN rewards concreteness and honesty; no adjectives, no "revolutionary."

**Title options** (pick one; ≤ 80 chars, no hype):
- `Show HN: See which processes read your Linux keyring secrets (and approve each)` ← recommended
- `Show HN: Per-app approval and audit log for the Linux keyring (Secret Service)`
- `Show HN: Approve what your AI agent reads from your keyring and signs as you`

**First comment:**

> I've run this in front of my own keyring for ~5 months, so this is a
> scratch-my-own-itch Show HN. The itch: coding agents (Claude Code, Cursor, aider)
> run *as me*, which on Linux means they can call the Secret Service over D-Bus and
> read anything in my unlocked keyring (`secret-tool lookup service github`) with no
> prompt and no log. Usually not malice — just an agent taking a wrong path to a
> task — but I never saw it happen.
>
> secrets-dispatcher is a proxy that speaks the Secret Service protocol on both
> sides: it registers as the keyring so apps talk to it unchanged, and behind it
> sits your real keyring (gnome-keyring/KeePassXC/gopass). Each access pauses for
> approval (web UI, notification, or CLI), shows the full process chain
> (`claude-code → node → secret-tool`, not just "dbus-daemon"), and gets logged. You
> auto-approve the tools you trust; the rest has to ask.
>
> The honest bit after five months: what I *click* most is actually the commit gate —
> it also wraps `git commit -S`, so I see the repo/message/diff before my key signs,
> which doubles as a "review what the agent is committing" checkpoint. But that part
> is replaceable (git/agent hooks) and needs signing setup. The part I'd actually
> miss is the secret gating that faded into rules — the assurance my keyring isn't
> wide open to every script on the box. It's **not a sandbox** and adds no privilege
> boundary (same user); the GNOME folks would rightly call same-user protection
> "security theater" against a determined attacker — fair, this is visibility +
> control for the careless-agent case, not containment. KWallet/KeePassXC have a
> version of the per-app prompt; this differs by being backend-agnostic, showing the
> full chain, keeping an audit log, and being agent-agnostic. It doesn't see `.env`
> files — separate problem, pair with a sandbox.
>
> `secrets-dispatcher try` puts it in front of your keyring and Ctrl-C restores
> everything exactly — nothing permanent, no root.
>
> I genuinely don't know yet if this is broadly useful or a niche of one. **Would
> you run it? If not, what's the blocker?** Repo: <link>

---

## 2. Lobsters

Tags: `security`, `linux`. Check "I am the author." Short intro text (Lobsters
prefers substance over pitch):

> A Secret Service (D-Bus keyring) proxy that adds per-application approval + an
> audit log — so you can see when a process (e.g. an AI coding agent running as
> you) reads a credential, with the full process chain, and approve/deny/auto-allow
> it. Also gates `git commit -S`. Explicitly *not* a privilege boundary — same
> user, it just closes the silent path and records it. Reversible one-command trial.
> I'd love the "why would/wouldn't you run this" take from this crowd.

*(Post the [blog post](blog-post.md) as the URL if you want the technical framing
to lead; post the repo if you want people to land on install + code.)*

---

## 3. Reddit

Mind each sub's self-promotion rules; be present in comments; disclose you're the author.

**r/linux** — title:
`I built a per-app approval + audit layer for the Linux keyring (Secret Service)`
> Any process running as you can read any unlocked secret from the keyring — no
> per-app control, no audit trail. secrets-dispatcher proxies the Secret Service so
> each access pauses for approval and gets logged, with full process-chain
> detection. Works with gnome-keyring/KeePassXC/gopass; reversible trial. It's
> visibility + control, not isolation (same-user). Would this fit your setup —
> and if not, why not?

**r/netsec** — link the **blog post**, not the repo (this sub wants a writeup):
`Your AI coding agent can read every secret in your Linux keyring — here's how to see it`
> Writeup on the session-level trust model of the Secret Service and a tool that
> adds per-request approval + audit + process-chain attribution in front of it,
> including for `git commit -S`. Candid about what it is not (no privilege boundary).

**AI-dev communities** (r/ClaudeAI, r/ChatGPTCoding, agent Discords) — less
security jargon, lead with the agent angle:
`Your coding agent can read every secret in your keyring, silently — I made it visible`
> If you run Claude Code / Cursor / etc. directly on your machine, they can read
> your keyring (GitHub tokens, cloud keys) with no prompt. This puts an approval +
> log in front of it so you actually see it — and can auto-allow the tools you
> trust. One reversible command to try. Curious whether this is a real worry for
> you or a non-issue.

---

## 4. Mastodon / Bluesky (ambient, low effort)

> On Linux, your AI coding agent runs as you — so it can read every secret in your
> unlocked keyring (`secret-tool lookup …`), silently, no log. I built a proxy that
> puts an approval prompt + audit trail in front of the keyring (and `git -S`
> signing). Not a sandbox — visibility + a choice. Reversible one-command trial: <link>

---

## 5. Live-answer cheat sheet ("why not just X?")

Keep this open while the threads are hot. The first two are the most damaging —
concede, don't argue.

- **"The keyring maintainers call same-user protection [security theater](https://wiki.gnome.org/Projects/GnomeKeyring/SecurityPhilosophy)."**
  Concede it: this can't *contain* a determined malicious process (it can read the
  keyring file, `LD_PRELOAD`, ptrace). Redirect to the actual claim — it makes the
  access of *benign-but-careless* agents/apps **visible and gateable**, and adds an
  audit log + a signing gate the keyrings don't. "I can't stop malware" and "I can
  show you and gate what your everyday tools read" are different claims; only the
  second is being made.
- **"KWallet / KeePassXC already prompt per-app."** True — if that covers you, use it.
  Different here: **backend-agnostic** (proxies *any* Secret Service keyring), shows the
  **full process chain** not one PID, keeps a **persistent audit log**, and gates **`git
  commit -S`** too. (Also: both of those get *disabled* for prompt fatigue — trust rules +
  process-chain specificity are how this avoids the same fate. Don't oversell; it's a real risk.)
- **"But agents read `.env` off disk, not the keyring."** Correct, and that's the more
  common path — out of scope by design; this closes the keyring/D-Bus path the file tools
  ignore. Complementary; sandbox for on-disk secrets.
- **"Sandbox the agent / run it in a container."** Yes, and complementary — do that
  *and* this. But most run agents on the host for convenience; then this is the only
  per-app control they have.
- **"Don't unlock the keyring / use a separate user for agents."** Fair for the
  strict; breaks the convenience of one shared keyring for everyday apps, which is
  the case this targets.
- **"AppArmor/SELinux/Flatpak already confine apps."** They confine *packaged* apps
  by profile; no per-request approval/audit across arbitrary processes, and
  `curl | sh` agents aren't profiled.
- **"Why should I trust THIS in my secret path?"** Same-user (no new privilege),
  MIT/open source, stores no secrets itself, reversible, and every access is logged.
  See SECURITY.md — it's deliberately candid that this adds visibility, not isolation.
- **"`name`/`args` rules are spoofable."** Correct, and the docs say so — match on
  `exe` (kernel-resolved `/proc/pid/exe`) or systemd `unit` for anything security-
  relevant; `name`/`args` are advisory.
- **"Wayland/KDE/my-distro?"** Honest answer: verified on GNOME/Ubuntu + Arch; other
  Secret Service backends and desktops *should* work but aren't verified — a report
  either way is exactly the feedback I'm after.
