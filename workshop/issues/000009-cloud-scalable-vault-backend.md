---
id: 000009
status: open
deps: []
github_issue:
created: 2026-04-26
updated: 2026-04-26
---

# Cloud-scalable vault backend + multi-user readiness

## Problem

Charon today is a single-user local-machine agent: macOS Keychain on
darwin, OS-user-scoped credentials, single audit log, no caller identity
above the OS user. That's the right shape for the personal AI-agent use
case, but it doesn't extend to a deployment model where charon runs as a
shared service for many users — agents in CI, fleet automation, hosted
agent platforms — where credentials need to live in a scalable, audited
secrets backend (HashiCorp Vault, AWS Secrets Manager, GCP Secret
Manager) and access has to be authenticated and authorized per caller.

This issue is the placeholder for "make charon work in a cloud / shared
service deployment." It is not blocking any current local-use work.

## Spec (sketch — to refine when prioritized)

The work breaks into two layers:

### Layer 1 — Pluggable vault backend (smaller, mechanical)

Goal: same `vault.Store` interface, new implementations for cloud
secret stores. Two interface gaps to close first:

- Add `context.Context` as first arg to every `Store` method
  (`Get(ctx, ...)`, etc.). One-shot churn across all call sites; cloud
  backends need cancellation/deadlines/tracing.
- Add per-backend constructors that accept endpoint + namespace + auth
  config (e.g. `hashivault.New(addr, token, namespace) vault.Store`,
  `awssm.New(region, prefix) vault.Store`).
- Wire backend selection in `cmd/charon` via a `--vault=...` flag
  (default keeps local keychain).
- Namespace prefix (already part of M3 in #000003 as `charon` vs
  `charon-dev`) extends naturally to cloud paths.

### Layer 2 — Caller identity, AuthN/AuthZ, audit (larger, design-heavy)

Today charon trusts the OS user. Cloud / shared deployment requires:

- **AuthN**: who's calling charon? Proxy clients need to identify
  themselves (mTLS client cert, SPIFFE, signed JWTs from an IdP, etc.).
- **AuthZ**: a *new* layer above `vault.Store` (think
  `policy.Authorizer`) that decides which (provider, account) pairs a
  given caller may fetch. The Store stays narrow — fetch by key — and
  the policy layer gates access.
- **Tenant isolation**: `Credential.Account` is currently the *external*
  identity (`user@gmail.com`); there's no charon-internal tenant axis.
  The auth/audit layer would tag credentials with the owning tenant.
- **Per-caller audit trail**: existing audit log lacks caller identity.
  Needs the AuthN context plus an immutable / WORM-friendly sink in
  shared deployments.

These additions are deliberately *above* `vault.Store`, not inside it.
The Store stays the lowest-level primitive (fetch credential X for
backend Y) — which is the right shape for both single-user and
multi-user deployments.

## Big architectural question to settle first

Single-user local agent vs multi-user cloud service look like
fundamentally different products. The current decisions ("no CGo",
"binary in `~/.local/bin`", "macOS Keychain", "audit log to stderr")
were all driven by the local model and most of them invert in a cloud
deployment. Reasonable answers to consider:

- **Same binary, different config**: charon flexibly runs both ways.
  Risk: inflates the local binary's surface and complexity for a use
  case it may never see.
- **Two binaries sharing `internal/vault`** as a Go package: cleanest
  separation of concerns, opt-in complexity, but doubles the operational
  story.
- **Don't do cloud at all**: keep charon strictly local; if someone
  wants the cloud thing, build a different product.

## Notes

Surfaced during #000003 implementation: while building the CGo Keychain
backend, we noticed the `vault.Store` interface has no `context.Context`
and the service name is hardcoded — both fine for local but blockers
for cloud. Captured here so the realization isn't lost.

## Plan

To be filled in when this issue is prioritized. Likely brainstorm pass
first to choose between the architectural options above.
