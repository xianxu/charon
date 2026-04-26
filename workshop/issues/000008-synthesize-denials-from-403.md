---
id: 000008
status: open
deps: [000005]
github_issue:
created: 2026-04-26
updated: 2026-04-26
---

# Synthesize scope denials from upstream 403 responses

## Problem

Today the proxy only records scope denials when the agent **opts in** by
sending `X-Charon-Scope`. If the agent doesn't set the header:

1. Charon forwards the request to the upstream API
2. Google (or whoever) returns 403 with "insufficient scope"
3. The agent sees a cryptic 403; user sees nothing actionable in charon
4. No badge appears in the TUI for that scope

This breaks the discoverability promise of the badge UX. Agents written
without charon awareness — most of them initially — bypass the whole
"requested by proxy" feedback loop.

## Spec

Make charon parse upstream 403 responses to back out which scope was
missing, and record that as a denial in the same ring buffer (`ScopeTracker`)
the explicit `X-Charon-Scope` path uses today.

### Three layers of signal, in order of cleanness

**Layer 1: `WWW-Authenticate` header (RFC 6750 standard)**

When OAuth-protected APIs reject for insufficient scope, they're
supposed to return:

```
HTTP/1.1 403 Forbidden
WWW-Authenticate: Bearer error="insufficient_scope",
                  scope="https://www.googleapis.com/auth/gmail.readonly"
```

The `scope=...` directive holds the missing scope verbatim. Cleanest
signal — no guessing. Some Google APIs return this; coverage varies
across the surface.

**Layer 2: Provider-specific body parsing**

Without the standard header, error JSON sometimes carries the scope
hint. Google's 403 body typically just says `"Request had insufficient
authentication scopes"` with no scope name, but other providers (GitHub,
some Microsoft APIs) put it in error fields. Handle per-provider in a
response interceptor.

**Layer 3: URL→scope mapping table**

Maintain a table per provider mapping URL patterns + HTTP method to the
scope(s) required. When neither header nor body discloses the scope,
look up by request shape.

For Google specifically: the [Discovery API](https://developers.google.com/discovery/v1/reference/apis)
exposes per-method `auth.oauth2.scopes` arrays for every API. A
**build-time scraper** could materialize the full table as a Go map
checked into the repo. Refresh periodically.

Mapping shape:
```go
type ScopeMapping struct {
    Method string  // "GET", "POST", etc.
    Path   string  // e.g. "gmail.googleapis.com/gmail/v1/users/{userId}/messages"
    Scopes []string // any of these is sufficient
}
```

Path matching uses provider-specific globbing (handle `{userId}`-style
templates). Performance: lookup is rare (only on 403), no hot path.

### When all three fail

Last resort: record `ScopeDenial{Scope: "<unknown>", URL: <full URL>}` so
the user at least sees "an agent failed authorization at this URL" in
the TUI. Less actionable but better than silent failure.

### Where this lives in the proxy code

`internal/proxy/proxy.go` has the response-forwarding path. Add a hook
that runs on `resp.StatusCode == 403`:

1. Read body (limited bytes, restore for forwarding)
2. Try Layer 1 (WWW-Authenticate)
3. Try Layer 2 (provider-specific body parser)
4. Try Layer 3 (URL→scope lookup via per-provider table)
5. Record best-available signal to `ScopeTracker`

Body restore is non-trivial since Go's `http.Response.Body` is a Reader.
Use `httputil.DumpResponse` or wrap with a `TeeReader` that buffers up
to N bytes for inspection.

### TUI implications

`scope_tracker.ScopeDenial` likely needs a new field:

```go
type ScopeDenial struct {
    Provider string
    Account  string
    Scope    string    // "" when unknown (Layer 3 miss)
    URL      string    // populated for Layer 3 misses; the failing endpoint
    Source   string    // "header" | "body" | "url-table" | "unknown"
    Count    int
    LastSeen time.Time
}
```

The TUI surfaces these in the existing badge column. For unknown-scope
entries, the TUI could show "agent failed at <URL>" via a special row
group rather than mapped to catalog rows.

## Plan

- [ ] **M1: Layer 1 implementation**
  - Parse `WWW-Authenticate: Bearer error="insufficient_scope" scope="..."`
  - Wire into proxy's response path; record to `ScopeTracker`
  - Tests with synthetic upstream responses
- [ ] **M2: Provider-specific body parsing**
  - Interface `BodyScopeExtractor` per provider
  - Google extractor (best-effort; current Google bodies don't actually
    include the scope, so this may be a no-op until we find a Google API
    that does include it)
- [ ] **M3: URL→scope table for Google**
  - Build-time scraper from Google Discovery API
  - Generates a Go map under `internal/oauth/google_scope_map_gen.go`
  - Wire into proxy 403 handler as Layer 3 fallback
- [ ] **M4: TUI surfacing**
  - Add `URL` and `Source` to `ScopeDenial` JSON
  - TUI: dedicated section/row group for unknown-scope denials

## Notes

- Primary contract remains "agents declare `X-Charon-Scope` upfront". This
  feature is fallback for agents that don't opt in.
- Discovery scraper output gets stale; periodic regeneration (CI job?
  manual `go generate` on releases?). Stale data degrades gracefully —
  still better than no signal.
- Body buffering on every proxy request is wasteful. Only buffer on 403;
  use `TeeReader` lazily.

## Log
