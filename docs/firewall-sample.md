# OS firewall guidance — closing A2

`charon` mediates HTTPS requests so the agent's upstream API calls
get the bearer token attached and the audit log records every call.
That works only when the agent actually goes through charon.

This doc is about **what stops the agent from going around charon**.
Specifically the threat-model item:

> ### A2. AI agent bypasses charon and calls API directly
>
> Drops `HTTPS_PROXY` from env, opens a raw TCP connection to
> `gmail.googleapis.com:443`. With no credential of its own this
> fails, but combined with any leaked token the agent can reach
> upstream without charon's audit log seeing it.

`charon` itself can't fix this — it's a userland proxy, not a
network kernel module. The fix lives one layer down, in the OS
firewall. This doc gives you concrete configs for the three options.

## When this matters

This is **defense in depth**. The path requires:

1. The agent has somehow obtained a valid OAuth token (charon's M4
   ACL is supposed to prevent this; firewall rules are the second
   net if it fails).
2. The agent then makes a direct connection to the upstream API
   without going through charon.

Step 2 is what we close here. Step 1 is closed by everything else
in the threat model. Run this doc's setup if you've decided you
want both nets — most paranoid threat models do.

## Recommended: Lulu (free, open source)

[Lulu](https://objective-see.org/products/lulu.html) by Patrick
Wardle (Objective-See). Per-process, per-host firewall. Free.

Install via Homebrew:

```bash
brew install --cask lulu
```

Then in Lulu's preferences:

1. **Default rule**: "Block" (so anything not explicitly allowed is
   denied).
2. **Allow** for `~/.local/bin/charon`:
   - Address: `*` (charon needs unrestricted outbound — it's the
     legitimate egress point)
   - Port: `*`
   - Protocol: TCP
3. **Block** for everything else when the destination matches
   charon's upstream API hosts:
   - `*.googleapis.com`
   - `accounts.google.com` (only charon's auth flow needs this)
   - Any other API hostnames you've added to charon's per-host
     routing config (`internal/proxy/routes.go`)
4. **Allow** for everything else outbound (so `npm install`,
   `git fetch`, etc. still work — they don't go through charon).

Lulu prompts on first connection from each app, and the chosen
rule persists. Once trained for ~a week, the noise drops to
nothing.

## Alternative: Little Snitch (paid)

[Little Snitch](https://obdev.at/products/littlesnitch/) is the
oldest and most full-featured macOS firewall. Same per-process,
per-host model as Lulu but with more polished UX, profile import/
export, and a longer track record.

The rule shape is identical to the Lulu setup above. Little Snitch
also supports importing a `.lsrules` configuration file:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<rules>
  <!-- Allow charon unrestricted outbound -->
  <rule>
    <process>/Users/<USERNAME>/.local/bin/charon</process>
    <action>allow</action>
    <protocol>tcp</protocol>
    <remote-domain>any</remote-domain>
    <remote-port>any</remote-port>
  </rule>

  <!-- Block all other processes from talking to googleapis.com -->
  <rule>
    <process>any</process>
    <action>deny</action>
    <protocol>tcp</protocol>
    <remote-domain>*.googleapis.com</remote-domain>
    <remote-port>443</remote-port>
  </rule>
  <rule>
    <process>any</process>
    <action>deny</action>
    <protocol>tcp</protocol>
    <remote-domain>accounts.google.com</remote-domain>
    <remote-port>443</remote-port>
  </rule>

  <!-- Permit everything else (your normal traffic) -->
  <rule>
    <process>any</process>
    <action>allow</action>
    <protocol>tcp</protocol>
    <remote-domain>any</remote-domain>
    <remote-port>any</remote-port>
  </rule>
</rules>
```

Replace `<USERNAME>` and adjust hostnames to whatever upstream APIs
you've configured charon for.

## Fallback: pf (built into macOS)

`pf` is macOS's packet filter. It's free and built-in, but operates
at L3/L4 — it knows IPs and ports, not hostnames or process names.
Two consequences:

1. To block "anything but charon to *.googleapis.com", you'd need
   to enumerate Google's CDN IP ranges and pin to them. Apple
   publishes some IP ranges, Google publishes its own; both shift.
2. pf can't tell charon-the-process from the agent-the-process —
   they're both your user's TCP traffic.

So pf can't directly enforce the rule we want. It can only do the
nuclear option: **block all outbound except via localhost**. That
means every program that talks to the internet has to go through
some local proxy. Workable for tightly-controlled environments;
disruptive for general dev work.

If you want to try anyway — sample `/etc/pf.anchors/charon-block`:

```pf
# Block all outbound TCP except to localhost.
# Loaded as a pf anchor — see `man pf.conf`. Requires sudo + reload.
block out quick proto tcp all
pass out quick proto tcp from any to 127.0.0.1
pass out quick proto tcp from any to ::1
```

To enable:

```bash
sudo cp /path/to/charon-block /etc/pf.anchors/
sudo cp /etc/pf.conf /etc/pf.conf.bak
echo 'anchor "charon-block"' | sudo tee -a /etc/pf.conf
echo 'load anchor "charon-block" from "/etc/pf.anchors/charon-block"' | sudo tee -a /etc/pf.conf
sudo pfctl -f /etc/pf.conf -e
```

To disable:

```bash
sudo pfctl -d
sudo cp /etc/pf.conf.bak /etc/pf.conf
sudo pfctl -f /etc/pf.conf -e   # back to original ruleset
```

**Don't use this on a primary machine** unless you know what you're
doing. Lulu/Little Snitch is dramatically more practical for the
A2-mitigation use case.

## What this does NOT close

- A leaked token used from a different machine entirely (your
  firewall rules don't apply elsewhere).
- An attacker who compromises charon itself (hardened runtime
  blocks injection — see A5 — but a buggy charon could still
  exfiltrate via its legitimate outbound path).
- Anyone who has SIP off and `sudo` (adversary C — they can disable
  pf, kill Lulu, attach a debugger to charon, whatever).

This is one specific net for one specific gap. Worth setting up
if you've decided you want it; not catastrophic to skip on a
single-developer machine where you trust your own agent
implementations.

## See also

- [`threat-model.md`](threat-model.md) → adversary A2.
- [Lulu source](https://github.com/objective-see/LuLu).
- [Little Snitch docs](https://help.obdev.at/littlesnitch5/).
- [`pf.conf(5)`](x-man-page://5/pf.conf) on macOS.
