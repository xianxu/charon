# Plan: Provider catalog + onboarding (#000015)

This plan elaborates the *how* for #000015. The *what* lives in the issue.
Base SHA at start: `9843ab4`.

## Framing (post-2026-05-03 scope reduction)

The catalog is a **generic API-key paste-and-revoke mechanism**, not an LLM-inference-specific feature. API-key auth covers a long tail of services well beyond inference (issue trackers, payment APIs, embeddings, search, dev tools — anything that hands the user a static key on a dashboard). Building the data-driven mechanism earns its keep here even though the launch seed is small.

**Launch seed: Anthropic only.** Anthropic is in scope because it was demoted from #13 (Admin API can't mint keys) and we still want to route + revoke its keys through charon. The other 12 providers from the original issue table become aspirational fodder — added as YAML PRs when we actually want them, not up-front.

This shrinks the original plan ~30%: the per-provider research log, the multi-provider e2e targets, and the v2 "real-API discovery" budget primitive all collapse to "verify Anthropic works end-to-end."

## Goals (recap)

1. Adding a Tier-3 provider in the future is a YAML edit + paste, not Go code.
2. TUI surfaces catalog entries with a filter (n=1 today; designed to grow); `[Open]` buttons jump to signup/key URLs; pasted key is stored and routes immediately.
3. Catalog-declared `revoke:` endpoints are honored on deletion (best-effort); providers without one fall back to local-delete + console_url message.
4. Optional `--verify` health-check post-paste.
5. Lifecycle principle locked in #13 holds: catalog keys are *not* mint-managed; revoke is opportunistic.

## Existing scaffolding to plug into

- **Provider interface** at `internal/providers/provider.go:24-72` — concerns minted-key lifecycle. Catalog providers do **not** implement this interface; they are pure-data and routed generically.
- **Routing tables** at `internal/proxy/routing.go:19-118`: `HostToProvider` (exact) + `SuffixToProvider` (suffix). Auth dispatch already handles `AuthBearer` and `AuthURLParamKey`. Need to add `AuthHeader` (and rename `AuthURLParamKey` → `AuthQuery` for consistency) plus a registration hook for catalog-declared entries.
- **vault.Credential** already has a `TypeCatalog` discriminator with `Catalog.KeyMaterial` payload — pasted keys land here, keyed by `<provider_id>/<account_name>` in the existing vault Store. No new keychain abstraction needed.
- **TUI provider picker** at `internal/tui/provider_picker.go:130,218-226` already has a `+ add provider` stub with a "coming in #15" status — that's the entry point we wire.
- **TUI paste flow** at `internal/tui/admin_key_paste.go:42-92` is the closest existing model. Catalog paste is structurally different (no org-discover step), so we author a new `catalogPasteModel` rather than overload.
- **Anthropic package** at `internal/providers/anthropic/` ships admin-key + workspace-level revoke. We re-use the HTTP client + auth helpers; the org-level **list+deactivate** flow needed for catalog-pasted Anthropic keys is **new** code (M4b).

## Package layout

```
internal/providers/catalog/
├── catalog.go            # parsed catalog types: Catalog, Entry, Auth, Revoke
├── catalog_test.go       # schema parse + validation tests
├── catalog.yaml          # the seed (Anthropic) — embedded via //go:embed
├── load.go               # parse embedded YAML at startup, validate, expose
├── load_test.go
├── router.go             # build []routing.Provider entries → register with proxy
├── router_test.go
├── revoke.go             # generic revoke dispatcher: list_endpoint + revoke_endpoint
├── revoke_test.go
├── verify.go             # M5: health-check post-paste
└── verify_test.go
internal/tui/
├── catalog_picker.go     # filterable list of catalog entries (bubbles/list)
├── catalog_picker_test.go
├── catalog_paste.go      # catalogPasteModel: account-name → paste → (optional verify) → store
├── catalog_paste_test.go
└── (+ wiring in provider_picker.go to launch catalog_picker on addProviderMsg)
docs/
└── providers.md          # NEW: catalog reference + how to add a new entry (M6)
```

`catalog.yaml` is the single source of truth, embedded into the binary via `embed.FS`. No external file at runtime — keeps the threat model unchanged (no user-extensible URLs in MVP; deferred per issue's open question).

## Schema (M1)

```go
// internal/providers/catalog/catalog.go

type Entry struct {
    ID               string   `yaml:"id"`                         // "anthropic"
    Name             string   `yaml:"name"`                       // "Anthropic"
    SignupURL        string   `yaml:"signup_url"`
    KeyURL           string   `yaml:"key_url"`
    HostnamePatterns []string `yaml:"hostname_patterns"`          // ["api.anthropic.com"]
    Auth             Auth     `yaml:"auth"`
    Revoke           *Revoke  `yaml:"revoke,omitempty"`           // nil → local-delete only
    ConsoleURL       string   `yaml:"console_url,omitempty"`      // shown on local-delete
    VerifyURL        string   `yaml:"verify_url,omitempty"`       // M5: GET this to validate paste
    Notes            string   `yaml:"notes,omitempty"`
}

type Auth struct {
    Style        string            `yaml:"style"`            // "bearer" | "header" | "query"
    Header       string            `yaml:"header,omitempty"` // for style=header (e.g. "x-api-key")
    Prefix       string            `yaml:"prefix,omitempty"` // for style=bearer (default "Bearer ")
    Param        string            `yaml:"param,omitempty"`  // for style=query (default "key")
    ExtraHeaders map[string]string `yaml:"extra_headers,omitempty"` // static (e.g. anthropic-version)
}

type Revoke struct {
    ListEndpoint *struct {
        URL        string `yaml:"url"`
        KeyMatch   string `yaml:"key_match"`     // "partial_key_hint" — match suffix
        ResultPath string `yaml:"result_path"`   // JSONPath-ish to id field
    } `yaml:"list_endpoint,omitempty"`
    Method     string `yaml:"method"`        // "POST" | "DELETE"
    URL        string `yaml:"url"`           // may contain {key_id}
    Body       string `yaml:"body,omitempty"`
    AuthSource string `yaml:"auth_source"`   // "pasted_key"
}
```

Validation rules (enforced in `Load()` at startup; binary refuses to start on schema breakage):
- IDs are unique, lowercase, `[a-z0-9_-]+`.
- Hostname patterns are non-empty and well-formed.
- `auth.style ∈ {bearer, header, query}`; `header` requires `header` field; `query` defaults `param=key`.
- If `revoke` is set, `revoke.method ∈ {POST, DELETE}`, `revoke.url` is full https://.
- `signup_url`, `key_url`, `console_url` (if set) parse as https URLs.

### Seed entry (M1)

```yaml
- id: anthropic
  name: Anthropic
  signup_url: https://console.anthropic.com
  key_url: https://console.anthropic.com/settings/keys
  hostname_patterns: [api.anthropic.com]
  auth:
    style: header
    header: x-api-key
    extra_headers: { anthropic-version: "2023-06-01" }
  revoke:
    list_endpoint:
      url: https://api.anthropic.com/v1/organizations/api_keys
      key_match: partial_key_hint
      result_path: data[].id
    method: POST
    url: https://api.anthropic.com/v1/organizations/api_keys/{key_id}
    body: '{"status":"inactive"}'
    auth_source: pasted_key
  console_url: https://console.anthropic.com/settings/keys
  verify_url: https://api.anthropic.com/v1/models
```

The `verify_url` choice and `partial_key_hint` semantics are confirmed against Anthropic's public docs during M1 (~30 min), and a real list-endpoint response is captured to `internal/providers/catalog/testdata/anthropic_list_keys.json` for fixture tests.

## M2 — catalog loader + TUI picker

### Loader

```go
// internal/providers/catalog/load.go
//go:embed catalog.yaml
var seedYAML []byte

func Load() (*Catalog, error) {
    var entries []Entry
    if err := yaml.Unmarshal(seedYAML, &entries); err != nil { … }
    if err := validate(entries); err != nil { … }
    return &Catalog{Entries: entries}, nil
}
```

Loaded once at process start; cached on a package-level singleton.

### TUI picker

`internal/tui/catalog_picker.go`: a `bubbles/list` with custom item renderer + built-in filter (`/`).

- Item: `catalogItem{Entry}`; renders `Name` + dim hostname pattern.
- Pressing Enter on an item emits `catalogSelectedMsg{Entry}`; top-level model transitions to `catalog_paste`.
- Esc returns to provider picker.

With n=1 the filter is overkill, but `bubbles/list` is the natural fit — it's not extra code, and the screen renders correctly the day a second entry lands.

### Wiring

In `provider_picker.go`, replace the "coming in #15" stub at line 218-226 with: emit `addProviderMsg{}`; top-level model handles by transitioning to `catalog_picker`.

### Tests (M2)

- `load_test.go`: parse seed YAML; assert Anthropic entry validates; assert various malformed entries are rejected (missing header for style=header, unknown style, duplicate id, etc.).
- `catalog_picker_test.go`: teatest — render, press Enter, assert `catalogSelectedMsg{id: "anthropic"}`.

## M3 — generic metadata-driven per-host router

### Adding `AuthHeader` to routing

```go
// internal/proxy/routing.go (extension)
const (
    AuthBearer AuthMethod = "bearer"   // existing
    AuthHeader AuthMethod = "header"   // NEW: arbitrary header name
    AuthQuery  AuthMethod = "query"    // RENAMED from AuthURLParamKey; arbitrary param name
)

type Provider struct {
    // … existing …
    HeaderName   string            // for AuthHeader (e.g. "x-api-key") or AuthQuery (e.g. "key")
    HeaderPrefix string            // for AuthBearer (default "Bearer ") or AuthHeader (e.g. "Token ")
    ExtraHeaders map[string]string // static headers
}
```

`AuthURLParamKey` becomes a special case of `AuthQuery` with `HeaderName="key"`. Migrate the AI Studio entry (zero behavior change; covered by existing routing tests). Single dispatch path.

### Catalog → routing registration

```go
// internal/providers/catalog/router.go
func Register(c *Catalog, table *proxy.RoutingTable) error {
    for _, e := range c.Entries {
        rp := proxy.Provider{
            Name:          e.ID,
            VaultProvider: e.ID,
            Auth:          authFromStyle(e.Auth.Style),
            HeaderName:    headerNameFor(e.Auth),
            HeaderPrefix:  prefixFor(e.Auth),
            ExtraHeaders:  e.Auth.ExtraHeaders,
        }
        for _, host := range e.HostnamePatterns {
            table.HostToProvider[host] = &rp
        }
    }
    return nil
}
```

Called once from proxy startup, after compiled-provider registration so compiled entries take precedence on overlap (won't happen for the Anthropic seed — `api.anthropic.com` is not in any compiled table today).

### Tests (M3)

- `router_test.go`: build a tiny catalog → register → assert `api.anthropic.com` routes; assert `x-api-key` + `anthropic-version` headers attached on a fake CONNECT round-trip. Cover all three styles via synthetic test entries (so behavior is locked even though the YAML seed is 1 entry).
- Regression: existing `routing_test.go` for OpenAI + AI Studio still passes after the `AuthURLParamKey` → `AuthQuery` rename.

## M4 — TUI add-account flow

### State machine

```
catalog_picker  ──Enter──▶  catalog_paste(entry)
                                │
            ┌───────────────────┤
            ▼                   ▼
   pickAccountName         [Open signup_url] / [Open key_url]
        ▼
    pasteKey
        │
        ├── verify? ──Y──▶ probe verify_url ──ok──▶ store ──▶ done
        │                                       └─err──▶ showErr → back to pasteKey
        └── verify? ──N──▶ store ──▶ done
```

`[Open]` buttons fire `exec.Command("open", url)` on darwin (existing pattern; mirror #14's GCP project flow). Non-blocking.

### Storage

Pasted key stored as `vault.Credential{Type: TypeCatalog, ID: entry.ID + "/" + accountName, Catalog: CatalogData{KeyMaterial: pastedKey}}`. Existing vault Store handles persistence.

### E2E acceptance

Single end-to-end target: paste a real Anthropic key, hit `https://api.anthropic.com/v1/messages` via the proxy with `X-Charon-Account: anthropic/personal`, get a 200. Stopwatch from "+ add provider → working request" should be under 60 seconds (issue's UX target).

### Tests (M4)

- `catalog_paste_test.go`: teatest — pick anthropic → enter account "personal" → paste fake key → assert vault.Set call with id `anthropic/personal` and key material.
- Manual e2e checklist captured in issue's `## Plan` section, ticked during execution.

## M4b — Anthropic-style revoke pathway (generic dispatcher)

The catalog `revoke:` schema is in M1. M4b implements the dispatcher that consumes it. Generic code — Anthropic is the first user but the same path serves any future provider with a list+deactivate or direct-delete shape.

### Dispatcher

```go
// internal/providers/catalog/revoke.go
func (e Entry) Revoke(ctx context.Context, pastedKey string) error {
    if e.Revoke == nil {
        return ErrNoRevokeEndpoint  // caller falls back to local-delete + console_url message
    }
    keyID := pastedKey
    if e.Revoke.ListEndpoint != nil {
        var err error
        keyID, err = lookupKeyID(ctx, *e.Revoke.ListEndpoint, pastedKey, e.Auth)
        if err != nil { return err }
    }
    return callRevoke(ctx, *e.Revoke, keyID, pastedKey, e.Auth)
}
```

`lookupKeyID` issues the list-endpoint request authenticated via `e.Auth` and the pasted key, scans the JSON response for an entry whose `partial_key_hint` matches the pasted key's suffix (last 4 chars per Anthropic's hint convention; YAML can override later if another provider uses different hint length).

### TUI revoke wiring

The existing `internal/tui/admin_revoke.go` modal targets minted credentials. For catalog credentials we add a parallel path: when user hits revoke on a `TypeCatalog` credential, call `entry.Revoke(ctx, key)`; on success or `ErrNoRevokeEndpoint`, delete locally; on other errors, surface but still allow local delete after confirm.

### Tests (M4b)

- `revoke_test.go`: fake httptest server simulates list (returns one matching hint) + POST deactivate; assert end-to-end. Cover key-not-found-in-list error path.
- Anthropic fixture test: the captured `testdata/anthropic_list_keys.json` shape is parsed correctly and the key-id is extracted.

## M5 — `--verify` flag

Per-entry `verify_url` (already in M1's schema; for Anthropic: `/v1/models`). When `charon auth add --verify` (or the TUI's verify checkbox), GET the URL with the just-pasted key authenticated per `entry.Auth`. 200 → ok; 401/403 → reject + ask user to re-paste; 5xx or network → warn but accept (don't block on transient provider issues).

Implementation: ~30 lines in `catalog_paste.go` plus a small helper in `catalog/verify.go`. Default off (extra latency, sometimes counts against quota).

### Tests (M5)

- Unit: helper hits httptest server, asserts 200/401/5xx behaviors.
- TUI: paste → toggle verify on → mock 401 → assert UI shows "key rejected" and stays on paste step.

## M6 — Docs

- `README.md`: brief "Providers supported" mention — Google OAuth + OpenAI admin-key + **catalog (paste-and-revoke; Anthropic seeded)**.
- `docs/providers.md` (new): one-pager. (a) what the catalog is — generic paste-and-revoke for any API-key service. (b) the seeded entry (Anthropic). (c) how to add a new entry — single YAML PR, validation rules, expectations re: revoke pathway.
- `docs/threat-model.md`: brief amendment — catalog is curated/embedded (not user-extensible to arbitrary URLs in MVP); pasted keys are not lifecycle-managed; revoke is best-effort; SSRF surface is bounded by the catalog (no user-supplied URLs hit by the proxy).
- `atlas/charon.md`: under "Credential lifecycle principle", add a "Catalog providers (Tier 3)" subsection cross-referencing `docs/providers.md`. Frame as generic mechanism not LLM-specific.
- `docs/agent-protocol.md`: confirm `X-Charon-Account: <provider_id>/<account_name>` semantics extend to catalog providers.

## M7 — Onboarding polish

In `provider_picker.go`, when the vault has zero credentials configured, default the picker to land on `+ add provider` rather than the empty list — emit `addProviderMsg{}` immediately on first render (`tea.Batch` in `Init()`).

Trivial; ~10 LOC + a teatest.

## Code review checkpoints

Per constitution §3, post-milestone review is mandatory. Two chunks:

- **Chunk 1 (after M3):** schema + loader + router + AuthHeader/AuthQuery dispatch. `BASE_SHA = 9843ab4`, `HEAD_SHA = <post-M3>`. Focus: catalog validation thoroughness, routing.Provider migration safety (no AI Studio regression), auth dispatch correctness across all three styles.
- **Chunk 2 (after M7):** TUI flows + revoke dispatcher + verify + polish. `BASE_SHA = <post-M3>`, `HEAD_SHA = <post-M7>`. Focus: state-machine correctness in catalog_paste, revoke error paths (including auth_source semantics + list-endpoint fixture), verify-flag UX.

Each chunk via `superpowers:requesting-code-review` → `superpowers:code-reviewer`. Address Critical/Important before next chunk. Log outcomes in issue's `## Log`.

## Sequencing

```
M1 ──┬──▶ M2 ──┐
     │         ├──▶ M4 ──▶ M4b ──┬──▶ M5 ──▶ M7 ──▶ M6 docs ──▶ chunk-2 review
     └──▶ M3 ──┘    (review chunk-1 here)
```

M2 + M3 can run in parallel after M1; both feed M4. M4b is sequential after M4 (shares the paste flow's revoke wiring). M5 + M7 after M4b. M6 docs at the end so they reflect what actually shipped.

## Risk register

- **`AuthHeader`/`AuthQuery` migration regression**: AI Studio swap to `AuthQuery` is the riskiest small refactor. Routing tests run on every commit; chunk-1 review is a backstop.
- **Anthropic list-endpoint shape changes**: their public API isn't documented as stable for org-level keys. Capture fixture once into `testdata/`, tolerate 404s gracefully in the dispatcher, surface clear error to user.
- **`bubbles/list` overhead for n=1**: not technically a risk, but worth flagging — if it feels weird in practice, drop to a one-line "press Enter to add Anthropic" stub in M2 and revisit when a 2nd entry lands.

## Out of scope (deferred per issue's open questions)

- User-extensible catalog (`~/.config/charon/providers.yaml` merge). Defer until requested.
- Catalog refresh subcommand (fetch from charon-hosted URL). Defer.
- Adding the original 12 LLM-inference providers. Each becomes a small YAML PR when needed.

## Estimate (revised post-scope-reduction)

Previous v2 estimate: ~12 hr (range 6.1–18.5).

Revised midpoint: **~7–8 hr.** Reductions: M1 schema work is unchanged but seed shrinks (saves ~0.4 hr); M4 e2e drops from 3 providers to 1 (saves ~1 hr); discovery-budget primitive collapses (saves ~1.4 hr); M6 docs simpler (saves ~0.3 hr). Mechanism-bearing work (M2/M3/M4b) is unchanged.
