// Package vault defines the credential storage interface and types.
package vault

import "time"

// Credential types. Empty Type is treated as TypeOAuth for backward
// compatibility with pre-#13 entries that predate the discriminator.
const (
	TypeOAuth    = "oauth"
	TypeAdminKey = "admin-key"
	TypeCatalog  = "catalog"
)

// Credential represents a stored credential for a service account.
//
// The Type discriminator selects which payload is meaningful:
//   - TypeOAuth ("" treated as oauth): the flat AccessToken / RefreshToken
//     / Expiry / Scopes fields below. Kept flat (rather than nested in an
//     OAuthData struct) for backward compat with existing keychain entries
//     and to avoid churn across the OAuth/proxy/TUI call sites that read
//     these fields directly. Future cleanup may lift them into a nested
//     OAuthData; tracked as a follow-up to #13.
//   - TypeAdminKey: AdminKey payload populated; flat OAuth fields unused.
//   - TypeCatalog:  Catalog payload populated; flat OAuth fields unused.
//
// At most one of {flat OAuth fields, AdminKey, Catalog} is populated per
// credential. Wrong-payload mixes indicate a bug.
//
// GCP is a sidecar payload that *augments* TypeOAuth credentials for Google
// accounts that have granted cloud-platform; it stores the project_id and
// region needed for Vertex / AI Studio URLs. Lives on the same credential
// because lifecycle and ACL match the OAuth tokens.
type Credential struct {
	Type     string `json:"type,omitempty"`
	Provider string `json:"provider"`
	Account  string `json:"account"`

	// OAuth payload (flat for backward compat). Valid when Type is
	// TypeOAuth or empty.
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`

	// Type-specific payloads. Exactly one is populated when Type !=
	// TypeOAuth.
	AdminKey *AdminKeyData `json:"admin_key,omitempty"`
	Catalog  *CatalogData  `json:"catalog,omitempty"`

	// GCP is an optional sidecar on TypeOAuth Google credentials.
	// Populated when the user grants cloud-platform and runs charon's
	// project setup (issue #14 M3). Independent of the OAuth payload —
	// can be present or absent regardless of token state.
	GCP *GCPData `json:"gcp,omitempty"`
}

// AdminKeyData is the per-account payload for TypeAdminKey credentials
// (OpenAI projects, Anthropic workspaces, …). The admin key itself is
// stored under a separate keychain entry keyed by OrgID — see
// workshop/plans/000013-…-plan.md § "Keychain layout".
type AdminKeyData struct {
	// OrgID is the opaque upstream organization id (e.g. OpenAI's
	// "org-aB3cD4…", Anthropic's UUID). Stable join key for the admin
	// key entry and same-org-replace detection.
	OrgID string `json:"org_id"`
	// OrgLabel is the user-typed mnemonic captured at admin-key setup
	// (e.g. "xianxu@gmail.com"). Survives upstream renames.
	OrgLabel string `json:"org_label,omitempty"`
	// OrgName is the discovered upstream display name (e.g. "acme-inc").
	// May drift if the user renames upstream.
	OrgName string `json:"org_name,omitempty"`

	// ProjectID is the upstream project/workspace id (proj_… or ws_…).
	ProjectID string `json:"project_id"`
	// ProjectName is the human-readable project/workspace name.
	ProjectName string `json:"project_name,omitempty"`
	// KeyID is the upstream-side api-key id; used for revoke calls.
	KeyID string `json:"key_id"`
	// KeyMaterial is the minted API key (sk-…/sk-ant-…). Captured at
	// mint time and never refetchable upstream.
	KeyMaterial string    `json:"key_material"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

// GCPData is the sidecar payload for Google OAuth credentials whose
// holder has granted cloud-platform and run charon's project setup.
// Records the GCP project + region needed to construct Vertex /
// AI Studio URLs and mint API Keys API requests.
//
// Lifecycle: charon creates and tracks the project (CreatedByCharon
// flag captures whether this charon instance ran the create call),
// but never deletes projects — even ones it created. Users delete
// in Cloud Console where billing / dependency review happens. See
// issue #14 § "Lifecycle of GCP artifacts".
type GCPData struct {
	// ProjectID is the immutable lowercase-alnum-hyphen identifier
	// (6-30 chars) used in URL paths.
	ProjectID string `json:"project_id"`
	// ProjectName is the human-readable display name. May change
	// upstream without invalidating ProjectID.
	ProjectName string `json:"project_name,omitempty"`
	// Parent is null for personal/no-org projects (the MVP default).
	// Reserved for a future org-aware UI; the field is present so a
	// later flow needs zero schema migration. See issue #14 §
	// "Open questions / Project under organization vs no-org".
	Parent *GCPParent `json:"parent,omitempty"`
	// VertexRegion is the default region for Vertex AI URLs (e.g.
	// "us-central1"). Agents may override per-request.
	VertexRegion string `json:"vertex_region,omitempty"`
	// CreatedByCharon is true when this charon instance ran the
	// projects.create call. Informational only — see Lifecycle.
	CreatedByCharon bool `json:"created_by_charon,omitempty"`
	// BillingEnabled mirrors cloudbilling.projects.getBillingInfo at
	// the time of last sync. Used to drive the "Vertex will fail with
	// BILLING_DISABLED" warning. Stale after upstream changes; charon
	// refreshes opportunistically.
	BillingEnabled bool      `json:"billing_enabled,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// GCPParent identifies a project's containing organization or folder.
// Mirrors gcp.Parent; lives in vault to avoid the import cycle.
type GCPParent struct {
	Type string `json:"type"` // "organization" or "folder"
	ID   string `json:"id"`
}

// CatalogData is the per-account payload for TypeCatalog credentials
// (Tier 3 long-tail providers — see issue #15). No admin/mint API; the
// user pastes a static key.
type CatalogData struct {
	KeyMaterial string    `json:"key_material"`
	AddedAt     time.Time `json:"added_at,omitempty"`
}

// CredType returns Type with empty values normalized to TypeOAuth so
// callers can switch on a single canonical value without juggling the
// "" legacy case.
func (c *Credential) CredType() string {
	if c.Type == "" {
		return TypeOAuth
	}
	return c.Type
}

// GracePeriod is how far before expiry a token is considered expired.
const GracePeriod = 30 * time.Second

// IsExpired returns true if the access token has expired or will expire within the grace period.
func (c *Credential) IsExpired() bool {
	return c.IsExpiredAt(time.Now())
}

// IsExpiredAt returns true if the access token is expired at the given time.
func (c *Credential) IsExpiredAt(now time.Time) bool {
	if c.AccessToken == "" {
		return true
	}
	if c.Expiry.IsZero() {
		return false // manual tokens with no expiry never expire
	}
	return now.After(c.Expiry.Add(-GracePeriod))
}

// Store is the interface for credential storage backends.
//
// Cross-backend contract: List returns full Credential structs with
// AccessToken stripped. All backends (memory, devFile, keychain prod
// and CLI fallback) honor this — callers may inspect Type / AdminKey
// / Catalog payloads on the returned creds without an extra Get
// round-trip. Backend implementations that can't return full
// credentials cheaply (e.g. keychain on prod) Get each entry
// internally; entries that fail individual reads are skipped silently
// rather than failing the whole List.
type Store interface {
	// Get retrieves a credential by provider and account.
	Get(provider, account string) (*Credential, error)

	// Set stores a credential.
	Set(cred *Credential) error

	// Delete removes a credential.
	Delete(provider, account string) error

	// List returns all stored credentials with AccessToken stripped.
	// See the Store interface comment for the full contract.
	List() ([]*Credential, error)
}
