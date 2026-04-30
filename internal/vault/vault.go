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
type Store interface {
	// Get retrieves a credential by provider and account.
	Get(provider, account string) (*Credential, error)

	// Set stores a credential.
	Set(cred *Credential) error

	// Delete removes a credential.
	Delete(provider, account string) error

	// List returns all stored credentials (without access tokens).
	List() ([]*Credential, error)
}
