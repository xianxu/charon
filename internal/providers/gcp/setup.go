package gcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// RequiredServices is the canonical list of GCP APIs charon enables
// on a Gemini-hosting project. Vertex AI (aiplatform), AI Studio
// data plane (generativelanguage), and the API Keys API used by M4
// to mint AI Studio keys.
//
// Order is stable so test assertions and audit logs render
// deterministically.
var RequiredServices = []string{
	"aiplatform.googleapis.com",
	"apikeys.googleapis.com",
	"generativelanguage.googleapis.com",
}

// DefaultVertexRegion is the region charon picks when the user has
// no preference. us-central1 has the broadest model availability and
// is in the lowest pricing tier.
const DefaultVertexRegion = "us-central1"

// SupportedVertexRegions is the canonical region list charon offers.
// Not exhaustive — the API supports more — but covers the common
// choices. Tests and pickers iterate this in stable order.
var SupportedVertexRegions = []string{
	"us-central1",
	"us-east1",
	"us-east4",
	"us-west1",
	"europe-west1",
	"europe-west4",
	"asia-northeast1",
	"asia-southeast1",
}

// Picker is the user-facing interaction during Setup. The
// orchestrator delegates every UI decision to a Picker so the same
// flow can drive a CLI prompt, a bubbletea TUI, or a test stub.
type Picker interface {
	// PickProject is called with the list of ACTIVE projects the
	// authenticated user has access to. Implementations return a
	// Choice indicating an existing project or the user's intent
	// to create a new one.
	PickProject(ctx context.Context, existing []Project) (Choice, error)

	// PickRegion picks a Vertex region. Implementations may show
	// SupportedVertexRegions or accept arbitrary input; the
	// orchestrator only requires a non-empty string.
	PickRegion(ctx context.Context) (string, error)

	// Notify is called for status messages the user should see
	// during setup (e.g. "creating project, this may take 30s",
	// "billing not enabled — Vertex calls will fail until linked").
	// Implementations may discard, print, or render in a TUI panel.
	Notify(format string, args ...any)
}

// Choice carries the user's PickProject decision back to the
// orchestrator. Exactly one of Existing / NewName is meaningful.
type Choice struct {
	// Existing is non-nil when the user picked from the list.
	Existing *Project

	// NewName is non-empty when the user wants a fresh project.
	// Required if Existing is nil; freeform display name (1-30
	// chars). The orchestrator generates a project ID — callers
	// don't pre-fill NewID.
	NewName string

	// NewID is optionally pre-supplied by the picker. When empty,
	// the orchestrator generates a `charon-gemini-<random>` id.
	// Useful for tests that want deterministic IDs.
	NewID string
}

// Result is what Setup returns on success. Callers (CLI, TUI) map
// this onto vault.GCPData when persisting.
type Result struct {
	// Project is the chosen or newly-created project. Always
	// populated on success (filled from the create response /
	// list entry).
	Project Project

	// Region is the Vertex region the user picked.
	Region string

	// EnabledServices is the set of API IDs charon ensured were
	// enabled. May exceed what was newly enabled — the call is
	// idempotent and the response doesn't distinguish.
	EnabledServices []string

	// BillingEnabled is the project's billing state at the end of
	// setup. False is non-fatal (AI Studio still works); the
	// picker is notified so the user can act.
	BillingEnabled bool

	// CreatedNew is true when Setup ran projects.create rather
	// than picking an existing project. Drives the
	// CreatedByCharon flag on the persisted vault.GCPData.
	CreatedNew bool
}

// Setup runs the full M3 flow: list projects → ask picker → maybe
// create → enable required APIs → check billing → ask picker for
// region → return Result. Notifications are emitted via picker so
// the caller controls UX entirely.
//
// Errors abort the flow; partial progress (e.g. project created but
// API enable failed) is surfaced to the picker via Notify so the
// user knows the upstream state, but Setup returns the underlying
// error and the caller decides whether to persist.
func Setup(ctx context.Context, c *Client, picker Picker) (*Result, error) {
	picker.Notify("Listing your Google Cloud projects...")
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	choice, err := picker.PickProject(ctx, projects)
	if err != nil {
		return nil, fmt.Errorf("pick project: %w", err)
	}

	res := &Result{}

	switch {
	case choice.Existing != nil:
		res.Project = *choice.Existing
	case choice.NewName != "":
		id := choice.NewID
		if id == "" {
			id = generateProjectID()
		}
		picker.Notify("Creating project %q (id: %s) — this typically takes 5-30 seconds.", choice.NewName, id)
		op, err := c.CreateProject(ctx, id, choice.NewName, nil)
		if err != nil {
			return nil, fmt.Errorf("create project: %w", err)
		}
		if !op.Done {
			if err := c.WaitOperation(ctx, op.Name, 0); err != nil {
				return nil, fmt.Errorf("wait create operation: %w", err)
			}
		}
		res.Project = Project{
			ProjectID:      id,
			Name:           choice.NewName,
			LifecycleState: "ACTIVE",
		}
		res.CreatedNew = true
	default:
		return nil, fmt.Errorf("picker returned empty choice (no Existing, no NewName)")
	}

	picker.Notify("Enabling required APIs on %s: %s", res.Project.ProjectID, strings.Join(RequiredServices, ", "))
	if err := c.BatchEnableServices(ctx, res.Project.ProjectID, RequiredServices); err != nil {
		return nil, fmt.Errorf("enable services: %w", err)
	}
	res.EnabledServices = append([]string(nil), RequiredServices...)

	billing, err := c.GetBillingInfo(ctx, res.Project.ProjectID)
	if err != nil {
		// Non-fatal: billing detection is informational. Surface and
		// proceed so we still get the user a usable project.
		picker.Notify("Couldn't read billing info (%v) — proceeding anyway. AI Studio (free tier) will work; Vertex calls may fail until billing is linked.", err)
	} else {
		res.BillingEnabled = billing.BillingEnabled
		if !billing.BillingEnabled {
			picker.Notify("Billing not linked on %s. AI Studio (free tier) works; Vertex calls return BILLING_DISABLED until you link a billing account at https://console.cloud.google.com/billing/linkedaccount?project=%s", res.Project.ProjectID, res.Project.ProjectID)
		}
	}

	region, err := picker.PickRegion(ctx)
	if err != nil {
		return nil, fmt.Errorf("pick region: %w", err)
	}
	if strings.TrimSpace(region) == "" {
		return nil, fmt.Errorf("picker returned empty region")
	}
	res.Region = region

	return res, nil
}

// generateProjectID returns a globally-unique project id starting
// with `charon-gemini-` so the user can identify charon-created
// projects in Cloud Console. 8 hex chars = 32 bits of entropy,
// adequate for the per-user namespace where collisions are
// vanishingly unlikely.
func generateProjectID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing on Darwin/Linux is "machine is
		// extremely broken" — there's no graceful fallback that
		// preserves global uniqueness, so panic.
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return "charon-gemini-" + hex.EncodeToString(buf[:])
}
