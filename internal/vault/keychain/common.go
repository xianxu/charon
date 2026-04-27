// Package keychain implements vault.Store using the OS keychain.
//
// On darwin with CGo enabled the backend lives in keychain_darwin.go and
// calls the macOS Security framework directly. On other platforms (or
// `CGO_ENABLED=0`) the fallback in keychain.go shells out to the macOS
// `security` CLI; that path is intended for hermetic CI / non-darwin
// development and does not support keychain ACLs.
package keychain

import (
	"time"

	"github.com/xianxu/charon/internal/vault"
)

// serviceName is the macOS Keychain "service" attribute under which
// charon stores all of its entries (OAuth tokens, CA cert, CA key).
//
// M3 swaps this out for a runtime-resolved value so an unsigned dev
// binary uses a separate `charon-dev` namespace and doesn't collide
// with the signed binary's ACL'd entries.
const serviceName = "charon"

// keyName builds the per-entry account key. Entries are stored as one
// row per (provider, account); the account attribute is rendered as
// `<provider>:<account>` so a single service name covers all
// credentials.
func keyName(provider, account string) string {
	return provider + ":" + account
}

// storedCredential is the JSON blob persisted in keychain. We store
// access_token alongside refresh_token for manual-token flows; OAuth
// refresh-only flows leave access_token empty and rely on the in-memory
// proxy cache.
type storedCredential struct {
	Provider     string    `json:"provider"`
	Account      string    `json:"account"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
}

func fromCredential(c *vault.Credential) storedCredential {
	return storedCredential{
		Provider:     c.Provider,
		Account:      c.Account,
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		Expiry:       c.Expiry,
		Scopes:       c.Scopes,
	}
}

func (sc storedCredential) toCredential() *vault.Credential {
	return &vault.Credential{
		Provider:     sc.Provider,
		Account:      sc.Account,
		AccessToken:  sc.AccessToken,
		RefreshToken: sc.RefreshToken,
		Expiry:       sc.Expiry,
		Scopes:       sc.Scopes,
	}
}
