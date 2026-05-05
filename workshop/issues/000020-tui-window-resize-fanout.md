---
id: 000020
status: working
deps: []
github_issue:
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 2
actual_hours:
---

# TUI doesn't propagate window resize to non-scopes screens

## Problem

bubbletea catches SIGWINCH and dispatches `tea.WindowSizeMsg`, but the
top-level `model.Update` (`internal/tui/model.go:225-233`) only forwards
that message to the scopes screen:

```go
case tea.WindowSizeMsg:
    m.width, m.height = msg.Width, msg.Height
    if m.current == screenScopes {
        var cmd tea.Cmd
        m.scopes, cmd = m.scopes.Update(msg)
        return m, cmd
    }
    return m, nil
```

Consequences:

- All other ~13 screens (provider picker, OAuth picker, admin
  paste/list/mint/revoke/detail, GCP setup, catalog
  picker/paste/list/revoke) never receive resize events. Resizing the
  terminal while on any of them leaves layout stale until the user
  navigates away and back.
- `adminKeyPasteModel.Update` (`internal/tui/admin_key_paste.go:146`) is
  already coded to handle `WindowSizeMsg` and store width/height — but
  it's dead code from the parent's POV because the parent never
  forwards.
- On first entry to a non-scopes screen, the parent doesn't seed the
  child with the current `m.width/m.height` either, so those screens
  render against zero-valued dimensions until a SIGWINCH happens *after*
  entry (which, per above, also won't reach them).

The scopes screen is robust because (a) it does its own ioctl seed in
`newScopesModel` (`internal/tui/scopes.go:266`) before any
`WindowSizeMsg` arrives, and (b) it's the one screen the parent
forwards to. The other screens have neither.

## Spec

Two fixes, both in `internal/tui/model.go`:

1. **Fan out `WindowSizeMsg` to whichever child is current**, not just
   scopes. Update each sub-model that has meaningful layout (paste
   forms, list views with row windows, modal confirm dialogs) to accept
   `WindowSizeMsg` and update its width/height. The signature pattern
   is already established by `adminKeyPasteModel`.
2. **Seed dimensions on screen transition.** When the parent switches
   `m.current` to a new screen, synthesize a `WindowSizeMsg{Width:
   m.width, Height: m.height}` to that child (or call a setter) so the
   child renders against real dimensions on first frame instead of
   zero.

Out of scope: introducing a new abstraction for sub-models. Keep
forwarding explicit per `m.current` — same shape as the existing
scopes-only branch, just exhaustive.

## Plan

- [ ] Audit each sub-model (`providerPickerModel`, `pickerModel`,
  `adminKeyListModel`, `adminKeyPasteModel`, `adminMintModel`,
  `adminRevokeModel`, `adminKeyDetailModel`, `gcpSetupModel`,
  `catalogPickerModel`, `catalogPasteModel`, `catalogAccountListModel`,
  `catalogRevokeModel`) for which ones actually use width/height for
  layout
- [ ] Extend `model.Update`'s `WindowSizeMsg` branch to dispatch to
  `m.current`'s sub-model
- [ ] Add WindowSize handling to sub-models that need it but don't have
  it yet
- [ ] Add a "seed dimensions" step at every `m.current = ...`
  transition — a small helper would avoid scattering the boilerplate
- [ ] Manual verification: `stty rows N` (or just resize the terminal)
  on each screen and confirm layout reflows
- [ ] Consider an automated test analogous to
  `internal/tui/render_dump_test.go` that drives a `WindowSizeMsg`
  through each screen and snapshots the output

## Log

### 2026-05-05
Filed after noticing the gap during a discussion about SIGWINCH
handling. Discovery starts at `internal/tui/model.go:225` (parent
forwarder) and `internal/tui/admin_key_paste.go:146` (orphaned child
handler).
