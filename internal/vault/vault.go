// Package vault defines the credential storage interface and types.
package vault

import "time"

// Credential represents a stored credential for a service account.
type Credential struct {
	Provider     string    `json:"provider"`      // e.g. "google"
	Account      string    `json:"account"`       // e.g. "user@gmail.com"
	AccessToken  string    `json:"access_token"`  // short-lived, cached in memory
	RefreshToken string    `json:"refresh_token"` // long-lived, stored in vault
	Expiry       time.Time `json:"expiry"`        // when access_token expires
	Scopes       []string  `json:"scopes"`        // granted scopes
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
