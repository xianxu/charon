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

- [x] Audit sub-models for width/height usage (only `scopesModel`
  actually lays out against height today; `adminKeyPasteModel` stores
  width/height but never reads them; everything else uses hardcoded
  60-char separators)
- [x] Extend `model.Update`'s `WindowSizeMsg` branch to dispatch to
  `m.current`'s sub-model
- [x] Add a "seed dimensions" step at screen transitions via a wrapper
  on `Update` that detects `m.current` changes and batches a synthetic
  `tea.WindowSizeMsg` with cached dimensions
- [x] Add automated tests covering the three new behaviors
  (`TestWindowSizeFanout`, `TestWindowSizeFanoutToPaste`,
  `TestSeedSizeOnScreenTransition`)
- [ ] Manual verification: deferred — only `scopesModel` actually
  reflows on resize today, and that path was already wired before this
  fix. The new wiring is a structural correctness fix; the snapshot
  test covers the message-routing contract that future
  width/height-aware screens will rely on.

## Log

### 2026-05-05 — session summary
Filed after noticing the gap during a discussion about SIGWINCH
handling. Discovery starts at `internal/tui/model.go:225` (parent
forwarder) and `internal/tui/admin_key_paste.go:146` (orphaned child
handler).

Fix landed as a thin wrapper on `model.Update`: factored the existing
big switch into `updateInner`, with a wrapper that (a) drops the
`screenScopes`-only guard so `WindowSizeMsg` falls through to the
bottom-of-function screen dispatch (forwarding to whichever child is
current), and (b) detects `m.current` changes and batches a
`seedSizeCmd` that re-delivers the cached dimensions to the new
screen on the next tick. Added `internal/tui/model_resize_test.go`
with three tests covering scopes-fanout, paste-fanout, and
seed-on-transition. All `go test ./...` green.

Behavioral surface today is small — `scopesModel` is the only screen
that reflows on resize, and it was already wired. The change is a
structural fix: `adminKeyPasteModel`'s orphaned `WindowSizeMsg`
handler is no longer dead code, and any future screen that wants to
react to resize gets it for free without having to remember to add a
forwarding branch in the parent.
