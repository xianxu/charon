# Pensive: Why Charon — The Credential Problem for AI Agents

**Date:** 2026-04-24
**Status:** Thinking out loud

---

## How We Got Here

Building a personal knowledge monorepo ("brain") that holds life data, tools, and AI-assisted workflows. Started with a Gmail search tool — search email, enrich contractor records, build a personal CRM. Simple Python script calling the Gmail API with OAuth tokens stored in macOS Keychain.

Then we asked: who can see the token?

Three paths to the credential:
1. **CLI** (`tools/gmail/run.sh`) — AI invokes the script, gets back search results. Token never in stdout. Safe.
2. **Library** (`from lib.google_auth import get_credentials`) — AI writes Python that calls the function, gets a Credentials object back. Could print the refresh token. Not safe.
3. **Keychain directly** — AI runs `security find-generic-password` via Bash. Direct access. Not safe.

We closed path 2 by making all credential functions private and changing the library API to take email strings instead of Credentials objects. Path 3 is mitigated by Claude Code's approval prompt — you'd see the command and deny it. But these are convention barriers, not security boundaries.

For a toy Gmail tool, that's fine. But brain is heading toward banking, calendar, documents — real sensitive services. Convention barriers won't cut it.

## The Security Model We Want

**The AI should be able to use services the user has authorized, but never see the credential.** The credential and the API call must happen in the same trust boundary, and the AI must be outside that boundary.

This is the same problem that cloud platforms solved with instance roles and metadata services: the application gets authenticated API access without ever seeing the access key. We need the local equivalent.

## The Proxy Model

The cleanest architecture: a local HTTPS proxy that sits between the AI and the internet. The AI makes normal HTTP requests through the proxy. The proxy looks up the credential, injects the Authorization header, forwards the request, and returns the response. The token never enters the AI's process.

Infisical's Agent Vault does exactly this — single Go binary, HTTPS_PROXY-based, credential injection at the network layer. But it's heavier than we need (dashboard, approval workflow, its own SQLite vault). We want:

- OS-native vault (macOS Keychain, Linux secret service) — not another encrypted database
- CLI only, no UI
- Single binary, Go, portable
- OAuth 2.0 + PKCE with automatic refresh and rotation
- Support multi accounts from the same provider

## The Multi-Account Problem

One interesting design question: in proxy mode, the proxy sees the destination host (e.g., `gmail.googleapis.com`) but not which account to use. If you have two Gmail accounts, how does the proxy pick the right credential?

We landed on a **custom header**: `X-Charon-Account: user@gmail.com`. The proxy reads it, selects the right token, strips the header before forwarding. Simple, explicit, doesn't break the proxy transparency model. If only one account exists for a service, the header is optional.

## OAuth Security

Google supports OAuth 2.0 with PKCE (Proof Key for Code Exchange) — the modern standard for native apps. No client_secret needed at runtime. Combined with refresh token rotation (each refresh invalidates the old refresh token), a leaked token becomes useless after the next legitimate refresh.

This is important for the threat model: even if the AI somehow exfiltrated a token, the window of exposure is bounded by the rotation interval.

## The Two-Level Keychain Insight

macOS Keychain has two tiers of access control that most people don't know about:

1. **App-specific items** — added with an ACL binding to a code-signed app. Other apps get an "Allow/Deny" dialog. Safari passwords work this way.
2. **Generic passwords** — added via `security add-generic-password`. Any process running as your user can read them without prompting.

Our current tokens are generic passwords (tier 2). A Go binary that's code-signed could use tier 1 — the Keychain ACL would ensure only Charon can read the tokens. Even if the AI ran `security find-generic-password`, macOS would block it.

This is the long-term play: Charon as a signed binary with exclusive Keychain access.

## Why a Separate Repo

Charon is infrastructure, not specific to brain. Any repo that uses AI agents and needs to access authenticated services can use it. It belongs alongside ariadne (the workflow substrate) as a shared tool.

The name: Charon is the ferryman who carries you across the river Styx. He handles the crossing — you don't touch the water.

## Build Order

1. **Proxy + Keychain + manual token** — prove the model works, test with brain's Gmail tool
2. **OAuth PKCE flow** — `charon auth google user@gmail.com`, automatic refresh and rotation
3. **Linux support** — secret service backend, cross-compile
4. **Code signing + Keychain ACL** — tier 1 access control, real security boundary
5. **More providers** — GitHub, banking, calendar
