---
id: 000007
status: open
deps: [000005, 000006]
github_issue:
created: 2026-04-26
updated: 2026-04-26
---

# Scope catalog: categories + filter syntax

## Problem

The current Google scope catalog (`internal/oauth/scope_catalog.go`) is
~20 hand-picked scopes covering common Workspace APIs plus YouTube readonly.
It's a convenience list, not a comprehensive reference.

For a provider as broad as Google (hundreds of OAuth scopes spanning
Workspace, Cloud, Photos, Maps, Analytics, Search Console, Ads, Fitness,
Forms, Apps Script, Classroom, BigQuery, GKE, …), a flat catalog will
get unwieldy fast. The TUI's `a`-key custom-scope escape hatch is fine
for the long tail but doesn't help discovery — users don't know what
exists.

## Spec

### Categories

Group scopes by API surface. Concrete categories worth modeling:

- `workspace` — Gmail, Calendar, Drive, Docs, Sheets, Slides, Tasks,
  Contacts, Forms
- `cloud` — Compute, GKE, BigQuery, Pub/Sub, Cloud Storage, IAM, …
- `media` — YouTube (read + manage), Photos
- `analytics` — Analytics, Search Console, Tag Manager
- `ads` — Ads, AdSense, Display & Video 360
- `identity` — openid, email, profile, userinfo.*

Each scope in the catalog gets a `Category string` field (or a slice if
some scopes legitimately span categories).

### Filter syntax

Extend the search bar's substring filter with category tags:

```
[workspace] gmail              # match gmail.* in workspace category
[cloud] storage                # match storage.* in cloud category
[media]                        # show all media-category scopes
gmail.readonly                 # plain substring search (current behavior)
[workspace cloud] readonly     # multiple categories OR'd
```

Parser rules:
- Bracketed terms `[name]` are category filters
- Free terms outside brackets are substring filters (current behavior)
- Multiple `[a b c]` inside one bracket = OR
- Multiple bracket groups across the input = AND (rare; could just
  consolidate to single bracket group)

### Catalog source of truth

Two paths, each with tradeoffs:

**A. Hand-curated YAML/JSON file** — version-controlled, curated quality,
quick to update for hot scopes. Has to be maintained by humans. Risk of
drift from Google's actual catalog.

**B. Generated from Google's Discovery API** — programmatic, comprehensive,
no manual curation. Loses category mapping (Discovery doesn't expose
"workspace" vs "cloud" as a field — would need a heuristic). Periodic
regeneration script.

Probably hybrid: generate the URL list from Discovery, layer human-edited
metadata (category, short name, description) on top. Catalog file stays
small (just the metadata layer); URLs come from Google.

### TUI changes

- `Category` column in catalog rendering (subtle; maybe shown as a
  prefix like `[ws] gmail.readonly` or as a sort grouping)
- Search bar parser handles bracket syntax
- Help text shows the syntax: `type to filter   [workspace] for category`

### Multi-provider implications

When provider abstraction lands (#000006), each provider has its own
catalog with its own categories. Dropbox might have categories like
`files`, `users`, `team`, `paper`. The category model becomes
provider-specific, not a global enum.

## Plan

- [ ] **Research**: confirm what Google's Discovery API actually exposes
  about scopes; sample categorization for ~50 popular scopes
- [ ] **Catalog metadata format**: pick yaml/json/Go-native, decide on
  category enum vs free-form strings
- [ ] **Add category to ScopeInfo + populate existing catalog entries**
- [ ] **Filter parser**: extend `scopeRow.matches` (or split into a
  `scopeFilter` type) to handle bracket syntax
- [ ] **TUI rendering**: subtle category indicator + help text update
- [ ] **Tests**: filter parser, category-only, category+substring,
  multi-category
- [ ] **(Optional) Discovery scraper**: script to pull Google's scope
  list and reconcile against the manual catalog metadata

## Notes

- The `a`-key custom scope URL escape hatch should remain — categories
  shouldn't be a barrier to granting an off-catalog scope.
- Once #000006 lands and we have multiple providers, the category UX
  needs to handle "show me all `[workspace]` from Google plus all
  `[files]` from Dropbox" — could be addressed with provider-prefixed
  syntax `[google.workspace]` if needed.

## Log
