---
id: 000021
status: open
deps: []
github_issue:
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 5
---

# gpg-agent lifecycle integration (Level 2)

## Problem

Charon already enforces credential lifecycle for API keys: disarm-by-default at boot, kept armed by activity, auto-disarm on idle, menubar surface for state. The shared-brain effort introduces a *second* credential daemon — gpg-agent — holding unlocked GPG private keys for decrypting brain-private and any brain-shared-\* the user is a recipient on. Without integration, the user has two independent armed/disarmed states to track and reason about: charon for API keys, gpg-agent for brain decryption. Parallel mechanism, exactly what we've been told to avoid.

The shared-brain threat model (`brain/atlas/threat-model-shared-brain.md` §*Lifecycle control via charon*) names charon as the single control plane for credential lifecycle and specifies Level 2 integration as the planned posture: lifecycle mirror plus pre-warming via Keychain-stored passphrase. This issue is the implementation of that contract.

## Done when

- Charon arm event triggers gpg-agent pre-warm: charon fetches the GPG-key passphrase from Keychain, feeds it via pinentry / `gpg-preset-passphrase`, gpg-agent's cached unlock is established without a UI prompt mid-flow.
- Charon disarm event (boot, idle timeout, manual button) triggers `gpg-connect-agent reloadagent /bye` (or equivalent), flushing gpg-agent's cached unlocks. After disarm, the next decrypt attempt blocks on pinentry, which is what we want.
- Charon's menubar shows GPG cache state alongside API-key state. One mental model: "armed = my secrets are accessible to agents running as me; disarmed = they aren't."
- Verified end-to-end against a gcrypt'd brain-private after `nous#3` M1 ships: arm → push/pull works without prompt; disarm → next push/pull prompts (or fails, depending on interactive context).
- Threat-model doc (`brain/atlas/threat-model-shared-brain.md`) lifecycle section is verified accurate against the shipped behavior — no drift between the doc's claims and what the code does.

## Spec

Source: `brain/atlas/threat-model-shared-brain.md` §*Lifecycle control via charon*. Cross-references `nous#3` (gcrypt setup uses gpg-agent for the GPG-encrypted shared brains; brain-private's symmetric gcrypt passphrase is independent and goes through charon's existing Keychain integration, not this issue).

**What Level 2 means** (the spec defers Level 1 and Level 3, both named in the threat model):

- *Level 1* — lifecycle mirror only (no pre-warming). Strict subset of Level 2; we ship Level 2 directly because pre-warming is a clean UX win and the additional code is small.
- *Level 2* (this issue) — lifecycle mirror plus pre-warming. Charon mediates the user-facing state (arm/disarm); gpg-agent stays the actual decrypt daemon. No protocol wrapping.
- *Level 3* — full Assuan socket proxy with per-decrypt consent prompts. Named as a credible upgrade path when per-decrypt consent becomes valuable; not now.

**The pre-warming flow** (Level 2-specific):

1. Charon arm event fires (boot completion + first activity, manual button, etc., per existing charon arm semantics).
2. Charon reads the GPG-key passphrase from a Keychain entry whose name is declared in charon's config (e.g., `brain-private-gpg-export-passphrase`).
3. Charon invokes `gpg-preset-passphrase --preset <keygrip>` (or equivalent), feeding the passphrase to gpg-agent. gpg-agent caches the unlocked key in memory under its existing TTL.
4. From this point until charon disarms, any decrypt request from any process succeeds without UI.

**The disarm flow:**

1. Charon disarm event fires (boot, idle timeout, manual button, charon's existing disarm triggers).
2. Charon issues `gpg-connect-agent reloadagent /bye` to flush gpg-agent's cached unlocks.
3. Next decrypt blocks on pinentry. In a non-interactive context (e.g., a sync daemon push), the operation fails cleanly and surfaces the disarmed-state hint.

**Threat-surface notes** (carried from the threat-model doc, not original to this issue):

- Pre-warming does not widen the attack surface vs. tty pinentry — the passphrase already lives in Keychain; pre-warming changes *when* it leaves Keychain, not *whether*.
- A compromised charon can mis-arm gpg-agent. Acceptable: charon is already a higher-value target on the device for API-key reasons, and brain integration shares charon's existing protections (signed binary, threat-model defenses A1a/A1b in `docs/threat-model.md`).

**Out of scope:**

- The Assuan socket proxy (Level 3). Track separately if/when per-decrypt consent prompts become valuable.
- ssh-agent integration. Same shape as gpg-agent integration; if SSH key lifecycle becomes a friction point, file a follow-on. Brain doesn't depend on it for shared-brain MVP.
- Cross-platform support. macOS-only initially (charon is macOS-only today).

## Plan

### M1 — config + keygrip discovery

- [ ] Extend charon config to declare the GPG keygrip(s) and Keychain entry name(s) for pre-warming. Reasonable shape: `gpg_keys: [{ keygrip, keychain_entry }, ...]`.
- [ ] Helper to discover keygrip(s) from `~/.gnupg/` so config can be auto-populated by an init step rather than requiring the user to look up keygrip manually.

### M2 — arm-time pre-warm

- [ ] Wire charon arm event to a pre-warm step: read passphrase from Keychain, invoke `gpg-preset-passphrase --preset <keygrip>` for each configured keygrip.
- [ ] Handle failures gracefully: missing Keychain entry, wrong keygrip, gpg-agent not running. Each surfaces a charon-level error without blocking arm.
- [ ] Verify with a synthetic decrypt: arm → run a no-op decrypt → confirm no pinentry prompt.

### M3 — disarm-time flush

- [ ] Wire charon disarm event to `gpg-connect-agent reloadagent /bye`.
- [ ] Verify with a synthetic decrypt: disarm → run a decrypt → confirm pinentry prompts (interactive) or operation fails cleanly (non-interactive).

### M4 — menubar surface

- [ ] Extend charon's menubar item to show GPG cache state alongside API-key state. State values: cached / not-cached / unknown.
- [ ] Single status line that summarizes the unified posture (armed/disarmed + GPG cached/flushed).

### M5 — end-to-end verification + doc sync

- [ ] After `nous#3` M1 ships: dogfood end-to-end against the gcrypt'd brain-private. Push, pull, edit, push again — observe gpg-agent caching working under charon's lifecycle.
- [ ] Cross-check the threat-model doc's *Lifecycle control via charon* section against shipped behavior. Update either the doc or the impl if drift exists.
- [ ] Add an atlas entry under charon's docs pointing at the shared-brain threat model so the cross-cutting context is discoverable from charon.

## Log

### 2026-05-05

- Issue created from `brain/atlas/threat-model-shared-brain.md` §Lifecycle control via charon. The threat-model doc is the authoritative spec for *what* this integration must achieve; this issue is *how*. Level 2 (lifecycle mirror + pre-warming) selected over Level 1 (mirror only); Level 3 (Assuan socket proxy) deferred.
